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
	"github.com/michael112233/pbft/logger"
	"github.com/michael112233/pbft/transportpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
)

const (
	rpcTimeout      = 60 * time.Second
	maxGRPCMsgBytes = 32 * 1024 * 1024
)

type clientStreamState struct {
	stream transportpb.PBFTTransport_ClientNodeChannelServer
	sendMu sync.Mutex
}

type NodeMessageHub struct {
	transportpb.UnimplementedPBFTTransportServer

	node_ref *Node
	log      *logger.Logger

	mu        sync.RWMutex
	clients   map[string]transportpb.PBFTTransportClient
	conns     map[string]*grpc.ClientConn
	grpcSrv   *grpc.Server
	listener  net.Listener
	closeOnce sync.Once

	clientStreamMu sync.RWMutex
	clientStream   *clientStreamState
}

func NewNodeMessageHub() *NodeMessageHub {
	return &NodeMessageHub{
		clients: make(map[string]transportpb.PBFTTransportClient),
		conns:   make(map[string]*grpc.ClientConn),
	}
}

func (hub *NodeMessageHub) Start(node *Node, wg *sync.WaitGroup) {
	if node == nil {
		return
	}
	hub.node_ref = node
	hub.log = node.log

	lis, err := net.Listen("tcp", hub.node_ref.GetAddr())
	if err != nil {
		hub.log.Error("Error setting up gRPC listener: err=%v", err)
		return
	}

	hub.mu.Lock()
	hub.listener = lis
	hub.grpcSrv = grpc.NewServer(
		grpc.MaxRecvMsgSize(maxGRPCMsgBytes),
		grpc.MaxSendMsgSize(maxGRPCMsgBytes),
	)
	transportpb.RegisterPBFTTransportServer(hub.grpcSrv, hub)
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

		hub.mu.Lock()
		grpcSrv := hub.grpcSrv
		listener := hub.listener
		conns := make([]*grpc.ClientConn, 0, len(hub.conns))
		for _, conn := range hub.conns {
			conns = append(conns, conn)
		}
		hub.clients = make(map[string]transportpb.PBFTTransportClient)
		hub.conns = make(map[string]*grpc.ClientConn)
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
		for _, conn := range conns {
			_ = conn.Close()
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

func (hub *NodeMessageHub) ClientNodeChannel(stream transportpb.PBFTTransport_ClientNodeChannelServer) error {
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
			go hub.node_ref.HandleRequestMessage(data)

		case core.MsgCloseMessage:
			return nil

		default:
			hub.log.Error("Unknown stream message type received: msgType=%s", env.MsgType)
		}
	}
}

func (hub *NodeMessageHub) Deliver(_ context.Context, env *transportpb.Envelope) (*transportpb.Ack, error) {
	if hub.node_ref.dead {
		hub.log.Info("Node is dead. Ignoring message from %d", env.From)
		return &transportpb.Ack{Ok: true}, nil
	}
	switch env.MsgType {
	case core.MsgRequestMessage:
		request := env.GetRequest()
		if request == nil {
			return &transportpb.Ack{Ok: false, Error: "missing request body"}, nil
		}
		data, err := transportpb.RequestFromPB(request)
		if err != nil {
			return &transportpb.Ack{Ok: false, Error: err.Error()}, nil
		}
		go hub.node_ref.HandleRequestMessage(data)
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
		go hub.node_ref.HandlePrePrepare(data, env.Signature)
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
		go hub.node_ref.HandlePrepare(data, env.Signature)
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
		go hub.node_ref.HandleCommit(data)
		return &transportpb.Ack{Ok: true}, nil

	case core.MsgCheckpointMessage:
		checkpoint := env.GetCheckpoint()
		if checkpoint == nil {
			return &transportpb.Ack{Ok: false, Error: "missing checkpoint body"}, nil
		}
		if int(checkpoint.From) != int(env.From) {
			return &transportpb.Ack{Ok: false, Error: "checkpoint sender mismatch"}, nil
		}
		if !hub.verifySignature(int(env.From), env.Signature, checkpoint) {
			return &transportpb.Ack{Ok: false, Error: "signature verification failed"}, nil
		}
		data, err := transportpb.CheckpointFromPB(checkpoint)
		if err != nil {
			return &transportpb.Ack{Ok: false, Error: err.Error()}, nil
		}
		go hub.node_ref.HandleCheckpoint(data)
		return &transportpb.Ack{Ok: true}, nil
	case core.MsgViewChangeMessage:
		viewChange := env.GetViewChange()
		if viewChange == nil {
			return &transportpb.Ack{Ok: false, Error: "missing view change body"}, nil
		}
		if !hub.verifySignature(int(env.From), env.Signature, viewChange) {
			// hub.log.Error("Signature verification failed for ViewChange message from node ID: %d", env.From)
			return &transportpb.Ack{Ok: false, Error: "signature verification failed"}, nil
		}
		data, err := transportpb.ViewChangeFromPB(viewChange)
		if err != nil {
			return &transportpb.Ack{Ok: false, Error: err.Error()}, nil
		}
		go hub.node_ref.HandleViewChange(data, env.Signature)
		return &transportpb.Ack{Ok: true}, nil

	// case core.MsgGrantVoteMessage:
	// 	// grantVote := env.GetGrantVote()
	// 	// if grantVote == nil {
	// 	// 	return &transportpb.Ack{Ok: false, Error: "missing grant vote body"}, nil
	// 	// }
	// 	// if !hub.verifySignature(int(env.From), env.Signature, grantVote) {
	// 	// 	return &transportpb.Ack{Ok: false, Error: "signature verification failed"}, nil
	// 	// }
	// 	// data, err := transportpb.GrantVoteFromPB(grantVote)
	// 	// if err != nil {
	// 	// 	return &transportpb.Ack{Ok: false, Error: err.Error()}, nil
	// 	// }
	// 	// go hub.node_ref.HandleGrantVote(data, env.Signature)
	// 	// return &transportpb.Ack{Ok: true}, nil

	case core.MsgNewViewMessage:
		newView := env.GetNewView()
		if newView == nil {
			return &transportpb.Ack{Ok: false, Error: "missing new view body"}, nil
		}
		if !hub.verifySignature(int(env.From), env.Signature, newView) {
			return &transportpb.Ack{Ok: false, Error: "signature verification failed"}, nil
		}
		data, err := transportpb.NewViewFromPB(newView)
		if err != nil {
			return &transportpb.Ack{Ok: false, Error: err.Error()}, nil
		}
		go hub.node_ref.HandleNewView(data, env.Signature)
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

func (hub *NodeMessageHub) getOrCreateClient(addr string) (transportpb.PBFTTransportClient, error) {
	hub.mu.RLock()
	client, ok := hub.clients[addr]
	hub.mu.RUnlock()
	if ok {
		return client, nil
	}

	conn, err := grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(maxGRPCMsgBytes),
			grpc.MaxCallSendMsgSize(maxGRPCMsgBytes),
		),
	)
	if err != nil {
		return nil, err
	}

	createdClient := transportpb.NewPBFTTransportClient(conn)

	hub.mu.Lock()
	if existingClient, exists := hub.clients[addr]; exists {
		hub.mu.Unlock()
		_ = conn.Close()
		return existingClient, nil
	}
	hub.clients[addr] = createdClient
	hub.conns[addr] = conn
	hub.mu.Unlock()

	return createdClient, nil
}

