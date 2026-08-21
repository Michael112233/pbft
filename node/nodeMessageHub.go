package node

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/crypto"
	"github.com/michael112233/pbft/learningagentpb"
	"github.com/michael112233/pbft/logger"
	"github.com/michael112233/pbft/transportpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

const (
	maxGRPCMsgBytes           = 1000 * 1024 * 1024
	grpcFlowControlWindowSize = 8 * 1024 * 1024
)

type clientStreamState struct {
	stream transportpb.PBFTTransport_ClientNodeChannelServer
	sendMu sync.Mutex
}

type peerStreamState struct {
	conn   *grpc.ClientConn
	stream transportpb.PBFTTransport_ClientNodeChannelClient
	sendMu sync.Mutex
}

type ConsensusMsg struct {
	MsgType   string
	Msg       interface{}
	Signature []byte
}

type ViewChangeMsg struct {
	MsgType   string
	Msg       interface{}
	Signature []byte
}

type CheckpointMsg struct {
	MsgType   string
	Msg       interface{}
	Signature []byte
}

type NewViewMsg struct {
	MsgType   string
	Msg       interface{}
	Signature []byte
}

type NodeMessageHub struct {
	transportpb.UnimplementedPBFTTransportServer

	node_ref *Node
	log      *logger.Logger

	mu           sync.RWMutex
	peerStreams  map[string]*peerStreamState
	streamCtx    context.Context
	streamCancel context.CancelFunc
	grpcSrv      *grpc.Server
	listener     net.Listener
	dialContext  func(context.Context, string) (net.Conn, error)
	closeOnce    sync.Once

	clientStreamMu sync.RWMutex
	clientStream   *clientStreamState
}

func NewNodeMessageHub() *NodeMessageHub {
	return &NodeMessageHub{
		peerStreams: make(map[string]*peerStreamState),
	}
}

func (hub *NodeMessageHub) Start(node *Node, wg *sync.WaitGroup) {
	if node == nil {
		return
	}
	hub.node_ref = node
	hub.log = node.log
	hub.streamCtx, hub.streamCancel = context.WithCancel(context.Background())

	lis, err := net.Listen("tcp", hub.node_ref.GetAddr())
	if err != nil {
		hub.log.Error("Error setting up gRPC listener: err=%v", err)
		return
	}

	hub.mu.Lock()
	hub.listener = lis
	hub.grpcSrv = grpc.NewServer(
		grpc.InitialWindowSize(grpcFlowControlWindowSize),
		grpc.InitialConnWindowSize(grpcFlowControlWindowSize),
		grpc.MaxRecvMsgSize(maxGRPCMsgBytes),
		grpc.MaxSendMsgSize(maxGRPCMsgBytes),
	)
	transportpb.RegisterPBFTTransportServer(hub.grpcSrv, hub)
	if node.learningAgent != nil {
		learningagentpb.RegisterLearningAgentNodeServer(
			hub.grpcSrv,
			node.learningAgent,
		)
	}
	hub.mu.Unlock()

	hub.log.Info("start gRPC listening on %s", hub.node_ref.GetAddr())
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := hub.grpcSrv.Serve(lis); err != nil {
			hub.log.Debug("gRPC server stopped: err=%v", err)
		}
	}()
}

func (hub *NodeMessageHub) Close() {
	hub.closeOnce.Do(func() {
		hub.log.Debug("nodeMessageHub closing...")

		if hub.streamCancel != nil {
			hub.streamCancel()
		}

		hub.mu.Lock()
		grpcSrv := hub.grpcSrv
		listener := hub.listener
		peerStreams := make([]*peerStreamState, 0, len(hub.peerStreams))
		for _, state := range hub.peerStreams {
			peerStreams = append(peerStreams, state)
		}
		hub.peerStreams = make(map[string]*peerStreamState)
		hub.mu.Unlock()

		hub.clientStreamMu.Lock()
		hub.clientStream = nil
		hub.clientStreamMu.Unlock()

		if grpcSrv != nil {
			grpcSrv.Stop()
		}
		if listener != nil {
			_ = listener.Close()
		}
		for _, state := range peerStreams {
			_ = state.conn.Close()
		}
		hub.log.Debug("messageHub is close.")
	})
}

