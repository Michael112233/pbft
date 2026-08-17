package node

import (
	"crypto/sha256"
	"fmt"
	"strconv"

	"sync"
	"sync/atomic"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/crypto"
	"github.com/michael112233/pbft/execution"
	"github.com/michael112233/pbft/logger"
	"github.com/michael112233/pbft/transportpb"
	"github.com/michael112233/pbft/utils"
	"google.golang.org/protobuf/proto"
)

const (
	defaultPendingQueueCapacity        = 10_000
	defaultPBFTRequestTimeout          = 5 * time.Second
	defaultPBFTRequestTimeoutJitterMax = 500 * time.Millisecond
	CHECKPOINT_INTERVAL                = 250
	defaultTargetThroughput            = 0.90 * 1400
	targetThroughputMaxFactor          = 0.90
	ALPHA                              = 1 / float64(10) // for exponential moving average calculation of throughput
	D                                  = 3
	THROUGHPUTINTERVAL_DELAY           = 100
)

type clientRequestKey struct {
	clientName string
	id         int64
}

type Node struct {
	NodeID int

	cfg           *config.Config
	log           *logger.Logger
	messageHub    *NodeMessageHub
	learningAgent *LearningAgentHub

	eventLoopStopCh                chan struct{}
	eventLoopDoneCh                chan struct{}
	receiveVerifiedClientRequestCh chan core.ClientMsgSignature
	consensusMsgChan               chan ConsensusMsg
	eventLoopStarted               atomic.Bool
	eventLoopStopOnce              sync.Once
	pendingRequests                RequestQueue
	batchLogic                     Batcher
	pool                           *Pool
	consensusLog                   *Log

	////

	encryptionKeyStore *KeyStore

	view              int64
	leaderId          int
	leaderIdForView   map[int64]int
	forView           int64
	votedFor          int
	viewChangeRunning bool
	sequenceNumber    int64
	lastExecuted      int64

	fNodes int

	executionMachine execution.StateMachine

	clientReceivedTxs             atomic.Int64
	clientReceiveRateStop         chan struct{}
	clientReceiveRateDone         chan struct{}
	clientReceiveRateStarted      atomic.Bool
	clientReceiveRateStopOnce     sync.Once
	leaderPrepreparesProcessed    atomic.Int64
	leaderPreprepareRateStop      chan struct{}
	leaderPreprepareRateDone      chan struct{}
	leaderPreprepareRateStarted   atomic.Bool
	leaderPreprepareRateStopOnce  sync.Once
	memoryLoggerStop              chan struct{}
	memoryLoggerDone              chan struct{}
	memoryLoggerStarted           atomic.Bool
	memoryLoggerStopOnce          sync.Once
	shareLoggerStop               chan struct{}
	shareLoggerDone               chan struct{}
	shareLoggerStarted            atomic.Bool
	shareLoggerStopOnce           sync.Once
	throughputMeasurementsChan    chan throughputMeasurement
	throughputMeasurementsStop    chan struct{}
	throughputMeasurementsDone    chan struct{}
	throughputMeasurementsStarted atomic.Bool
	throughputMeasurementsOnce    sync.Once

	throughputPerf ThroughputPerf
	lm             *LatencyMonitor

	dead               bool
	split              bool
	periodic           bool
	fixed              bool
	changemu           sync.RWMutex
	periodicReq        bool
	performanceTrigger bool
	peakTpsTest        bool
	proposalDelay      bool
	gc                 bool
	latencyLog         bool
}