func (hub *NodeMessageHub) dropClient(addr string) {
	hub.mu.Lock()
	conn, ok := hub.conns[addr]
	if ok {
		delete(hub.conns, addr)
		delete(hub.clients, addr)
	}
	hub.mu.Unlock()
	if ok {
		_ = conn.Close()
	}
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

	case core.MsgCheckpointMessage:
		checkpoint, ok := msg.(core.CheckpointMsg)
		if !ok {
			return nil, errInvalidPayloadType(msgType, msg)
		}
		pbMsg := transportpb.CheckpointToPB(checkpoint)
		env.Body = &transportpb.Envelope_Checkpoint{Checkpoint: pbMsg}
		env.From = int32(hub.node_ref.GetNodeID())
	case core.MsgViewChangeMessage:
		viewChange, ok := msg.(core.ViewChangeMsg)
		if !ok {
			return nil, errInvalidPayloadType(msgType, msg)
		}
		pbMsg := transportpb.ViewChangeToPB(viewChange)
		env.Body = &transportpb.Envelope_ViewChange{ViewChange: pbMsg}
		env.From = int32(hub.node_ref.GetNodeID())
		// env.Signature = crypto.SignMessageEd25519(payloadBytes, hub.node_ref.encryptionKeyStore.GetPrivateKey())

	case core.MsgGrantVoteMessage:
		grantVote, ok := msg.(core.GrantVoteMsg)
		if !ok {
			return nil, errInvalidPayloadType(msgType, msg)
		}
		pbMsg := transportpb.GrantVoteToPB(grantVote)
		env.Body = &transportpb.Envelope_GrantVote{GrantVote: pbMsg}
		env.From = int32(hub.node_ref.GetNodeID())

	case core.MsgNewViewMessage:
		newView, ok := msg.(core.NewViewMsg)
		if !ok {
			return nil, errInvalidPayloadType(msgType, msg)
		}
		pbMsg := transportpb.NewViewToPB(newView)
		env.Body = &transportpb.Envelope_NewView{NewView: pbMsg}
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
	if msgType == core.MsgReplyMessage || msgType == core.MsgCommitTpsMessage {
		env, err := hub.buildEnvelope(msgType, msg, signature)
		if err != nil {
			hub.log.Error("build envelope failed. msgType=%s err=%v", msgType, err)
			return
		}
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

	client, err := hub.getOrCreateClient(ip)
	if err != nil {
		hub.log.Error("dial target failed. msgType=%s target=%s err=%v", msgType, ip, err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()

	ack, err := client.Deliver(ctx, env)
	if err != nil {
		hub.log.Error("deliver rpc failed. msgType=%s target=%s err=%v", msgType, ip, err)
		hub.dropClient(ip)
		return
	}
	if ack != nil && !ack.Ok {
		hub.log.Error("deliver rejected. msgType=%s target=%s err=%s", msgType, ip, ack.Error)
	}

}