func marshalDeterministic(msg proto.Message) ([]byte, error) {
	return proto.MarshalOptions{Deterministic: true}.Marshal(msg)
}

func errInvalidPayloadType(msgType string, payload interface{}) error {
	return fmt.Errorf("invalid payload type for %s: %T", msgType, payload)
}

func errUnknownMessageType(msgType string) error {
	return fmt.Errorf("unknown message type %s", msgType)
}

func preprepareSignPayload(view, seq int64, digest []byte) *transportpb.PreprepareSignPayload {

	return &transportpb.PreprepareSignPayload{
		View:            view,
		SeqNum:          seq,
		DigestClientMsg: digest,
	}
}

func (hub *NodeMessageHub) verifySignature(from int, signature []byte, payload proto.Message) bool {
	senderPubKey, exists := hub.node_ref.encryptionKeyStore.GetPublicKey(from)
	if !exists {
		hub.log.Error("Public key not found for sender node ID: %d", from)
		return false
	}
	payloadBytes, err := marshalDeterministic(payload)
	if err != nil {
		hub.log.Error("payload marshal failed: err=%v", err)
		return false
	}
	if !crypto.VerifySignatureEd25519(payloadBytes, signature, senderPubKey) {
		hub.log.Error("Signature verification failed for message from node ID: %d", from)
		return false
	}
	return true
}

func (hub *NodeMessageHub) setClientStream(s *clientStreamState) {
	hub.clientStreamMu.Lock()
	hub.clientStream = s
	hub.clientStreamMu.Unlock()
}

func (hub *NodeMessageHub) clearClientStream(s *clientStreamState) {
	hub.clientStreamMu.Lock()
	if hub.clientStream == s {
		hub.clientStream = nil
	}
	hub.clientStreamMu.Unlock()
}

func (hub *NodeMessageHub) sendEnvelopeOverClientStream(env *transportpb.Envelope) error {
	hub.clientStreamMu.RLock()
	streamState := hub.clientStream
	hub.clientStreamMu.RUnlock()
	if streamState == nil {
		return errors.New("client stream is not connected")
	}
	streamState.sendMu.Lock()
	defer streamState.sendMu.Unlock()
	return streamState.stream.Send(env)
}

func (hub *NodeMessageHub) injectArtificialLatency(msgType, targetAddr string) {
	if hub == nil || hub.node_ref == nil || hub.node_ref.cfg == nil {
		return
	}
	fromAddr := hub.node_ref.GetAddr()
	delay := hub.node_ref.cfg.ArtificialLatency(fromAddr, targetAddr)
	if delay <= 0 {
		return
	}
	hub.log.Info("Injecting artificial latency. msgType=%s from=%s to=%s delay=%s", msgType, fromAddr, targetAddr, delay)
	time.Sleep(delay)
}

func (hub *NodeMessageHub) ClientNodeChannel(stream transportpb.PBFTTransport_ClientNodeChannelServer) error {
	if channelKindFromContext(stream.Context()) == transportpb.ChannelKindNode {
		return hub.receiveNodeStream(stream)
	}

	streamState := &clientStreamState{stream: stream}
	hub.setClientStream(streamState)
	defer hub.clearClientStream(streamState)

	for {
		env, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if hub.node_ref.dead {
			hub.log.Info("Node is dead. Ignoring message from client stream.")
			continue
		}
		switch env.MsgType {
		case core.MsgRequestMessage:
			request := env.GetRequest()
			if request == nil {
				continue
			}
			data, err := transportpb.RequestFromPB(request)
			if err != nil {
				hub.log.Error("stream request decode failed: err=%v", err)
				continue
			}
			// hub.node_ref.recordClientRequestReceived(len(data.Txs))
			hub.node_ref.HandleRequestMessage(data)

		case core.MsgCloseMessage:
			return nil

		default:
			hub.log.Error("Unknown stream message type received: msgType=%s", env.MsgType)
		}
	}
}