func NewNode(nodeID int, cfg *config.Config) (*Node, error) {

	log := logger.NewLogger(nodeID, "node")
	n := &Node{
		NodeID: nodeID,

		cfg:        cfg,
		log:        log,
		messageHub: NewNodeMessageHub(),

		encryptionKeyStore: NewKeyStore(nodeID, cfg.NodeNum),
		pool:               NewPool(log),
		consensusLog:       NewLog(),
		executionMachine:   execution.NewAccountStateMachine(),

		eventLoopStopCh:                make(chan struct{}),
		eventLoopDoneCh:                make(chan struct{}),
		receiveVerifiedClientRequestCh: make(chan core.ClientMsgSignature),
		consensusMsgChan:               make(chan ConsensusMsg, cfg.ConsensusChanSize),
		pendingRequests:                NewRequestQueue(cfg.PendingQueueCapacity),
		clientReceiveRateStop:          make(chan struct{}),
		clientReceiveRateDone:          make(chan struct{}),
		leaderPreprepareRateStop:       make(chan struct{}),
		leaderPreprepareRateDone:       make(chan struct{}),
		memoryLoggerStop:               make(chan struct{}),
		memoryLoggerDone:               make(chan struct{}),
		shareLoggerStop:                make(chan struct{}),
		shareLoggerDone:                make(chan struct{}),
		throughputMeasurementsChan:     make(chan throughputMeasurement, throughputMeasurementBufferSize),
		throughputMeasurementsStop:     make(chan struct{}),
		throughputMeasurementsDone:     make(chan struct{}),

		batchLogic: Batcher{
			maxBatchSize:     cfg.MaxBatchSize,
			maxBatchWaitTime: time.Duration(cfg.MaxBatchDelay) * time.Millisecond,
			batch:            make([]core.ClientMsgSignature, 0, cfg.MaxBatchSize),
		},

		view:            1,
		forView:         1,
		leaderId:        1,
		leaderIdForView: map[int64]int{1: 1},

		viewChangeRunning: false,

		fNodes: (int(cfg.NodeNum) - 1) / 3,

		throughputPerf: ThroughputPerf{
			targetThroughput:             defaultTargetThroughput,
			throughputIntervalStartSeq:   THROUGHPUTINTERVAL_DELAY,
			throughputIntervalStart:      time.Time{},
			throughputObservationStarted: false,
		},
		lm: NewLatencyMonitor(),

		split:              false,
		dead:               cfg.NodesDead[nodeID],
		proposalDelay:      cfg.ProposalDelayNode == nodeID,
		periodic:           cfg.Periodic,
		periodicReq:        cfg.PeriodicReq,
		fixed:              cfg.Fixed,
		performanceTrigger: cfg.PerformanceTrigger,
		peakTpsTest:        cfg.PeakTpsTest,

		latencyLog: cfg.LatencyLog,
		gc:         cfg.GC,
	}

	n.ArmBatchTimer()
	n.StopBatchTimer()

	if address := config.LearningAgentAddr[nodeID]; address != "" {
		learningAgent, err := NewLearningAgent(n, address)
		if err != nil {
			log.Error("failed to create learning-agent client: %v", err)
			return nil, fmt.Errorf("failed to create learning-agent client: %w", err)
		} else {
			n.learningAgent = learningAgent
		}
	}
	return n, nil
}

func pendingQueueCapacity(cfg *config.Config) int {
	if cfg.PendingQueueCapacity > 0 {
		return cfg.PendingQueueCapacity
	}
	return defaultPendingQueueCapacity
}

