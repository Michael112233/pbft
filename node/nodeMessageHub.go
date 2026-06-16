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
	rpcTimeout      = 300 * time.Second
	maxGRPCMsgBytes = 1000 * 1024 * 1024

	outboundCriticalQueueSize = 4096
	outboundHighQueueSize     = 8192
	outboundMediumQueueSize   = 4096
	outboundLowQueueSize      = 128

	dispatchCriticalQueueSize = 4096
	dispatchHighQueueSize     = 8192
	dispatchMediumQueueSize   = 4096
	dispatchLowQueueSize      = 1024

	dispatchWorkerCount = 4
)

type clientStreamState struct {
	stream transportpb.PBFTTransport_ClientNodeChannelServer
	sendMu sync.Mutex
}

type hubPriority int

const (
	priorityCritical hubPriority = iota
	priorityHigh
	priorityMedium
	priorityLow
	priorityCount
)

type outboundLane int

const (
	outboundLaneControl outboundLane = iota
	outboundLaneData
)

var weightedPriorityOrder = []hubPriority{
	priorityHigh, priorityHigh, priorityHigh, priorityHigh,
	priorityHigh, priorityHigh, priorityHigh, priorityHigh,
	priorityMedium, priorityMedium,
	priorityLow,
}

type outboundJob struct {
	msgType string
	target  string
	env     *transportpb.Envelope
	lane    outboundLane
}

type dispatchJob struct {
	msgType   string
	env       *transportpb.Envelope
	signature []byte
	run       func()
}

type priorityJobQueue struct {
	mu          sync.Mutex
	cond        *sync.Cond
	queues      [priorityCount][]interface{}
	caps        [priorityCount]int
	rrIndex     int
	closed      bool
	lastFullLog [priorityCount]time.Time
}

type outboundPeerState struct {
	target       string
	controlQueue *priorityJobQueue
	dataQueue    *priorityJobQueue
}

type NodeMessageHub struct {
	transportpb.UnimplementedPBFTTransportServer

	node_ref *Node
	log      *logger.Logger

	mu            sync.RWMutex
	clients       map[string]transportpb.PBFTTransportClient
	conns         map[string]*grpc.ClientConn
	grpcSrv       *grpc.Server
	listener      net.Listener
	closeOnce     sync.Once
	workerWg      sync.WaitGroup
	outboundMu    sync.Mutex
	outboundPeers map[string]*outboundPeerState

	dispatchQueue     *priorityJobQueue
	dispatchStartOnce sync.Once

	clientStreamMu sync.RWMutex
	clientStream   *clientStreamState
}

func NewNodeMessageHub() *NodeMessageHub {
	return &NodeMessageHub{
		clients:       make(map[string]transportpb.PBFTTransportClient),
		conns:         make(map[string]*grpc.ClientConn),
		outboundPeers: make(map[string]*outboundPeerState),
		dispatchQueue: newPriorityJobQueue([priorityCount]int{
			priorityCritical: dispatchCriticalQueueSize,
			priorityHigh:     dispatchHighQueueSize,
			priorityMedium:   dispatchMediumQueueSize,
			priorityLow:      dispatchLowQueueSize,
		}),
	}
}

func newPriorityJobQueue(caps [priorityCount]int) *priorityJobQueue {
	q := &priorityJobQueue{caps: caps}
	q.cond = sync.NewCond(&q.mu)
	return q
}

func priorityName(priority hubPriority) string {
	switch priority {
	case priorityCritical:
		return "critical"
	case priorityHigh:
		return "high"
	case priorityMedium:
		return "medium"
	case priorityLow:
		return "low"
	default:
		return "unknown"
	}
}

func messagePriority(msgType string) hubPriority {
	switch msgType {
	case core.MsgNewViewMessage, core.MsgViewChangeMessage:
		return priorityCritical
	case core.MsgPrepareMessage, core.MsgCommitMessage:
		return priorityHigh
	case core.MsgCheckpointMessage, core.MsgRequestStateTransfer, core.MsgStateTransfer:
		return priorityMedium
	default:
		return priorityLow
	}
}

