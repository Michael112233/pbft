package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/logger"
	"github.com/michael112233/pbft/transportpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	clientDialTimeout   = 3 * time.Second
	streamRetryInterval = 500 * time.Millisecond
	sendWaitTimeout     = 5 * time.Second
	maxGRPCMsgBytes     = 256 * 1024 * 1024
)

type nodeStreamState struct {
	conn   *grpc.ClientConn
	client transportpb.PBFTTransportClient
	stream transportpb.PBFTTransport_ClientNodeChannelClient
	sendMu sync.Mutex
}

type ClientMessageHub struct {
	client_ref *Client
	log        *logger.Logger

	mu      sync.RWMutex
	streams map[string]*nodeStreamState

	ctx       context.Context
	cancel    context.CancelFunc
	workersWG sync.WaitGroup
	closeOnce sync.Once
}

func NewClientMessageHub() *ClientMessageHub {
	return &ClientMessageHub{
		streams: make(map[string]*nodeStreamState),
	}
}

func (hub *ClientMessageHub) Start(client *Client, _ *sync.WaitGroup) {
	if client == nil {
		return
	}
	hub.client_ref = client
	hub.log = client.log
	hub.ctx, hub.cancel = context.WithCancel(context.Background())

	for _, nodeAddr := range config.NodeAddr {
		hub.workersWG.Add(1)
		go hub.maintainNodeStream(nodeAddr)
	}

	hub.log.Info("clientMessageHub started")
	hub.log.Info("client stream workers started for %d nodes", len(config.NodeAddr))
}

func (hub *ClientMessageHub) Close() {
	hub.closeOnce.Do(func() {
		hub.log.Debug("clientMessageHub closing...")
		if hub.cancel != nil {
			hub.cancel()
		}
		hub.workersWG.Wait()

		hub.mu.Lock()
		streams := make([]*nodeStreamState, 0, len(hub.streams))
		for _, stream := range hub.streams {
			streams = append(streams, stream)
		}
		hub.streams = make(map[string]*nodeStreamState)
		hub.mu.Unlock()

		for _, stream := range streams {
			if stream != nil && stream.conn != nil {
				_ = stream.conn.Close()
			}
		}
		hub.log.Debug("clientMessageHub is close.")
	})
}

func (hub *ClientMessageHub) setNodeStream(addr string, stream *nodeStreamState) {
	hub.mu.Lock()
	hub.log.Debug("setNodeStream: target=%s streamExists=%t", addr, hub.streams[addr] != nil)
	hub.streams[addr] = stream
	hub.mu.Unlock()
}

func (hub *ClientMessageHub) clearNodeStream(addr string, expected *nodeStreamState) {
	hub.mu.Lock()
	hub.log.Debug("clearNodeStream: target=%s streamExists=%t", addr, hub.streams[addr] != nil)
	if current, ok := hub.streams[addr]; ok && current == expected {
		delete(hub.streams, addr)
	}
	hub.mu.Unlock()
}

func (hub *ClientMessageHub) getNodeStream(addr string) *nodeStreamState {
	hub.mu.RLock()
	// hub.log.Debug("getNodeStream: target=%s streamExists=%t", addr, hub.streams[addr] != nil)
	stream := hub.streams[addr]
	hub.mu.RUnlock()
	return stream
}

func (hub *ClientMessageHub) openNodeStream(addr string) (*nodeStreamState, error) {
	dialer := &net.Dialer{}
	localHost, _, err := net.SplitHostPort(hub.client_ref.GetAddr())
	if err != nil {
		return nil, err
	}
	if localIP := net.ParseIP(localHost); localIP != nil {
		dialer.LocalAddr = &net.TCPAddr{IP: localIP, Port: 0}
	}

	conn, err := grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, target string) (net.Conn, error) {
			conn, err := dialer.DialContext(ctx, "tcp", target)
			if err == nil {
				hub.log.Info("client dialed %s from %s", target, conn.LocalAddr())
			}
			return conn, err
		}),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(maxGRPCMsgBytes),
			grpc.MaxCallSendMsgSize(maxGRPCMsgBytes),
		),
	)
	if err != nil {
		return nil, err
	}

	client := transportpb.NewPBFTTransportClient(conn)
	// Use hub.ctx for stream lifetime. A short timeout context here would
	// cancel the stream immediately after openNodeStream returns.
	stream, err := client.ClientNodeChannel(hub.ctx)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}

	return &nodeStreamState{
		conn:   conn,
		client: client,
		stream: stream,
	}, nil
}

func (hub *ClientMessageHub) maintainNodeStream(addr string) {
	defer hub.workersWG.Done()

	for {
		if hub.ctx.Err() != nil {
			return
		}

		state, err := hub.openNodeStream(addr)
		if err != nil {
			hub.log.Debug("open stream failed. target=%s err=%v", addr, err)
			select {
			case <-hub.ctx.Done():
				return
			case <-time.After(streamRetryInterval):
			}
			continue
		}

		hub.setNodeStream(addr, state)
		hub.log.Debug("stream connected. target=%s", addr)

		err = hub.recvLoop(addr, state)
		hub.clearNodeStream(addr, state)
		_ = state.conn.Close()

		if hub.ctx.Err() != nil {
			hub.log.Debug("stream check closed due to context cancellation. target=%s", addr)
			return
		}
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
			hub.log.Debug("stream recv ended. target=%s err=%v", addr, err)
		}

		select {
		case <-hub.ctx.Done():
			return
		case <-time.After(streamRetryInterval):
		}
	}
}