func (n *Node) Start() error {

	if n.learningAgent != nil {
		err := n.learningAgent.Start()
		if err != nil {
			n.log.Error("failed to start learning-agent client: %v", err)
			return err
		}
		// ctx, cancel := context.WithTimeout(context.Background(), learningAgentStartupTimeout)
		// err = n.learningAgentHandshake(ctx, learningAgentRPCTimeout, learningAgentRetryInterval)
		// cancel()
		// if err != nil {
		// 	return err
		// }
		n.log.Info("learning-agent startup handshake succeeded")
	}
	n.throughputMeasurementStart()
	n.startEventLoop()
	n.messageHub.Start(n, &sync.WaitGroup{})

	if n.cfg.Logging {
		if n.memoryLoggerStarted.CompareAndSwap(false, true) {
			component := "node_" + strconv.Itoa(n.NodeID)
			go utils.StartMemoryLogger("logs/"+component+"_mem.log", component, 30*time.Second, n.memoryLoggerStop, n.memoryLoggerDone)
		}
		// if n.clientReceiveRateStarted.CompareAndSwap(false, true) {
		// 	go n.clientReceiveRateLogger()
		// }
		// if n.leaderPreprepareRateStarted.CompareAndSwap(false, true) {
		// 	go n.leaderPreprepareRateLogger()
		// }
	}
	// if n.cfg.LogShares {
	// 	if n.shareLoggerStarted.CompareAndSwap(false, true) {
	// 		go n.shareLogger()
	// 	}

	// }
	// if n.commitSerializedRoutineStarted.CompareAndSwap(false, true) {
	// 	go n.commitSerializedRoutine()
	// }
	// if n.checkpointSerializedRoutineStarted.CompareAndSwap(false, true) {
	// 	go n.checkpointSerializedRoutine()
	// }
	// if n.cfg.Netem.Enabled && n.netemEventStarted.CompareAndSwap(false, true) {
	// 	go n.netemEventWorker()
	// }
	// go n.ClientSignatureVerifier()
	// go n.VerifiedClientMessageHandler()
	// if n.pbftTimerManager.pbftTimerStarted.CompareAndSwap(false, true) {
	// 	go n.pbftTimerManager.pbftTimerWorker(n)
	// }
	// if n.periodicTimerManager != nil {
	// 	n.periodicTimerManager.Start()
	// }
	n.log.Info("node started")
	return nil
}

func (n *Node) Stop() {
	if n.learningAgent != nil {
		if err := n.learningAgent.Close(); err != nil {
			n.log.Error("failed to close learning-agent connection: %v", err)
		}
	}
	// Stop all expire timers to prevent resource leaks
	// n.StopAllExpireTimers()
	// Close network resources to stop listeners and connections
	if n.messageHub != nil && n.messageHub.node_ref != nil {
		n.messageHub.Close()
	}
	n.stopEventLoop()
	n.throughputMeasurementStop()

	n.clientReceiveRateStopOnce.Do(func() {
		close(n.clientReceiveRateStop)
	})
	if n.clientReceiveRateStarted.Load() {
		<-n.clientReceiveRateDone
	}
	n.leaderPreprepareRateStopOnce.Do(func() {
		close(n.leaderPreprepareRateStop)
	})
	if n.leaderPreprepareRateStarted.Load() {
		<-n.leaderPreprepareRateDone
	}
	n.memoryLoggerStopOnce.Do(func() {
		close(n.memoryLoggerStop)
	})
	if n.memoryLoggerStarted.Load() {
		<-n.memoryLoggerDone
	}
	n.shareLoggerStopOnce.Do(func() {
		close(n.shareLoggerStop)
	})
	if n.shareLoggerStarted.Load() {
		<-n.shareLoggerDone
	}

	n.log.Info("node stopped")
}

func (n *Node) GetAddr() string {
	return config.NodeAddr[int(n.NodeID)]
}

func (n *Node) GetNodeID() int {
	return n.NodeID
}

func (n *Node) Dead() {
	n.dead = true
}
func (n *Node) Split() {
	n.split = true
}