func channelKindFromContext(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return transportpb.ChannelKindClient
	}
	values := md.Get(transportpb.ChannelKindMetadataKey)
	if len(values) == 0 {
		return transportpb.ChannelKindClient
	}
	return values[0]
}

func (hub *NodeMessageHub) receiveNodeStream(stream transportpb.PBFTTransport_ClientNodeChannelServer) error {
	for {
		env, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
		timeStart := time.Now()
		ack, err := hub.Deliver(stream.Context(), env)
		if err != nil {
			hub.log.Error("node stream delivery failed. msgType=%s from=%d err=%v", env.MsgType, env.From, err)
			continue
		}
		if ack != nil && !ack.Ok {
			hub.log.Error("node stream delivery rejected. msgType=%s from=%d err=%s", env.MsgType, env.From, ack.Error)
		}
		timeElapsed := time.Since(timeStart)
		if timeElapsed > 5*time.Millisecond {
			hub.log.Warn("node stream delivery took too long. msgType=%s from=%d elapsed=%s", env.MsgType, env.From, timeElapsed)
		}
	}
}

func (hub *NodeMessageHub) Deliver(_ context.Context, env *transportpb.Envelope) (*transportpb.Ack, error) {
	// if hub.node_ref.dead {
	// 	hub.log.Info("Node is dead. Ignoring message from %d", env.From)
	// 	return &transportpb.Ack{Ok: true}, nil
	// }
	switch env.MsgType {
	case core.MsgRequestMessage:
		request := env.GetRequest()
		if request == nil {
			return &transportpb.Ack{Ok: false, Error: "missing request body"}, nil
		}
		_, err := transportpb.RequestFromPB(request)
		if err != nil {
			return &transportpb.Ack{Ok: false, Error: err.Error()}, nil
		}
		// hub.node_ref.recordClientRequestReceived(len(data.Txs))
		// go hub.node_ref.HandleRequestMessage(data)
		return &transportpb.Ack{Ok: true}, nil

	case core.MsgPreprepareMessage:
		preprepare := env.GetPreprepare()
		if preprepare == nil {
			return &transportpb.Ack{Ok: false, Error: "missing preprepare body"}, nil
		}
		if !hub.verifySignature(int(env.From), env.Signature, preprepareSignPayload(preprepare.View, preprepare.SeqNum, preprepare.DigestClientMsg)) {
			// hub.log.Error("Signature verification failed for PrePrepare message from node ID: %d", env.From)
			return &transportpb.Ack{Ok: false, Error: "signature verification failed"}, nil
		}
		data, err := transportpb.PreprepareFromPB(preprepare)
		if err != nil {
			return &transportpb.Ack{Ok: false, Error: err.Error()}, nil
		}
		// go hub.node_ref.HandlePrePrepare(data, env.Signature)
		hub.node_ref.consensusMsgChan <- ConsensusMsg{
			MsgType:   core.MsgPreprepareMessage,
			Msg:       data,
			Signature: env.Signature,
		}
		return &transportpb.Ack{Ok: true}, nil

	case core.MsgPrepareMessage:
		prepare := env.GetPrepare()
		if prepare == nil {
			return &transportpb.Ack{Ok: false, Error: "missing prepare body"}, nil
		}
		if !hub.verifySignature(int(env.From), env.Signature, prepare) {
			// hub.log.Error("Signature verification failed for Prepare message from node ID: %d", env.From)
			return &transportpb.Ack{Ok: false, Error: "signature verification failed"}, nil
		}
		data, err := transportpb.PrepareFromPB(prepare)
		if err != nil {
			return &transportpb.Ack{Ok: false, Error: err.Error()}, nil
		}
		// go hub.node_ref.HandlePrepare(data, env.Signature)
		hub.node_ref.consensusMsgChan <- ConsensusMsg{
			MsgType:   core.MsgPrepareMessage,
			Msg:       data,
			Signature: env.Signature,
		}
		return &transportpb.Ack{Ok: true}, nil

	case core.MsgCommitMessage:
		commit := env.GetCommit()
		if commit == nil {
			return &transportpb.Ack{Ok: false, Error: "missing commit body"}, nil
		}
		if !hub.verifySignature(int(env.From), env.Signature, commit) {
			// hub.log.Error("Signature verification failed for Commit message from node ID: %d", env.From)
			return &transportpb.Ack{Ok: false, Error: "signature verification failed"}, nil
		}
		data, err := transportpb.CommitFromPB(commit)
		if err != nil {
			return &transportpb.Ack{Ok: false, Error: err.Error()}, nil
		}
		// go hub.node_ref.HandleCommit(data)
		hub.node_ref.consensusMsgChan <- ConsensusMsg{
			MsgType:   core.MsgCommitMessage,
			Msg:       data,
			Signature: env.Signature,
		}
		return &transportpb.Ack{Ok: true}, nil

	case core.MsgViewChangeMessage:
		viewChange := env.GetViewChange()
		if viewChange == nil {
			return &transportpb.Ack{Ok: false, Error: "missing view-change body"}, nil
		}
		if int(env.From) != int(viewChange.From) {
			return &transportpb.Ack{Ok: false, Error: "view-change sender mismatch"}, nil
		}
		if !hub.verifySignature(int(env.From), env.Signature, viewChange) {
			return &transportpb.Ack{Ok: false, Error: "signature verification failed"}, nil
		}
		data, err := transportpb.ViewChangeFromPB(viewChange)
		if err != nil {
			return &transportpb.Ack{Ok: false, Error: err.Error()}, nil
		}
		sizeBytes := proto.Size(env)
		hub.node_ref.log.Info(
			"HUB: Received ViewChange message from node %d for view %d. size_bytes=%d size_mib=%.3f",
			env.From,
			data.ViewNumber,
			sizeBytes,
			float64(sizeBytes)/(1024*1024),
		)
		hub.node_ref.viewChangeMsgChan <- ViewChangeMsg{
			MsgType:   core.MsgViewChangeMessage,
			Msg:       data,
			Signature: env.Signature,
		}
		return &transportpb.Ack{Ok: true}, nil

	case core.MsgCheckpointMessage:
		checkpoint := env.GetCheckpoint()
		if checkpoint == nil {
			return &transportpb.Ack{Ok: false, Error: "missing checkpoint body"}, nil
		}
		if int(env.From) != int(checkpoint.From) {
			return &transportpb.Ack{Ok: false, Error: "checkpoint sender mismatch"}, nil
		}
		if !hub.verifySignature(int(env.From), env.Signature, checkpoint) {
			return &transportpb.Ack{Ok: false, Error: "signature verification failed"}, nil
		}
		data, err := transportpb.CheckpointFromPB(checkpoint)
		if err != nil {
			return &transportpb.Ack{Ok: false, Error: err.Error()}, nil
		}
		hub.node_ref.checkpointMsgChan <- CheckpointMsg{
			MsgType:   core.MsgCheckpointMessage,
			Msg:       data,
			Signature: env.Signature,
		}
		return &transportpb.Ack{Ok: true}, nil

	case core.MsgNewViewMessage:
		newView := env.GetNewView()
		if newView == nil {
			return &transportpb.Ack{Ok: false, Error: "missing new-view body"}, nil
		}
		if int(env.From) != int(newView.From) {
			return &transportpb.Ack{Ok: false, Error: "new-view sender mismatch"}, nil
		}
		if !hub.verifySignature(int(env.From), env.Signature, newView) {
			return &transportpb.Ack{Ok: false, Error: "signature verification failed"}, nil
		}
		data, err := transportpb.NewViewFromPB(newView)
		if err != nil {
			return &transportpb.Ack{Ok: false, Error: err.Error()}, nil
		}
		sizeBytes := proto.Size(env)
		hub.node_ref.log.Info(
			"HUB: Received NewView message from node %d for view %d. size_bytes=%d size_mib=%.3f",
			env.From,
			data.NewViewNumber,
			sizeBytes,
			float64(sizeBytes)/(1024*1024),
		)
		hub.node_ref.newViewMsgChan <- NewViewMsg{
			MsgType:   core.MsgNewViewMessage,
			Msg:       data,
			Signature: env.Signature,
		}
		return &transportpb.Ack{Ok: true}, nil

	case core.MsgCloseMessage:
		_ = env.GetClose()
		return &transportpb.Ack{Ok: true}, nil

	default:
		errMsg := "unknown message type: " + env.MsgType
		hub.log.Error("%s", errMsg)
		return &transportpb.Ack{Ok: false, Error: errMsg}, nil
	}
}