func (hub *ClientMessageHub) recvLoop(addr string, state *nodeStreamState) error {
	for {
		env, err := state.stream.Recv()
		if err != nil {
			hub.log.Debug("stream check recv error. target=%s err=%v", addr, err)
			return err
		}
		hub.handleIncomingEnvelope(addr, env)
	}
}

func (hub *ClientMessageHub) handleIncomingEnvelope(addr string, env *transportpb.Envelope) {
	switch env.MsgType {
	case core.MsgReplyMessage:
		reply := env.GetReply()
		if reply == nil {
			hub.log.Error("stream reply missing body. target=%s", addr)
			return
		}
		data, err := transportpb.ReplyFromPB(reply)
		if err != nil {
			hub.log.Error("stream reply decode failed. target=%s err=%v", addr, err)
			return
		}
		go hub.client_ref.HandleReplyMessage(data)

	case core.MsgCommitTpsMessage:
		commitTps := env.GetCommitTps()
		if commitTps == nil {
			hub.log.Error("stream commitTps missing body. target=%s", addr)
			return
		}
		data, err := transportpb.CommitTpsFromPB(commitTps)
		if err != nil {
			hub.log.Error("stream commitTps decode failed. target=%s err=%v", addr, err)
			return
		}
		go hub.client_ref.HandleCommitTpsMessage(data)

	case core.MsgLeaderIdUpdateMessage:
		leaderUpdate := env.GetLeaderIdUpdate()
		if leaderUpdate == nil {
			hub.log.Error("stream leaderIdUpdate missing body. target=%s", addr)
			return
		}
		data, err := transportpb.LeaderIdUpdateFromPB(leaderUpdate)
		if err != nil {
			hub.log.Error("stream leaderIdUpdate decode failed. target=%s err=%v", addr, err)
			return
		}
		go hub.client_ref.HandleLeaderUpdate(data)

	case core.MsgVCRunningStatusMessage:
		vcRunningStatus := env.GetVcRunningStatus()
		if vcRunningStatus == nil {
			hub.log.Error("stream vcRunningStatus missing body. target=%s", addr)
			return
		}
		data, err := transportpb.VCRunningStatusFromPB(vcRunningStatus)
		if err != nil {
			hub.log.Error("stream vcRunningStatus decode failed. target=%s err=%v", addr, err)
			return
		}
		go hub.client_ref.HandleVCRunningStatus(data)

	default:
		hub.log.Error("Unknown stream message type received: msgType=%s target=%s", env.MsgType, addr)
	}
}

func (hub *ClientMessageHub) sendToNodeStream(addr string, env *transportpb.Envelope) error {
	deadline := time.Now().Add(sendWaitTimeout)
	for {
		state := hub.getNodeStream(addr)
		if state != nil {
			state.sendMu.Lock()
			err := state.stream.Send(env)
			state.sendMu.Unlock()
			if err != nil {
				hub.clearNodeStream(addr, state)
				_ = state.conn.Close()
				return err
			}
			return nil
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("stream not ready for target %s", addr)
		}

		select {
		case <-hub.ctx.Done():
			return errors.New("client message hub is shutting down")
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (hub *ClientMessageHub) buildEnvelope(msgType string, msg interface{}) (*transportpb.Envelope, error) {
	env := &transportpb.Envelope{MsgType: msgType}

	switch msgType {
	case core.MsgRequestMessage:
		request, ok := msg.(core.RequestMessage)
		if !ok {
			return nil, fmt.Errorf("invalid payload type for %s: %T", msgType, msg)
		}
		env.Body = &transportpb.Envelope_Request{Request: transportpb.RequestToPB(request)}

	case core.MsgRetryMessage:
		retry, ok := msg.(core.RetryMessage)
		if !ok {
			return nil, fmt.Errorf("invalid payload type for %s: %T", msgType, msg)
		}
		env.Body = &transportpb.Envelope_Retry{Retry: transportpb.RetryToPB(retry)}

	case core.MsgCloseMessage:
		closeMsg, ok := msg.(core.CloseMessage)
		if !ok {
			return nil, fmt.Errorf("invalid payload type for %s: %T", msgType, msg)
		}
		env.Body = &transportpb.Envelope_Close{Close: transportpb.CloseToPB(closeMsg)}

	default:
		return nil, fmt.Errorf("unsupported message type %s", msgType)
	}

	return env, nil
}

func (hub *ClientMessageHub) injectArtificialLatency(msgType, from, to string) {
	if hub == nil || hub.client_ref == nil || hub.client_ref.config == nil {
		return
	}
	delay := hub.client_ref.config.ArtificialLatency(from, to)
	if delay <= 0 {
		return
	}
	hub.log.Info("Injecting artificial latency. msgType=%s from=%s to=%s delay=%s", msgType, from, to, delay)
	time.Sleep(delay)
}

func (hub *ClientMessageHub) Send(msgType string, from string, to string, msg interface{}, callback func(...interface{})) {
	env, err := hub.buildEnvelope(msgType, msg)
	if err != nil {
		hub.log.Error("build envelope failed. msgType=%s err=%v", msgType, err)
		return
	}

	// hub.injectArtificialLatency(msgType, from, to)

	if err := hub.sendToNodeStream(to, env); err != nil {
		hub.log.Error("stream send failed. msgType=%s target=%s err=%v", msgType, to, err)
		return
	}

	if callback != nil {
		callback()
	}

	if msgType == core.MsgRequestMessage {
		// if request, ok := msg.(core.RequestMessage); ok {
		// 	// hub.log.Info("Msg Sent: MsgRequestMessage, From %s, To %s, Txs %d", from, to, len(request.Txs))
		// }
	}
}