func (n *Node) tryPropose(fullBatch bool) {
	if n.GetNodeID() != n.GetLeaderId() {
		return
	}
	if n.pendingRequests.Len() < n.GetBatchSize() {
		n.log.Debug("Not enough pending requests to propose a batch: %d < %d", n.pendingRequests.Len(), n.GetBatchSize())
		return
	}
	inflight := n.CurrentSequenceNumber() - n.GetLastExecuted()
	if inflight >= n.AllowedMaxInFlight() {
		n.log.Debug("Cannot propose: inflight %d >= allowed max inflight %d", inflight, n.AllowedMaxInFlight())
		return
	}
	reqs := n.pendingRequests.Dequeue(n.GetBatchSize())
	digestBatch, requestDigests, err := ComputeBatchDigest(reqs)
	if err != nil {
		n.log.Error("Failed to compute batch digest: %v", err)
		return
	}
	view := n.GetView()
	seqNum := n.GetNextSeqNum()
	preprepareMsg := core.PreprepareMsg{
		View:                       view,
		SeqNum:                     seqNum,
		DigestClientMsg:            digestBatch,
		ClientMsg:                  reqs,
		DigestIndividualClientMsgs: requestDigests,
	}
	preprepareMsgMini := core.PreprepareMsgMini{
		View:                       view,
		SeqNum:                     seqNum,
		DigestClientMsg:            digestBatch,
		DigestIndividualClientMsgs: requestDigests,
	}

	payloadBytes, err := marshalDeterministic(preprepareSignPayload(view, seqNum, digestBatch[:]))
	if err != nil {
		n.log.Error("Failed to marshal PrePrepare message for signing: %v", err)

		return
	}

	n.pool.AddBatch(reqs, requestDigests, seqNum, view)

	signature := crypto.SignMessageEd25519(payloadBytes, n.encryptionKeyStore.GetPrivateKey())
	slot, _ := n.consensusLog.GetorCreateEntry(seqNum, view)
	n.slotPreprepare(slot, &preprepareMsgMini, signature, true)
	slot.view = view
	n.RecordStartTime(seqNum, digestBatch, time.Now())

	n.asyncBroadCast(core.MsgPreprepareMessage, preprepareMsg, signature)
}

func (n *Node) HandlePrePrepare(preprepareMsg core.PreprepareMsg, signature []byte) {
	// above it have check view chnage running ignore
	view := n.GetView()
	if preprepareMsg.View > view {
		// buffer
	}
	if preprepareMsg.View != view {
		return
	}

	// can skip or optimise it later
	for _, clientmsg := range preprepareMsg.ClientMsg {
		clientMsgBytes, err := marshalDeterministic(transportpb.ClientMsgToPB(clientmsg.Data))
		if err != nil {
			n.log.Error("Failed to marshal client message for verification: %v", err)
			return
		}
		verified := crypto.VerifySignatureEd25519(clientMsgBytes, clientmsg.Signature, n.encryptionKeyStore.clientKey)
		if !verified {
			n.log.Error("Failed to verify client message signature")
			return
		}
	}
	// can use optimization and dont use same func as indv digest already there in preprepare
	digestBatch, _, err := ComputeBatchDigest(preprepareMsg.ClientMsg)
	if err != nil {
		n.log.Error("Failed to compute batch digest: %v", err)
		return
	}

	if digestBatch != preprepareMsg.DigestClientMsg {
		n.log.Error("Batch digest mismatch")
		return
	}
	slot, exists := n.consensusLog.GetorCreateEntry(preprepareMsg.SeqNum, preprepareMsg.View)
	if exists {
		if slot.view != preprepareMsg.View {
			if slot.view < preprepareMsg.View {
				n.log.Error("Received PrePrepare message for a lower view than existing log entry")
				return
			} else {
				n.log.Error("Received PrePrepare message for a higher view than existing log entry")
				return
			}
		} else {
			// same view might have received prepare or commit ahead
			// if same seq same view alrady set equivocation
			if slot.preprepare != nil {
				n.log.Error("Received duplicate PrePrepare message for the same view and sequence number")
				return
			}
		}
	}
	prepareMsgMini := core.PreprepareMsgMini{
		View:                       preprepareMsg.View,
		SeqNum:                     preprepareMsg.SeqNum,
		DigestClientMsg:            preprepareMsg.DigestClientMsg,
		DigestIndividualClientMsgs: preprepareMsg.DigestIndividualClientMsgs,
	}
	n.slotPreprepare(slot, &prepareMsgMini, signature, false)
	slot.view = preprepareMsg.View
	n.pool.AddBatch(preprepareMsg.ClientMsg, preprepareMsg.DigestIndividualClientMsgs, preprepareMsg.SeqNum, preprepareMsg.View)
	msg := core.PrepareMsg{
		View:   preprepareMsg.View,
		SeqNum: preprepareMsg.SeqNum,
		Digest: preprepareMsg.DigestClientMsg,
		From:   n.GetNodeID(),
	}
	pbmsg := transportpb.PrepareToPB(msg)
	payloadBytes, err := marshalDeterministic(pbmsg)
	if err != nil {
		n.log.Error("Failed to marshal Prepare message for signing: %v", err)
		return
	}
	signaturePrepare := crypto.SignMessageEd25519(payloadBytes, n.encryptionKeyStore.GetPrivateKey())
	msgForLog := core.PrepareMsgSig{
		PrepareMsg: msg,
		Signature:  signaturePrepare,
	}
	// prepare sent already marked true above
	slot.prepares[n.GetNodeID()] = msgForLog

	n.RecordStartTime(preprepareMsg.SeqNum, preprepareMsg.DigestClientMsg, time.Now())

	n.asyncBroadCast(core.MsgPrepareMessage, msg, signaturePrepare)
	n.tryAdvancePrepare(slot)

}