func (hub *NodeMessageHub) getOrCreatePeerStream(addr string) (*peerStreamState, error) {
	hub.mu.RLock()
	state, ok := hub.peerStreams[addr]
	streamCtx := hub.streamCtx
	hub.mu.RUnlock()
	if ok {
		return state, nil
	}
	if streamCtx == nil {
		return nil, errors.New("node message hub is not started")
	}

	dialContext := hub.dialContext
	if dialContext == nil {
		dialer := &net.Dialer{}
		localHost, _, err := net.SplitHostPort(hub.node_ref.GetAddr())
		if err != nil {
			return nil, err
		}
		if localIP := net.ParseIP(localHost); localIP != nil {
			dialer.LocalAddr = &net.TCPAddr{IP: localIP, Port: 0}
		}
		dialContext = func(ctx context.Context, target string) (net.Conn, error) {
			conn, dialErr := dialer.DialContext(ctx, "tcp", target)
			if dialErr == nil {
				hub.log.Info("node dialed %s from %s", target, conn.LocalAddr())
			}
			return conn, dialErr
		}
	}

	conn, err := grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(dialContext),
		grpc.WithInitialWindowSize(grpcFlowControlWindowSize),
		grpc.WithInitialConnWindowSize(grpcFlowControlWindowSize),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(maxGRPCMsgBytes),
			grpc.MaxCallSendMsgSize(maxGRPCMsgBytes),
		),
	)
	if err != nil {
		return nil, err
	}

	client := transportpb.NewPBFTTransportClient(conn)
	peerCtx := metadata.NewOutgoingContext(
		streamCtx,
		metadata.Pairs(transportpb.ChannelKindMetadataKey, transportpb.ChannelKindNode),
	)
	stream, err := client.ClientNodeChannel(peerCtx)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	createdState := &peerStreamState{conn: conn, stream: stream}

	hub.mu.Lock()
	if existingState, exists := hub.peerStreams[addr]; exists {
		hub.mu.Unlock()
		_ = conn.Close()
		return existingState, nil
	}
	hub.peerStreams[addr] = createdState
	hub.mu.Unlock()
	go hub.watchPeerStream(addr, createdState)

	return createdState, nil
}

