package node

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
	Digest    [32]byte // sha256 of the signed ViewChange bytes (ViewChange only)
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

type ElectionMsg struct {
	MsgType   string
	Msg       interface{}
	Signature []byte
}

type EpochProtocolMsg struct {
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

	// One reply stream per client, keyed by the name the client announces in
	// transportpb.ClientNameMetadataKey (config.LegacyClientName if it does not).
	clientStreamMu sync.RWMutex
	clientStreams  map[string]*clientStreamState
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
		hub.clientStreams = nil
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

func preprepareSignPayload(view core.ViewID, seq int64, digest []byte) *transportpb.PreprepareSignPayload {
	return &transportpb.PreprepareSignPayload{
		View:            transportpb.ViewIDToPB(view),
		SeqNum:          seq,
		DigestClientMsg: digest,
	}
}

func epochAggregateSignPayload(msg *transportpb.EpochAggregateMsg) *transportpb.EpochAggregateMsgMini {
	return &transportpb.EpochAggregateMsgMini{
		EpochGeneration: msg.EpochGeneration,
		From:            msg.From,
		EpochData:       msg.EpochData,
		CurrentAction:   msg.CurrentAction,
	}
}

func (hub *NodeMessageHub) verifySignature(from int, signature []byte, payload proto.Message) bool {
	_, ok := hub.verifySignatureBytes(from, signature, payload)
	return ok
}

// verifySignatureBytes is verifySignature that also returns the signed bytes, so a
// caller can digest them without marshaling the payload a second time.
func (hub *NodeMessageHub) verifySignatureBytes(from int, signature []byte, payload proto.Message) ([]byte, bool) {
	senderPubKey, exists := hub.node_ref.encryptionKeyStore.GetPublicKey(from)
	if !exists {
		hub.log.Error("Public key not found for sender node ID: %d", from)
		return nil, false
	}
	payloadBytes, err := marshalDeterministic(payload)
	if err != nil {
		hub.log.Error("payload marshal failed: err=%v", err)
		return nil, false
	}
	if !crypto.VerifySignatureEd25519(payloadBytes, signature, senderPubKey) {
		hub.log.Error("Signature verification failed for message from node ID: %d", from)
		return nil, false
	}
	return payloadBytes, true
}

// setClientStream registers s as name's reply stream; a reconnecting client
// replaces only its own entry.
func (hub *NodeMessageHub) setClientStream(name string, s *clientStreamState) {
	hub.clientStreamMu.Lock()
	if hub.clientStreams == nil {
		hub.clientStreams = make(map[string]*clientStreamState)
	}
	hub.clientStreams[name] = s
	hub.clientStreamMu.Unlock()
}

func (hub *NodeMessageHub) clearClientStream(name string, s *clientStreamState) {
	hub.clientStreamMu.Lock()
	if hub.clientStreams[name] == s {
		delete(hub.clientStreams, name)
	}
	hub.clientStreamMu.Unlock()
}

func (s *clientStreamState) send(env *transportpb.Envelope) error {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	return s.stream.Send(env)
}

// sendEnvelopeToClient sends env on the named client's stream. CommitTps must go
// only to the client that sent the request: clients match replies by request id
// alone, and every client numbers its ids from 0.
func (hub *NodeMessageHub) sendEnvelopeToClient(name string, env *transportpb.Envelope) error {
	hub.clientStreamMu.RLock()
	streamState := hub.clientStreams[name]
	hub.clientStreamMu.RUnlock()
	if streamState == nil {
		return fmt.Errorf("client stream %q is not connected", name)
	}
	return streamState.send(env)
}

// broadcastEnvelopeToClients sends env on every client stream, each on its own
// goroutine so a client whose stream is backed up (a far one) does not delay the
// others. Every client needs leader updates to count its own 2f quorum.
func (hub *NodeMessageHub) broadcastEnvelopeToClients(env *transportpb.Envelope) error {
	hub.clientStreamMu.RLock()
	names := make([]string, 0, len(hub.clientStreams))
	streams := make([]*clientStreamState, 0, len(hub.clientStreams))
	for name, s := range hub.clientStreams {
		names = append(names, name)
		streams = append(streams, s)
	}
	hub.clientStreamMu.RUnlock()
	if len(streams) == 0 {
		return errors.New("no client stream is connected")
	}

	errs := make([]error, len(streams))
	var wg sync.WaitGroup
	for i, s := range streams {
		wg.Add(1)
		go func(i int, s *clientStreamState) {
			defer wg.Done()
			if err := s.send(env); err != nil {
				errs[i] = fmt.Errorf("client %q: %w", names[i], err)
			}
		}(i, s)
	}
	wg.Wait()
	return errors.Join(errs...)
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

	clientName := clientNameFromContext(stream.Context())
	streamState := &clientStreamState{stream: stream}
	hub.setClientStream(clientName, streamState)
	defer hub.clearClientStream(clientName, streamState)
	hub.log.Info("client stream registered: client=%s", clientName)

	for {
		env, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if hub.node_ref.dead.Load() {
			// hub.log.Info("Node is dead. Ignoring message from client stream.")
			continue
		}
		switch env.MsgType {
		case core.MsgEventMessage:
			event := env.GetEvent()
			if event == nil {
				continue
			}
			data, err := transportpb.EventFromPB(event)
			if err != nil {
				hub.log.Error("stream event decode failed: err=%v", err)
				continue
			}
			if err := hub.node_ref.HandleEventMessage(stream.Context(), data); err != nil {
				hub.log.Error("stream event enqueue failed: err=%v", err)
			}

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

// clientNameFromContext returns the name a client stream announced, or the legacy
// single client's name.
func clientNameFromContext(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return config.LegacyClientName
	}
	values := md.Get(transportpb.ClientNameMetadataKey)
	if len(values) == 0 || values[0] == "" {
		return config.LegacyClientName
	}
	return values[0]
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

func (hub *NodeMessageHub) Deliver(ctx context.Context, env *transportpb.Envelope) (*transportpb.Ack, error) {
	// if hub.node_ref.dead {
	// 	hub.log.Info("Node is dead. Ignoring message from %d", env.From)
	// 	return &transportpb.Ack{Ok: true}, nil
	// }
	switch env.MsgType {
	case core.MsgEventMessage:
		if hub.node_ref.dead.Load() {
			return &transportpb.Ack{Ok: false, Error: "node is dead"}, nil
		}
		event := env.GetEvent()
		if event == nil {
			return &transportpb.Ack{Ok: false, Error: "missing event body"}, nil
		}
		data, err := transportpb.EventFromPB(event)
		if err != nil {
			return &transportpb.Ack{Ok: false, Error: err.Error()}, nil
		}
		if err := hub.node_ref.HandleEventMessage(ctx, data); err != nil {
			return &transportpb.Ack{Ok: false, Error: err.Error()}, nil
		}
		return &transportpb.Ack{Ok: true}, nil

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
		view, err := transportpb.ViewIDFromPB(preprepare.View)
		if err != nil {
			return &transportpb.Ack{Ok: false, Error: err.Error()}, nil
		}
		if !hub.verifySignature(int(env.From), env.Signature, preprepareSignPayload(view, preprepare.SeqNum, preprepare.DigestClientMsg)) {
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
		// the vote is stored under prepare.From, so it must be the key that signed it
		if int(env.From) != int(prepare.From) {
			return &transportpb.Ack{Ok: false, Error: "prepare sender mismatch"}, nil
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
		if int(env.From) != int(commit.From) {
			return &transportpb.Ack{Ok: false, Error: "commit sender mismatch"}, nil
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
		// env.From is the original sender also when the new primary forwards it
		// (ForwardViewChange), so this authenticates forwarded ViewChanges too.
		signedBytes, ok := hub.verifySignatureBytes(int(env.From), env.Signature, viewChange)
		if !ok {
			return &transportpb.Ack{Ok: false, Error: "signature verification failed"}, nil
		}
		data, err := transportpb.ViewChangeFromPB(viewChange)
		if err != nil {
			return &transportpb.Ack{Ok: false, Error: err.Error()}, nil
		}
		sizeBytes := len(signedBytes) // the signed payload; proto.Size(env) would walk it again
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
			Digest:    viewChangeDigest(signedBytes),
		}
		return &transportpb.Ack{Ok: true}, nil

	case core.MsgViewChangeFetchMessage:
		fetch := env.GetViewChangeFetch()
		if fetch == nil {
			return &transportpb.Ack{Ok: false, Error: "missing view-change-fetch body"}, nil
		}
		if int(env.From) != int(fetch.From) {
			return &transportpb.Ack{Ok: false, Error: "view-change-fetch sender mismatch"}, nil
		}
		if !hub.verifySignature(int(env.From), env.Signature, fetch) {
			return &transportpb.Ack{Ok: false, Error: "signature verification failed"}, nil
		}
		data, err := transportpb.ViewChangeFetchFromPB(fetch)
		if err != nil {
			return &transportpb.Ack{Ok: false, Error: err.Error()}, nil
		}
		hub.node_ref.viewChangeMsgChan <- ViewChangeMsg{
			MsgType:   core.MsgViewChangeFetchMessage,
			Msg:       data,
			Signature: env.Signature,
		}
		return &transportpb.Ack{Ok: true}, nil

	case core.MsgCheckpointFetchMessage:
		fetch := env.GetCheckpointFetch()
		if fetch == nil {
			return &transportpb.Ack{Ok: false, Error: "missing checkpoint-fetch body"}, nil
		}
		if int(env.From) != int(fetch.From) {
			return &transportpb.Ack{Ok: false, Error: "checkpoint-fetch sender mismatch"}, nil
		}
		if !hub.verifySignature(int(env.From), env.Signature, fetch) {
			return &transportpb.Ack{Ok: false, Error: "signature verification failed"}, nil
		}
		data, err := transportpb.CheckpointFetchFromPB(fetch)
		if err != nil {
			return &transportpb.Ack{Ok: false, Error: err.Error()}, nil
		}
		hub.node_ref.checkpointMsgChan <- CheckpointMsg{
			MsgType:   core.MsgCheckpointFetchMessage,
			Msg:       data,
			Signature: env.Signature,
		}
		return &transportpb.Ack{Ok: true}, nil

	case core.MsgCheckpointStateMessage:
		state := env.GetCheckpointState()
		if state == nil {
			return &transportpb.Ack{Ok: false, Error: "missing checkpoint-state body"}, nil
		}
		if int(env.From) != int(state.From) {
			return &transportpb.Ack{Ok: false, Error: "checkpoint-state sender mismatch"}, nil
		}
		if !hub.verifySignature(int(env.From), env.Signature, state) {
			return &transportpb.Ack{Ok: false, Error: "signature verification failed"}, nil
		}
		data, err := transportpb.CheckpointStateFromPB(state)
		if err != nil {
			return &transportpb.Ack{Ok: false, Error: err.Error()}, nil
		}
		hub.node_ref.checkpointMsgChan <- CheckpointMsg{
			MsgType:   core.MsgCheckpointStateMessage,
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
		signedBytes, ok := hub.verifySignatureBytes(int(env.From), env.Signature, newView)
		if !ok {
			return &transportpb.Ack{Ok: false, Error: "signature verification failed"}, nil
		}
		data, err := transportpb.NewViewFromPB(newView)
		if err != nil {
			return &transportpb.Ack{Ok: false, Error: err.Error()}, nil
		}
		sizeBytes := len(signedBytes) // the signed payload; proto.Size(env) would walk it again
		hub.node_ref.log.Info(
			"HUB: Received NewView message from node %d for view %d. refs=%d o_set=%d size_bytes=%d size_mib=%.3f",
			env.From,
			data.NewViewNumber,
			len(data.ViewChangeRefs),
			len(data.PreprepareLog),
			sizeBytes,
			float64(sizeBytes)/(1024*1024),
		)
		hub.node_ref.newViewMsgChan <- NewViewMsg{
			MsgType:   core.MsgNewViewMessage,
			Msg:       data,
			Signature: env.Signature,
		}
		return &transportpb.Ack{Ok: true}, nil

	case core.MsgRequestVoteMessage:
		requestVote := env.GetRequestVote()
		if requestVote == nil {
			return &transportpb.Ack{Ok: false, Error: "missing request-vote body"}, nil
		}
		if int(env.From) != int(requestVote.From) {
			return &transportpb.Ack{Ok: false, Error: "request-vote sender mismatch"}, nil
		}
		if !hub.verifySignature(int(env.From), env.Signature, requestVote) {
			return &transportpb.Ack{Ok: false, Error: "signature verification failed"}, nil
		}
		data, err := transportpb.RequestVoteFromPB(requestVote)
		if err != nil {
			return &transportpb.Ack{Ok: false, Error: err.Error()}, nil
		}
		hub.node_ref.log.Info(
			"HUB: Received RequestVote message from node %d for view %d",
			data.From,
			data.ViewNumber,
		)

		// TODO: Deliver the verified request vote to the node event loop.
		hub.node_ref.electionMsgChan <- ElectionMsg{
			MsgType:   core.MsgRequestVoteMessage,
			Msg:       data,
			Signature: env.Signature,
		}
		return &transportpb.Ack{Ok: true}, nil

	case core.MsgGrantVoteMessage:
		grantVote := env.GetGrantVote()
		if grantVote == nil {
			return &transportpb.Ack{Ok: false, Error: "missing grant-vote body"}, nil
		}
		if int(env.From) != int(grantVote.From) {
			return &transportpb.Ack{Ok: false, Error: "grant-vote sender mismatch"}, nil
		}
		if !hub.verifySignature(int(env.From), env.Signature, grantVote) {
			return &transportpb.Ack{Ok: false, Error: "signature verification failed"}, nil
		}
		data, err := transportpb.GrantVoteFromPB(grantVote)
		if err != nil {
			return &transportpb.Ack{Ok: false, Error: err.Error()}, nil
		}
		hub.node_ref.log.Info(
			"HUB: Received GrantVote message from node %d for view %d",
			data.From,
			data.ViewNumber,
		)

		// TODO: Deliver the verified grant vote to the node event loop.
		hub.node_ref.electionMsgChan <- ElectionMsg{
			MsgType:   core.MsgGrantVoteMessage,
			Msg:       data,
			Signature: env.Signature,
		}
		return &transportpb.Ack{Ok: true}, nil

	case core.MsgEpochDataMessage:
		epochData := env.GetEpochData()
		if epochData == nil {
			return &transportpb.Ack{Ok: false, Error: "missing epoch-data body"}, nil
		}
		if int(env.From) != int(epochData.From) {
			return &transportpb.Ack{Ok: false, Error: "epoch-data sender mismatch"}, nil
		}
		if !hub.verifySignature(int(env.From), env.Signature, epochData) {
			return &transportpb.Ack{Ok: false, Error: "signature verification failed"}, nil
		}
		data, err := transportpb.EpochDataMsgFromPB(epochData)
		if err != nil {
			return &transportpb.Ack{Ok: false, Error: err.Error()}, nil
		}
		hub.node_ref.epochMsgChan <- EpochProtocolMsg{
			MsgType:   core.MsgEpochDataMessage,
			Msg:       data,
			Signature: env.Signature,
		}
		return &transportpb.Ack{Ok: true}, nil

	case core.MsgEpochAggregateMessage:
		epochAggregate := env.GetEpochAggregate()
		if epochAggregate == nil {
			return &transportpb.Ack{Ok: false, Error: "missing epoch-aggregate body"}, nil
		}
		if int(env.From) != int(epochAggregate.From) {
			return &transportpb.Ack{Ok: false, Error: "epoch-aggregate sender mismatch"}, nil
		}
		if !hub.verifySignature(int(epochAggregate.From), env.Signature, epochAggregateSignPayload(epochAggregate)) {
			return &transportpb.Ack{Ok: false, Error: "signature verification failed"}, nil
		}
		data, err := transportpb.EpochAggregateMsgFromPB(epochAggregate)
		if err != nil {
			return &transportpb.Ack{Ok: false, Error: err.Error()}, nil
		}
		hub.node_ref.epochMsgChan <- EpochProtocolMsg{
			MsgType:   core.MsgEpochAggregateMessage,
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

	case core.MsgViewChangeFetchMessage:
		fetch, ok := msg.(core.ViewChangeFetchMsg)
		if !ok {
			return nil, errInvalidPayloadType(msgType, msg)
		}
		env.Body = &transportpb.Envelope_ViewChangeFetch{ViewChangeFetch: transportpb.ViewChangeFetchToPB(fetch)}
		env.From = int32(hub.node_ref.GetNodeID())

	case core.MsgCheckpointFetchMessage:
		fetch, ok := msg.(core.CheckpointFetchMsg)
		if !ok {
			return nil, errInvalidPayloadType(msgType, msg)
		}
		env.Body = &transportpb.Envelope_CheckpointFetch{CheckpointFetch: transportpb.CheckpointFetchToPB(fetch)}
		env.From = int32(hub.node_ref.GetNodeID())

	case core.MsgCheckpointStateMessage:
		state, ok := msg.(core.CheckpointStateMsg)
		if !ok {
			return nil, errInvalidPayloadType(msgType, msg)
		}
		env.Body = &transportpb.Envelope_CheckpointState{CheckpointState: transportpb.CheckpointStateToPB(state)}
		env.From = int32(hub.node_ref.GetNodeID())

	case core.MsgRequestVoteMessage:
		requestVote, ok := msg.(core.RequestVoteMsg)
		if !ok {
			return nil, errInvalidPayloadType(msgType, msg)
		}
		env.Body = &transportpb.Envelope_RequestVote{RequestVote: transportpb.RequestVoteToPB(requestVote)}
		env.From = int32(hub.node_ref.GetNodeID())

	case core.MsgGrantVoteMessage:
		grantVote, ok := msg.(core.GrantVoteMsg)
		if !ok {
			return nil, errInvalidPayloadType(msgType, msg)
		}
		env.Body = &transportpb.Envelope_GrantVote{GrantVote: transportpb.GrantVoteToPB(grantVote)}
		env.From = int32(hub.node_ref.GetNodeID())

	case core.MsgEpochDataMessage:
		epochData, ok := msg.(core.EpochDataMsg)
		if !ok {
			return nil, errInvalidPayloadType(msgType, msg)
		}
		env.Body = &transportpb.Envelope_EpochData{EpochData: transportpb.EpochDataMsgToPB(epochData)}
		env.From = int32(hub.node_ref.GetNodeID())

	case core.MsgEpochAggregateMessage:
		epochAggregate, ok := msg.(core.EpochAggregateMsg)
		if !ok {
			return nil, errInvalidPayloadType(msgType, msg)
		}
		env.Body = &transportpb.Envelope_EpochAggregate{EpochAggregate: transportpb.EpochAggregateMsgToPB(epochAggregate)}
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
	if hub.node_ref.dead.Load() {
		// hub.log.Info("Node is dead. Not sending message. msgType=%s target=%s", msgType, ip)
		return
	}
	if msgType == core.MsgReplyMessage || msgType == core.MsgCommitTpsMessage || msgType == core.MsgLeaderIdUpdateMessage || msgType == core.MsgVCRunningStatusMessage {
		env, err := hub.buildEnvelope(msgType, msg, signature)
		if err != nil {
			hub.log.Error("build envelope failed. msgType=%s err=%v", msgType, err)
			return
		}
		// hub.injectArtificialLatency(msgType, ip)
		switch m := msg.(type) {
		case core.CommitTps:
			err = hub.sendEnvelopeToClient(m.ClientMsg.ClientName, env)
		case core.ReplyMessage:
			err = hub.sendEnvelopeToClient(m.ClientMsg.ClientName, env)
		default: // LeaderIdUpdate, VCRunningStatus
			err = hub.broadcastEnvelopeToClients(env)
		}
		if err != nil {
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
	// if msgType == core.MsgNewViewMessage || msgType == core.MsgViewChangeMessage {
	// 	sizeBytes = proto.Size(env) // this is also very slow
	// }

	if err := hub.sendEnvelopeOverPeerStream(ip, env); err != nil {
		hub.log.Error("node stream send failed. msgType=%s target=%s err=%v", msgType, ip, err)
	}
	// this finishes when node puts msg on wire
	// after that it travels through network receiver receive and then process it and put in event loop where then new view processing start
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

// ForwardViewChange re-sends another node's signed ViewChange to addr, as the new
// primary answering a ViewChangeFetchMsg. The envelope carries the original sender
// and signature, so the receiver's usual ViewChange checks authenticate it.
func (hub *NodeMessageHub) ForwardViewChange(addr string, vc *core.ViewChangeMsgSig) {
	if hub.node_ref.dead.Load() {
		return
	}
	env := &transportpb.Envelope{
		MsgType:   core.MsgViewChangeMessage,
		From:      int32(vc.ViewChangeMsg.From),
		Signature: vc.Signature,
		Body:      &transportpb.Envelope_ViewChange{ViewChange: transportpb.ViewChangeToPB(vc.ViewChangeMsg)},
	}
	hub.injectArtificialLatency(core.MsgViewChangeMessage, addr)
	if err := hub.sendEnvelopeOverPeerStream(addr, env); err != nil {
		hub.log.Error("forwarding view change from node %d to %s failed: %v", vc.ViewChangeMsg.From, addr, err)
	}
}