func (n *Node) HandlePrepare(prepareMsg core.PrepareMsg, signature []byte) {
	view := n.GetView()
	if prepareMsg.View > view {
		//buffer
	}

	if prepareMsg.View != view {
		return
	}

	slot, exists := n.consensusLog.GetorCreateEntry(prepareMsg.SeqNum, prepareMsg.View)
	if exists {
		if slot.view != prepareMsg.View {
			if slot.view < prepareMsg.View {
				n.log.Error("Received Prepare message for a lower view than existing log entry")
				return
			} else {
				n.log.Error("Received Prepare message for a higher view than existing log entry")
				return
			}

		} else {
			// may have diff preprepare or may already have prepare from same node
			if slot.preprepare != nil && slot.preprepare.DigestClientMsg != prepareMsg.Digest {
				// same cond as olde code leader preprepare not match with prepare
				n.log.Error("equivcation at prepare")
				return
			}
		}
	}
	msgForLog := core.PrepareMsgSig{
		PrepareMsg: prepareMsg,
		Signature:  signature,
	}
	// we may already have it from same node
	slot.prepares[prepareMsg.From] = msgForLog
	slot.view = prepareMsg.View // if already exist then view is already set but if not then set it
	n.tryAdvancePrepare(slot)

}

func (n *Node) tryAdvancePrepare(slot *LogEntry) {
	if slot.commitSent {
		return
	}
	if slot.preprepare == nil {
		return
	}
	if len(slot.prepares) < n.QuorumSize()-1 || matchingVotes(slot.prepares, slot.preprepare.DigestClientMsg) < n.QuorumSize()-1 {

		return
	}
	slot.commitSent = true
	commitDigest := slot.preprepare.DigestClientMsg
	slot.commits[n.GetNodeID()] = commitDigest
	go n.asyncBroadcastCommit(slot.view, slot.preprepare.SeqNum, commitDigest)
	n.tryExecute(slot)

}

func (n *Node) HandleCommit(commitMsg core.CommitMsg) {
	view := n.GetView()
	if commitMsg.View > view {
		//buffer
	}

	if commitMsg.View != view {
		return
	}

	slot, exists := n.consensusLog.GetorCreateEntry(commitMsg.SeqNum, commitMsg.View)
	if exists {
		if slot.view != commitMsg.View {
			if slot.view < commitMsg.View {
				n.log.Error("Received Commit message for a lower view than existing log entry")
				return
			} else {
				n.log.Error("Received Commit message for a higher view than existing log entry")
				return
			}
		} else {
			if slot.preprepare != nil && slot.preprepare.DigestClientMsg != commitMsg.Digest {
				n.log.Error("equivcation at commit")
				return
			}
		}
	}
	slot.commits[commitMsg.From] = commitMsg.Digest
	slot.view = commitMsg.View // if already exist then view is already set but if not then set
	n.tryExecute(slot)
}

func (n *Node) tryExecute(slot *LogEntry) {

	if slot.committed {
		return
	}
	if slot.preprepare == nil {
		return
	}
	if slot.commitSent == false {
		return
	}
	if slot.executed {
		n.log.Error("should not be executed")
		return
	}
	if len(slot.commits) < n.QuorumSize() || matchingVotesC(slot.commits, slot.preprepare.DigestClientMsg) < n.QuorumSize() {

		return
	}
	slot.committed = true
	n.exeLoop()
}