func (hub *NodeMessageHub) watchPeerStream(addr string, state *peerStreamState) {
	for {
		_, err := state.stream.Recv()
		if err != nil {
			hub.dropPeerStream(addr, state)
			return
		}
	}
}

func (hub *NodeMessageHub) dropPeerStream(addr string, expected *peerStreamState) {
	hub.mu.Lock()
	state, ok := hub.peerStreams[addr]
	if ok && state == expected {
		delete(hub.peerStreams, addr)
	}
	hub.mu.Unlock()
	if ok && state == expected {
		_ = state.conn.Close()
	}
}

func (hub *NodeMessageHub) sendEnvelopeOverPeerStream(addr string, env *transportpb.Envelope) error {
	state, err := hub.getOrCreatePeerStream(addr)
	if err != nil {
		return err
	}

	state.sendMu.Lock()
	err = state.stream.Send(env)
	state.sendMu.Unlock()
	if err != nil {
		hub.dropPeerStream(addr, state)
	}
	return err
}

func (hub *NodeMessageHub) buildEnvelope(msgType string, msg interface{}, signature []byte) (*transportpb.Envelope, error) {
	env := &transportpb.Envelope{MsgType: msgType, Signature: signature}

	switch msgType {
	case core.MsgRequestMessage:
		request, ok := msg.(core.RequestMessage)
		if !ok {
			return nil, errInvalidPayloadType(msgType, msg)
		}
		env.Body = &transportpb.Envelope_Request{Request: transportpb.RequestToPB(request)}

	case core.MsgPreprepareMessage:
		preprepare, ok := msg.(core.PreprepareMsg)
		if !ok {
			return nil, errInvalidPayloadType(msgType, msg)
		}
		pbMsg := transportpb.PreprepareToPB(preprepare)
		// payloadBytes, err := marshalDeterministic(preprepareSignPayload(pbMsg))
		// if err != nil {
		// 	return nil, err
		// }
		env.Body = &transportpb.Envelope_Preprepare{Preprepare: pbMsg}
		env.From = int32(hub.node_ref.GetNodeID())
		// env.Signature = crypto.SignMessageEd25519(payloadBytes, hub.node_ref.encryptionKeyStore.GetPrivateKey())

	case core.MsgPrepareMessage:
		prepare, ok := msg.(core.PrepareMsg)
		if !ok {
			return nil, errInvalidPayloadType(msgType, msg)
		}
		pbMsg := transportpb.PrepareToPB(prepare)
		// payloadBytes, err := marshalDeterministic(pbMsg)
		// if err != nil {
		// 	return nil, err
		// }
		env.Body = &transportpb.Envelope_Prepare{Prepare: pbMsg}
		env.From = int32(hub.node_ref.GetNodeID())
		// env.Signature = crypto.SignMessageEd25519(payloadBytes, hub.node_ref.encryptionKeyStore.GetPrivateKey())

	case core.MsgCommitMessage:
		commit, ok := msg.(core.CommitMsg)
		if !ok {
			return nil, errInvalidPayloadType(msgType, msg)
		}
		pbMsg := transportpb.CommitToPB(commit)
		// payloadBytes, err := marshalDeterministic(pbMsg)
		// if err != nil {
		// 	return nil, err
		// }
		env.Body = &transportpb.Envelope_Commit{Commit: pbMsg}
		env.From = int32(hub.node_ref.GetNodeID())
		// env.Signature = crypto.SignMessageEd25519(payloadBytes, hub.node_ref.encryptionKeyStore.GetPrivateKey())

	case core.MsgViewChangeMessage:
		viewChange, ok := msg.(core.ViewChangeMsg)
		if !ok {
			return nil, errInvalidPayloadType(msgType, msg)
		}
		env.Body = &transportpb.Envelope_ViewChange{ViewChange: transportpb.ViewChangeToPB(viewChange)}
		env.From = int32(hub.node_ref.GetNodeID())

	case core.MsgCheckpointMessage:
		checkpoint, ok := msg.(core.CheckpointMsg)
		if !ok {
			return nil, errInvalidPayloadType(msgType, msg)
		}
		env.Body = &transportpb.Envelope_Checkpoint{Checkpoint: transportpb.CheckpointToPB(checkpoint)}
		env.From = int32(hub.node_ref.GetNodeID())

	case core.MsgNewViewMessage:
		newView, ok := msg.(core.NewViewMsg)
		if !ok {
			return nil, errInvalidPayloadType(msgType, msg)
		}
		env.Body = &transportpb.Envelope_NewView{NewView: transportpb.NewViewToPB(newView)}
		env.From = int32(hub.node_ref.GetNodeID())

	case core.MsgReplyMessage:
		reply, ok := msg.(core.ReplyMessage)
		if !ok {
			return nil, errInvalidPayloadType(msgType, msg)
		}
		env.Body = &transportpb.Envelope_Reply{Reply: transportpb.ReplyToPB(reply)}

	case core.MsgCommitTpsMessage:
		commitTps, ok := msg.(core.CommitTps)
		if !ok {
			return nil, errInvalidPayloadType(msgType, msg)
		}
		env.Body = &transportpb.Envelope_CommitTps{CommitTps: transportpb.CommitTpsToPB(commitTps)}

	case core.MsgLeaderIdUpdateMessage:
		leaderUpdate, ok := msg.(core.LeaderIdUpdate)
		if !ok {
			return nil, errInvalidPayloadType(msgType, msg)
		}
		env.Body = &transportpb.Envelope_LeaderIdUpdate{LeaderIdUpdate: transportpb.LeaderIdUpdateToPB(leaderUpdate)}

	case core.MsgVCRunningStatusMessage:
		vcRunningStatus, ok := msg.(core.VCRunningStatus)
		if !ok {
			return nil, errInvalidPayloadType(msgType, msg)
		}
		env.Body = &transportpb.Envelope_VcRunningStatus{VcRunningStatus: transportpb.VCRunningStatusToPB(vcRunningStatus)}

	case core.MsgCloseMessage:
		closeMsg, ok := msg.(core.CloseMessage)
		if !ok {
			return nil, errInvalidPayloadType(msgType, msg)
		}
		env.Body = &transportpb.Envelope_Close{Close: transportpb.CloseToPB(closeMsg)}

	default:
		return nil, errUnknownMessageType(msgType)
	}

	return env, nil
}