func outboundLaneForPriority(priority hubPriority) outboundLane {
	if priority == priorityLow {
		return outboundLaneData
	}
	return outboundLaneControl
}

func outboundClientKey(addr string, lane outboundLane) string {
	return fmt.Sprintf("%d:%s", lane, addr)
}

func (q *priorityJobQueue) enqueue(job interface{}, priority hubPriority, log *logger.Logger, queueName, msgType string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	for !q.closed && len(q.queues[priority]) >= q.caps[priority] {
		now := time.Now()
		if log != nil && now.Sub(q.lastFullLog[priority]) >= time.Second {
			log.Info("%s %s queue full for msgType=%s len=%d cap=%d; applying backpressure",
				queueName, priorityName(priority), msgType, len(q.queues[priority]), q.caps[priority])
			q.lastFullLog[priority] = now
		}
		q.cond.Wait()
	}
	if q.closed {
		return false
	}

	q.queues[priority] = append(q.queues[priority], job)
	q.cond.Signal()
	return true
}

func (q *priorityJobQueue) dequeue() (interface{}, hubPriority, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	for {
		if q.closed {
			return nil, priorityLow, false
		}
		if job, ok := q.popLocked(priorityCritical); ok {
			return job, priorityCritical, true
		}
		for i := 0; i < len(weightedPriorityOrder); i++ {
			priority := weightedPriorityOrder[q.rrIndex]
			q.rrIndex = (q.rrIndex + 1) % len(weightedPriorityOrder)
			if job, ok := q.popLocked(priority); ok {
				return job, priority, true
			}
		}
		q.cond.Wait()
	}
}

func (q *priorityJobQueue) popLocked(priority hubPriority) (interface{}, bool) {
	if len(q.queues[priority]) == 0 {
		return nil, false
	}
	job := q.queues[priority][0]
	q.queues[priority][0] = nil
	q.queues[priority] = q.queues[priority][1:]
	q.cond.Signal()
	return job, true
}

func (q *priorityJobQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.cond.Broadcast()
	q.mu.Unlock()
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
	hub.startDispatchWorkers()

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

		if hub.dispatchQueue != nil {
			hub.dispatchQueue.close()
		}
		hub.outboundMu.Lock()
		for _, peer := range hub.outboundPeers {
			peer.controlQueue.close()
			peer.dataQueue.close()
		}
		hub.outboundPeers = make(map[string]*outboundPeerState)
		hub.outboundMu.Unlock()

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
		hub.workerWg.Wait()
		hub.log.Debug("messageHub is close.")
	})
}