func (n *Node) sendCommitTps(clientMsg core.ClientMsg) {
	commitTpsMsg := core.CommitTps{
		From: n.GetAddr(),
		To:   config.ClientAddr,
		ClientMsg: core.ClientMsgReply{
			Id:         clientMsg.Id,
			Timestamp:  clientMsg.Timestamp,
			Txn:        clientMsg.Txn,
			ClientName: clientMsg.ClientName,
		},
	}
	n.messageHub.Send(core.MsgCommitTpsMessage, config.ClientAddr, commitTpsMsg, nil)
}

func ComputeBatchDigest(batch []core.ClientMsgSignature) ([32]byte, [][32]byte, error) {
	if len(batch) == 0 {
		return [32]byte{}, nil, fmt.Errorf("cannot digest empty client-message batch")
	}

	payload := &transportpb.ClientBatchDigestPayload{
		ClientMsgs: make([]*transportpb.ClientMsg, 0, len(batch)),
	}
	requestDigests := make([][32]byte, 0, len(batch))
	marshalOptions := proto.MarshalOptions{Deterministic: true}
	for i, clientMsg := range batch {
		clientMsgPB := transportpb.ClientMsgToPB(clientMsg.Data)
		payload.ClientMsgs = append(payload.ClientMsgs, clientMsgPB)

		clientMsgData, err := marshalOptions.Marshal(clientMsgPB)
		if err != nil {
			return [32]byte{}, nil, fmt.Errorf("marshal client message at index %d: %w", i, err)
		}
		requestDigests = append(requestDigests, sha256.Sum256(clientMsgData))
	}

	data, err := marshalOptions.Marshal(payload)
	if err != nil {
		return [32]byte{}, nil, fmt.Errorf("marshal client-message batch: %w", err)
	}
	return sha256.Sum256(data), requestDigests, nil
}

func matchingVotes(votes map[int]core.PrepareMsgSig, target [32]byte) int {
	count := 0
	for _, d := range votes {
		if d.PrepareMsg.Digest == target {
			count++
		}
	}
	return count
}

func matchingVotesC(votes map[int][32]byte, target [32]byte) int {
	count := 0
	for _, d := range votes {
		if d == target {
			count++
		}
	}
	return count
}

func (n *Node) GetView() int64 {
	return n.view
}

func (n *Node) GetLeaderId() int {
	return n.leaderId
}

func (n *Node) CurrentSequenceNumber() int64 {
	return n.sequenceNumber
}

func (n *Node) GetNextSeqNum() int64 {
	n.sequenceNumber++
	return n.sequenceNumber
}

func (n *Node) AllowedMaxInFlight() int64 {
	return n.cfg.MaxInflightSeq
}

func (n *Node) GetLastExecuted() int64 {
	return n.lastExecuted
}

func (n *Node) asyncBroadCast(msgType string, msg interface{}, signature []byte) {
	for _, othersIp := range config.NodeAddr {
		if othersIp == n.GetAddr() {
			continue
		}
		// preprepareMsg.To = othersIp
		go n.messageHub.Send(msgType, othersIp, msg, signature)
	}
}

func (n *Node) asyncBroadcastCommit(view, seq int64, digest [32]byte) {
	msg := core.CommitMsg{
		View:   view,
		SeqNum: seq,
		Digest: digest,
		From:   n.GetNodeID(),
	}

	pbMsg := transportpb.CommitToPB(msg)
	payloadBytes, err := marshalDeterministic(pbMsg)
	if err != nil {
		n.log.Error("Failed to marshal Commit message for signing: %v", err)
		return
	}
	signature := crypto.SignMessageEd25519(payloadBytes, n.encryptionKeyStore.GetPrivateKey())

	for _, othersIp := range config.NodeAddr {
		if othersIp == n.GetAddr() {
			continue
		}
		// msg.To = othersIp
		go n.messageHub.Send(core.MsgCommitMessage, othersIp, msg, signature)
	}
}

func (n *Node) QuorumSize() int {
	return 2*n.fNodes + 1
}