func (hub *NodeMessageHub) Send(msgType string, ip string, msg interface{}, signature []byte) {
	if hub.node_ref.dead {
		hub.log.Info("Node is dead. Not sending message. msgType=%s target=%s", msgType, ip)
		return
	}
	if msgType == core.MsgReplyMessage || msgType == core.MsgCommitTpsMessage || msgType == core.MsgLeaderIdUpdateMessage || msgType == core.MsgVCRunningStatusMessage {
		env, err := hub.buildEnvelope(msgType, msg, signature)
		if err != nil {
			hub.log.Error("build envelope failed. msgType=%s err=%v", msgType, err)
			return
		}
		// hub.injectArtificialLatency(msgType, ip)
		if err := hub.sendEnvelopeOverClientStream(env); err != nil {
			hub.log.Error("stream deliver failed. msgType=%s target=%s err=%v", msgType, ip, err)
			return
		}

		return
	}

	env, err := hub.buildEnvelope(msgType, msg, signature)
	if err != nil {
		hub.log.Error("build envelope failed. msgType=%s err=%v", msgType, err)
		return
	}

	hub.injectArtificialLatency(msgType, ip)
	timeStart := time.Now()
	sizeBytes := 0
	if msgType == core.MsgNewViewMessage || msgType == core.MsgViewChangeMessage {
		sizeBytes = proto.Size(env)
	}

	if err := hub.sendEnvelopeOverPeerStream(ip, env); err != nil {
		hub.log.Error("node stream send failed. msgType=%s target=%s err=%v", msgType, ip, err)
	}
	if msgType == core.MsgNewViewMessage || msgType == core.MsgViewChangeMessage {
		timeElapsed := time.Since(timeStart)
		hub.node_ref.log.Info(
			"HUB: node stream send for %s took %s. msgType=%s target=%s size_bytes=%d size_mib=%.3f",
			msgType,
			timeElapsed,
			msgType,
			ip,
			sizeBytes,
			float64(sizeBytes)/(1024*1024),
		)
	}

}