func (hub *NodeMessageHub) startDispatchWorkers() {
	if hub.dispatchQueue == nil {
		hub.dispatchQueue = newPriorityJobQueue([priorityCount]int{
			priorityCritical: dispatchCriticalQueueSize,
			priorityHigh:     dispatchHighQueueSize,
			priorityMedium:   dispatchMediumQueueSize,
			priorityLow:      dispatchLowQueueSize,
		})
	}
	hub.dispatchStartOnce.Do(func() {
		for i := 0; i < dispatchWorkerCount; i++ {
			hub.workerWg.Add(1)
			go hub.dispatchWorker()
		}
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

func (hub *NodeMessageHub) enqueueDispatch(job dispatchJob, priority hubPriority) bool {
	if hub.dispatchQueue == nil {
		hub.dispatchQueue = newPriorityJobQueue([priorityCount]int{
			priorityCritical: dispatchCriticalQueueSize,
			priorityHigh:     dispatchHighQueueSize,
			priorityMedium:   dispatchMediumQueueSize,
			priorityLow:      dispatchLowQueueSize,
		})
	}
	return hub.dispatchQueue.enqueue(job, priority, hub.log, "dispatch", job.msgType)
}

func (hub *NodeMessageHub) dispatchWorker() {
	defer hub.workerWg.Done()
	for {
		rawJob, _, ok := hub.dispatchQueue.dequeue()
		if !ok {
			return
		}
		job, ok := rawJob.(dispatchJob)
		if !ok || job.run == nil {
			continue
		}
		job.run()
	}
}

func (hub *NodeMessageHub) getOrCreateOutboundPeer(target string) *outboundPeerState {
	hub.outboundMu.Lock()
	defer hub.outboundMu.Unlock()

	if hub.outboundPeers == nil {
		hub.outboundPeers = make(map[string]*outboundPeerState)
	}
	if peer, ok := hub.outboundPeers[target]; ok {
		return peer
	}

	peer := &outboundPeerState{
		target: target,
		controlQueue: newPriorityJobQueue([priorityCount]int{
			priorityCritical: outboundCriticalQueueSize,
			priorityHigh:     outboundHighQueueSize,
			priorityMedium:   outboundMediumQueueSize,
			priorityLow:      1,
		}),
		dataQueue: newPriorityJobQueue([priorityCount]int{
			priorityCritical: 1,
			priorityHigh:     1,
			priorityMedium:   1,
			priorityLow:      outboundLowQueueSize,
		}),
	}
	hub.outboundPeers[target] = peer

	hub.workerWg.Add(2)
	go hub.outboundWorker(peer.controlQueue, outboundLaneControl)
	go hub.outboundWorker(peer.dataQueue, outboundLaneData)

	return peer
}

func (hub *NodeMessageHub) enqueueOutbound(job outboundJob, priority hubPriority) bool {
	peer := hub.getOrCreateOutboundPeer(job.target)
	if job.lane == outboundLaneData {
		return peer.dataQueue.enqueue(job, priority, hub.log, "outbound data", job.msgType)
	}
	return peer.controlQueue.enqueue(job, priority, hub.log, "outbound control", job.msgType)
}

func (hub *NodeMessageHub) outboundWorker(queue *priorityJobQueue, lane outboundLane) {
	defer hub.workerWg.Done()
	for {
		rawJob, _, ok := queue.dequeue()
		if !ok {
			return
		}
		job, ok := rawJob.(outboundJob)
		if !ok {
			continue
		}
		hub.deliverOutbound(job, lane)
	}
}

func (hub *NodeMessageHub) deliverOutbound(job outboundJob, lane outboundLane) {
	hub.injectArtificialLatency(job.msgType, job.target)

	client, err := hub.getOrCreateClient(job.target, lane)
	if err != nil {
		hub.log.Error("dial target failed. msgType=%s target=%s err=%v", job.msgType, job.target, err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()

	ack, err := client.Deliver(ctx, job.env)
	if err != nil {
		hub.log.Error("deliver rpc failed. msgType=%s target=%s err=%v", job.msgType, job.target, err)
		hub.dropClient(job.target, lane)
		return
	}
	if ack != nil && !ack.Ok {
		hub.log.Error("deliver rejected. msgType=%s target=%s err=%s", job.msgType, job.target, ack.Error)
	}
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
			hub.node_ref.recordClientRequestReceived(len(data.Txs))
			dataCopy := data
			if ok := hub.enqueueDispatch(dispatchJob{
				msgType: core.MsgRequestMessage,
				env:     env,
				run: func() {
					hub.node_ref.HandleRequestMessage(dataCopy)
				},
			}, messagePriority(core.MsgRequestMessage)); !ok {
				return errors.New("dispatch queue closed")
			}

		case core.MsgCloseMessage:
			return nil

		default:
			hub.log.Error("Unknown stream message type received: msgType=%s", env.MsgType)
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
		data, err := transportpb.RequestFromPB(request)
		if err != nil {
			return &transportpb.Ack{Ok: false, Error: err.Error()}, nil
		}
		hub.node_ref.recordClientRequestReceived(len(data.Txs))
		if ok := hub.enqueueDispatch(dispatchJob{
			msgType: core.MsgRequestMessage,
			env:     env,
			run: func() {
				hub.node_ref.HandleRequestMessage(data)
			},
		}, messagePriority(env.MsgType)); !ok {
			return &transportpb.Ack{Ok: false, Error: "dispatch queue closed"}, nil
		}
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
		signature := append([]byte(nil), env.Signature...)
		if ok := hub.enqueueDispatch(dispatchJob{
			msgType:   core.MsgPreprepareMessage,
			env:       env,
			signature: signature,
			run: func() {
				hub.node_ref.HandlePrePrepare(data, signature)
			},
		}, messagePriority(env.MsgType)); !ok {
			return &transportpb.Ack{Ok: false, Error: "dispatch queue closed"}, nil
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
		signature := append([]byte(nil), env.Signature...)
		if ok := hub.enqueueDispatch(dispatchJob{
			msgType:   core.MsgPrepareMessage,
			env:       env,
			signature: signature,
			run: func() {
				hub.node_ref.HandlePrepare(data, signature)
			},
		}, messagePriority(env.MsgType)); !ok {
			return &transportpb.Ack{Ok: false, Error: "dispatch queue closed"}, nil
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
		if ok := hub.enqueueDispatch(dispatchJob{
			msgType: core.MsgCommitMessage,
			env:     env,
			run: func() {
				hub.node_ref.HandleCommit(data)
			},
		}, messagePriority(env.MsgType)); !ok {
			return &transportpb.Ack{Ok: false, Error: "dispatch queue closed"}, nil
		}
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
		signature := append([]byte(nil), env.Signature...)
		if ok := hub.enqueueDispatch(dispatchJob{
			msgType:   core.MsgCheckpointMessage,
			env:       env,
			signature: signature,
			run: func() {
				hub.node_ref.HandleCheckpoint(data, signature)
			},
		}, messagePriority(env.MsgType)); !ok {
			return &transportpb.Ack{Ok: false, Error: "dispatch queue closed"}, nil
		}
		return &transportpb.Ack{Ok: true}, nil

	case core.MsgRequestStateTransfer:
		request := env.GetRequestStateTransfer()
		if request == nil {
			return &transportpb.Ack{Ok: false, Error: "missing request state transfer body"}, nil
		}
		if int(request.From) != int(env.From) {
			return &transportpb.Ack{Ok: false, Error: "request state transfer sender mismatch"}, nil
		}
		if !hub.verifySignature(int(env.From), env.Signature, request) {
			return &transportpb.Ack{Ok: false, Error: "signature verification failed"}, nil
		}
		data, err := transportpb.RequestStateTransferFromPB(request)
		if err != nil {
			return &transportpb.Ack{Ok: false, Error: err.Error()}, nil
		}
		signature := append([]byte(nil), env.Signature...)
		if ok := hub.enqueueDispatch(dispatchJob{
			msgType:   core.MsgRequestStateTransfer,
			env:       env,
			signature: signature,
			run: func() {
				hub.node_ref.HandleRequestStateTransfer(data, signature)
			},
		}, messagePriority(env.MsgType)); !ok {
			return &transportpb.Ack{Ok: false, Error: "dispatch queue closed"}, nil
		}
		return &transportpb.Ack{Ok: true}, nil

	case core.MsgStateTransfer:
		stateTransfer := env.GetStateTransfer()
		if stateTransfer == nil {
			return &transportpb.Ack{Ok: false, Error: "missing state transfer body"}, nil
		}
		if int(stateTransfer.From) != int(env.From) {
			return &transportpb.Ack{Ok: false, Error: "state transfer sender mismatch"}, nil
		}
		if !hub.verifySignature(int(env.From), env.Signature, stateTransfer) {
			return &transportpb.Ack{Ok: false, Error: "signature verification failed"}, nil
		}
		data, err := transportpb.StateTransferFromPB(stateTransfer)
		if err != nil {
			return &transportpb.Ack{Ok: false, Error: err.Error()}, nil
		}
		signature := append([]byte(nil), env.Signature...)
		if ok := hub.enqueueDispatch(dispatchJob{
			msgType:   core.MsgStateTransfer,
			env:       env,
			signature: signature,
			run: func() {
				hub.node_ref.HandleStateTransfer(data, signature)
			},
		}, messagePriority(env.MsgType)); !ok {
			return &transportpb.Ack{Ok: false, Error: "dispatch queue closed"}, nil
		}
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
		signature := append([]byte(nil), env.Signature...)
		if ok := hub.enqueueDispatch(dispatchJob{
			msgType:   core.MsgViewChangeMessage,
			env:       env,
			signature: signature,
			run: func() {
				hub.node_ref.HandleViewChange(data, signature)
			},
		}, messagePriority(env.MsgType)); !ok {
			return &transportpb.Ack{Ok: false, Error: "dispatch queue closed"}, nil
		}
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
		signature := append([]byte(nil), env.Signature...)
		if ok := hub.enqueueDispatch(dispatchJob{
			msgType:   core.MsgNewViewMessage,
			env:       env,
			signature: signature,
			run: func() {
				hub.node_ref.HandleNewView(data, signature)
			},
		}, messagePriority(env.MsgType)); !ok {
			return &transportpb.Ack{Ok: false, Error: "dispatch queue closed"}, nil
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

func (hub *NodeMessageHub) getOrCreateClient(addr string, lane outboundLane) (transportpb.PBFTTransportClient, error) {
	key := outboundClientKey(addr, lane)
	hub.mu.RLock()
	client, ok := hub.clients[key]
	hub.mu.RUnlock()
	if ok {
		return client, nil
	}

	dialer := &net.Dialer{}
	localHost, _, err := net.SplitHostPort(hub.node_ref.GetAddr())
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
				hub.log.Info("node dialed %s from %s", target, conn.LocalAddr())
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

	createdClient := transportpb.NewPBFTTransportClient(conn)

	hub.mu.Lock()
	if existingClient, exists := hub.clients[key]; exists {
		hub.mu.Unlock()
		_ = conn.Close()
		return existingClient, nil
	}
	hub.clients[key] = createdClient
	hub.conns[key] = conn
	hub.mu.Unlock()

	return createdClient, nil
}

func (hub *NodeMessageHub) dropClient(addr string, lane outboundLane) {
	key := outboundClientKey(addr, lane)
	hub.mu.Lock()
	conn, ok := hub.conns[key]
	if ok {
		delete(hub.conns, key)
		delete(hub.clients, key)
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

	case core.MsgRequestStateTransfer:
		request, ok := msg.(core.RequestStateTransferMsg)
		if !ok {
			return nil, errInvalidPayloadType(msgType, msg)
		}
		env.Body = &transportpb.Envelope_RequestStateTransfer{RequestStateTransfer: transportpb.RequestStateTransferToPB(request)}
		env.From = int32(hub.node_ref.GetNodeID())

	case core.MsgStateTransfer:
		stateTransfer, ok := msg.(core.StateTransferMsg)
		if !ok {
			return nil, errInvalidPayloadType(msgType, msg)
		}
		env.Body = &transportpb.Envelope_StateTransfer{StateTransfer: transportpb.StateTransferToPB(stateTransfer)}
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
		hub.injectArtificialLatency(msgType, ip)
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

	priority := messagePriority(msgType)
	lane := outboundLaneForPriority(priority)
	if ok := hub.enqueueOutbound(outboundJob{
		msgType: msgType,
		target:  ip,
		env:     env,
		lane:    lane,
	}, priority); !ok {
		hub.log.Error("outbound queue closed. msgType=%s target=%s", msgType, ip)
	}

}
