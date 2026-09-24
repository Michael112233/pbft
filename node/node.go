package node

import (
	"crypto/sha256"
	"fmt"
	"runtime"
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
	defaultTargetThroughput            = 0.92 * 160
	// 8000/batch size
	targetThroughputMaxFactor = 0.92
	ALPHA                     = 1 / float64(10) // for exponential moving average calculation of throughput
	D                         = 3
	THROUGHPUTINTERVAL_DELAY  = 3
)

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
	viewChangeMsgChan              chan ViewChangeMsg
	checkpointMsgChan              chan CheckpointMsg
	newViewMsgChan                 chan NewViewMsg
	electionMsgChan                chan ElectionMsg
	epochMsgChan                   chan EpochProtocolMsg
	clientEventMsgChan             chan core.EventMsg
	learningAgentDecisionCh        chan core.LearningAgentDecision

	electionVDFResultCh chan electionVDFResult
	eventLoopStarted    atomic.Bool
	eventLoopStopOnce   sync.Once
	// electionVDFWorkers             sync.WaitGroup

	pendingRequests       RequestQueue
	batchLogic            Batcher
	leaderProgressTimer   *time.Timer
	leaderProgressTimerCh <-chan time.Time
	newViewTimer          *time.Timer
	newViewTimerCh        <-chan time.Time
	perfTimer             *time.Timer
	perfTimerCh           <-chan time.Time
	epochTimer            *time.Timer
	epochTimerCh          <-chan time.Time
	pool                  *Pool
	consensusLog          *Log
	checkpointManager     *CheckpointManager
	bufferedMsgs          []bufferedConsensusMessage
	electionManager       *ElectionManager
	triggerManager        *TriggerManager
	epochManager          *EpochManager

	////

	encryptionKeyStore *KeyStore

	// view              int64
	leaderId        int
	leaderIdForView map[core.ViewID]int
	// forView           int64
	viewID            core.ViewID
	forViewID         core.ViewID
	currAction        core.Action
	votedFor          int
	viewChangeRunning bool
	viewChangeMsgsLog map[core.ViewID][]*core.ViewChangeMsgSig
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

	dead bool

	performanceTimedTrigger bool
	peakTpsTest             bool
	proposalDelay           bool
	stallState              StallState

	// scenario mode (node/scenario.go); loop-owned except the netem worker channel
	scenarioMode    bool
	currScenario    core.Scenario
	scenarioApplied bool
	netemScriptPath string
	netemCmdCh      chan netemCmd
	netemWorkerDone chan struct{}
	netemStopOnce   sync.Once
	gc              bool
	latencyLog      bool
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
		bufferedMsgs:       make([]bufferedConsensusMessage, 0),
		electionManager:    NewElectionManager(),

		// checkpointManager:  NewCheckpointManager(log),

		eventLoopStopCh:                make(chan struct{}),
		eventLoopDoneCh:                make(chan struct{}),
		receiveVerifiedClientRequestCh: make(chan core.ClientMsgSignature),
		consensusMsgChan:               make(chan ConsensusMsg, cfg.ConsensusChanSize),
		viewChangeMsgChan:              make(chan ViewChangeMsg, 100),
		checkpointMsgChan:              make(chan CheckpointMsg, 100),
		newViewMsgChan:                 make(chan NewViewMsg, 20),
		electionMsgChan:                make(chan ElectionMsg, 100),
		epochMsgChan:                   make(chan EpochProtocolMsg, 20),
		clientEventMsgChan:             make(chan core.EventMsg, 2),
		learningAgentDecisionCh:        make(chan core.LearningAgentDecision, 1),
		electionVDFResultCh:            make(chan electionVDFResult, 1),

		pendingRequests:            NewRequestQueue(cfg.PendingQueueCapacity),
		clientReceiveRateStop:      make(chan struct{}),
		clientReceiveRateDone:      make(chan struct{}),
		leaderPreprepareRateStop:   make(chan struct{}),
		leaderPreprepareRateDone:   make(chan struct{}),
		memoryLoggerStop:           make(chan struct{}),
		memoryLoggerDone:           make(chan struct{}),
		shareLoggerStop:            make(chan struct{}),
		shareLoggerDone:            make(chan struct{}),
		throughputMeasurementsChan: make(chan throughputMeasurement, throughputMeasurementBufferSize),
		throughputMeasurementsStop: make(chan struct{}),
		throughputMeasurementsDone: make(chan struct{}),

		batchLogic: Batcher{
			maxBatchSize:     cfg.MaxBatchSize,
			maxBatchWaitTime: time.Duration(cfg.MaxBatchDelay) * time.Millisecond,
			batch:            make([]core.ClientMsgSignature, 0, cfg.MaxBatchSize),
		},

		// view:            1,
		// forView:         1,
		leaderId:        1,
		leaderIdForView: map[core.ViewID]int{core.ViewID{Generation: 1, Counter: 1}: 1},
		viewID:          core.ViewID{Generation: 1, Counter: 1},
		forViewID:       core.ViewID{Generation: 1, Counter: 1},

		viewChangeMsgsLog: make(map[core.ViewID][]*core.ViewChangeMsgSig),
		viewChangeRunning: false,
		currAction:        cfg.InitialAction(),

		fNodes: (int(cfg.NodeNum) - 1) / 3,

		throughputPerf: ThroughputPerf{
			targetThroughput:             defaultTargetThroughput,
			throughputIntervalStartSeq:   THROUGHPUTINTERVAL_DELAY,
			throughputIntervalStart:      time.Time{},
			throughputObservationStarted: false,
			viewThroughputs:              make(map[core.ViewID]float64),
			maxCounterByGeneration:       make(map[uint64]uint64),

			timedTargetThroughput:   defaultTargetThroughput,
			timedIntervalStartSeq:   THROUGHPUTINTERVAL_DELAY,
			timedIntervalStart:      time.Time{},
			timedObservationStarted: false,
		},
		lm: NewLatencyMonitor(),

		dead:                    cfg.NodesDead[nodeID],
		proposalDelay:           !cfg.ScenarioMode && cfg.ProposalDelayNode == nodeID, // scenario mode owns it otherwise
		scenarioMode:            cfg.ScenarioMode,
		performanceTimedTrigger: cfg.PerformanceTimedTrigger,
		peakTpsTest:             cfg.PeakTpsTest,
		stallState:              StallState{stall: false, view: 1},

		latencyLog: cfg.LatencyLog,
		gc:         cfg.GC,
	}

	n.ArmBatchTimer()
	n.StopBatchTimer()
	checkpointManager := NewCheckpointManager(log, n)
	triggerManager := NewTriggerManager(log, n.currAction.TriggerMode, n)
	n.triggerManager = triggerManager
	epochManager := NewEpochManager(log, n)
	n.epochManager = epochManager
	n.checkpointManager = checkpointManager
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
	if err := n.startScenario(); err != nil {
		n.log.Error("failed to start scenario mode: %v", err)
		return err
	}
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
	n.stopScenario()
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

func (n *Node) tryPropose(fullBatch bool) {
	if n.GetNodeID() != n.GetLeaderId() || n.viewChangeRunning {
		return
	}
	if n.pendingRequests.Len() < n.GetBatchSize() {
		// n.log.Debug("Not enough pending requests to propose a batch: %d < %d", n.pendingRequests.Len(), n.GetBatchSize())
		return
	}
	inflight := n.CurrentSequenceNumber() - n.GetLastExecuted()
	if inflight >= n.AllowedMaxInFlight() {
		// n.log.Debug("Cannot propose: inflight %d >= allowed max inflight %d", inflight, n.AllowedMaxInFlight())
		return
	}
	if n.CurrentSequenceNumber()+1 > n.consensusLog.high {
		n.log.Debug("Cannot propose: next sequence number %d would exceed the high watermark %d", n.CurrentSequenceNumber()+1, n.consensusLog.high)
		return
	}
	// if n.CurrentSequenceNumber() == 100 {
	// 	n.log.Debug("Leader stall aplied")
	// 	time.Sleep(120 * time.Millisecond)
	// }
	// leaderStall := n.GetStall()
	// if leaderStall {
	// 	time.Sleep(200 * time.Millisecond)
	// 	n.log.Debug("Applied leader stall")
	// 	n.SetStall(false)
	// 	return
	// }
	if n.ProposalDelayEnabled() {
		time.Sleep(100 * time.Millisecond)
	}

	reqs := n.pendingRequests.Dequeue(n.GetBatchSize())
	digestBatch, requestDigests, err := ComputeBatchDigest(reqs)
	if err != nil {
		n.log.Error("Failed to compute batch digest: %v", err)
		return
	}
	// view := n.GetView()
	seqNum := n.GetNextSeqNum()
	view := n.GetViewID()
	preprepareMsg := core.PreprepareMsg{
		// View:                       view,
		SeqNum:                     seqNum,
		DigestClientMsg:            digestBatch,
		ClientMsg:                  reqs,
		DigestIndividualClientMsgs: requestDigests,
		View:                       view,
	}
	preprepareMsgMini := core.PreprepareMsgMini{
		// View:                       view,
		SeqNum:                     seqNum,
		DigestClientMsg:            digestBatch,
		DigestIndividualClientMsgs: requestDigests,
		View:                       view,
	}

	payloadBytes, err := marshalDeterministic(preprepareSignPayload(view, seqNum, digestBatch[:]))
	if err != nil {
		n.log.Error("Failed to marshal PrePrepare message for signing: %v", err)

		return
	}

	n.pool.AddBatch(reqs, requestDigests, seqNum, view)

	signature := crypto.SignMessageEd25519(payloadBytes, n.encryptionKeyStore.GetPrivateKey())
	slot, _ := n.consensusLog.GetorCreateEntry(seqNum)
	n.slotPreprepare(slot, &preprepareMsgMini, signature, true)
	// slot.view = view
	slot.view = view
	n.RecordStartTime(seqNum, digestBatch, time.Now())

	n.asyncBroadCast(core.MsgPreprepareMessage, preprepareMsg, signature)
}

func (n *Node) HandlePrePrepare(preprepareMsg core.PreprepareMsg, signature []byte) {
	// above it have check view chnage running ignore
	// view := n.GetView()
	view := n.GetViewID()
	forView := n.GetForViewID()
	// carry_state off: VC certs and the O-set carry no request bodies, so the pool is
	// the only place a new-view install can find them. Keep every batch the leader
	// sends, including ones dropped below (view change running, other view). Seqs
	// below low are covered by a stable checkpoint and never needed; seqs above high
	// are kept, since a lagging node can jump its checkpoint past them and execute them.
	if !n.cfg.CarryState && preprepareMsg.SeqNum >= n.consensusLog.low {
		n.pool.AddBatchIfNewer(preprepareMsg.ClientMsg, preprepareMsg.DigestIndividualClientMsgs, preprepareMsg.SeqNum, preprepareMsg.View)
	}
	if n.viewChangeRunning {
		if preprepareMsg.View.GreaterThan(view) {
			// buffer
			n.bufferConsensusMessage(bufferedConsensusMessage{
				kind:       bufferedPrePrepare,
				view:       preprepareMsg.View,
				preprepare: preprepareMsg,
				signature:  append([]byte(nil), signature...),
			})
			n.log.Info("Buffered PrePrepare for future view (%d,%d) seq %d while current view is (%d,%d)", preprepareMsg.View.Generation, preprepareMsg.View.Counter, preprepareMsg.SeqNum, view.Generation, view.Counter)
			return
		} else if preprepareMsg.View.LessThan(view) {
			n.log.Info("Received PrePrepare for past view (%d,%d) seq %d while current view is (%d,%d), ignoring and for view is (%d,%d)", preprepareMsg.View.Generation, preprepareMsg.View.Counter, preprepareMsg.SeqNum, view.Generation, view.Counter, forView.Generation, forView.Counter)
		} else if preprepareMsg.SeqNum%10 == 0 {
			n.log.Info("Received PrePrepare for current view (%d,%d) seq %d but currently in view change, ignoring and for view is (%d,%d)", preprepareMsg.View.Generation, preprepareMsg.View.Counter, preprepareMsg.SeqNum, forView.Generation, forView.Counter)
		}

		return

	}

	if !n.viewChangeRunning && preprepareMsg.View.GreaterThan(view) {
		n.log.Warn("Interesting case: Received PrePrepare for future view (%d,%d) seq %d while current view is (%d,%d), ignoring and for view is (%d,%d)", preprepareMsg.View.Generation, preprepareMsg.View.Counter, preprepareMsg.SeqNum, view.Generation, view.Counter, forView.Generation, forView.Counter)
		return
	}

	if preprepareMsg.View.NotEqual(view) {
		return
	}

	if preprepareMsg.SeqNum < n.consensusLog.low {
		// n.log.Info("Received PrePrepare for seq %d which is below low watermark %d, ignoring", preprepareMsg.SeqNum, n.consensusLog.low)
		return
	}
	if preprepareMsg.SeqNum > n.consensusLog.high {
		n.log.Info("Received PrePrepare for seq %d which is above high watermark %d, ignoring", preprepareMsg.SeqNum, n.consensusLog.high)
		return
	}

	// can skip or optimise it later
	if !n.verifyPreprepareClientMessages(preprepareMsg.ClientMsg) {
		return
	}
	// digest check turned off , can turn them on or parallelise them
	// // can use optimization and dont use same func as indv digest already there in preprepare
	// digestBatch, _, err := ComputeBatchDigest(preprepareMsg.ClientMsg)
	// if err != nil {
	// 	n.log.Error("Failed to compute batch digest: %v", err)
	// 	return
	// }

	// if digestBatch != preprepareMsg.DigestClientMsg {
	// 	n.log.Error("Batch digest mismatch")
	// 	return
	// }
	slot, exists := n.consensusLog.GetorCreateEntry(preprepareMsg.SeqNum)
	// slots above masSeq of O only survive if prepared and even for those view aligned at new view, so can raise error if view greater or less
	if exists {
		if slot.view.NotEqual(preprepareMsg.View) {
			if slot.view.LessThan(preprepareMsg.View) {
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
	if n.cfg.CarryState {
		n.pool.AddBatch(preprepareMsg.ClientMsg, preprepareMsg.DigestIndividualClientMsgs, preprepareMsg.SeqNum, preprepareMsg.View)
	}
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

// verifyPreprepareClientMessages verifies every client message signature in a
// preprepare's batch across a worker pool. Verification is a pure function of
// message bytes and the (immutable) client public key, so it is safe to run
// concurrently as long as no worker touches node state. Returns false as soon
// as any signature fails to verify or fails to marshal, matching the
// reject-the-whole-batch semantics of the original serial loop.
func (n *Node) verifyPreprepareClientMessages(clientMsgs []core.ClientMsgSignature) bool {
	if len(clientMsgs) == 0 {
		return true
	}

	workers := runtime.NumCPU()
	if workers > len(clientMsgs) {
		workers = len(clientMsgs)
	}
	if workers < 1 {
		workers = 1
	}
	chunkSize := (len(clientMsgs) + workers - 1) / workers

	var wg sync.WaitGroup
	var failed atomic.Bool
	var reportOnce sync.Once

	for start := 0; start < len(clientMsgs); start += chunkSize {
		end := start + chunkSize
		if end > len(clientMsgs) {
			end = len(clientMsgs)
		}
		wg.Add(1)
		go func(chunk []core.ClientMsgSignature) {
			defer wg.Done()
			for _, clientmsg := range chunk {
				if failed.Load() {
					return
				}
				clientMsgBytes, err := marshalDeterministic(transportpb.ClientMsgToPB(clientmsg.Data))
				if err != nil {
					failed.Store(true)
					reportOnce.Do(func() {
						n.log.Error("Failed to marshal client message for verification: %v", err)
					})
					return
				}
				if !crypto.VerifySignatureEd25519(clientMsgBytes, clientmsg.Signature, n.encryptionKeyStore.clientKey) {
					failed.Store(true)
					reportOnce.Do(func() {
						n.log.Error("Failed to verify client message signature")
					})
					return
				}
			}
		}(clientMsgs[start:end])
	}
	wg.Wait()

	return !failed.Load()
}

func (n *Node) HandlePrepare(prepareMsg core.PrepareMsg, signature []byte) {
	view := n.GetViewID()
	forView := n.GetForViewID()
	if n.viewChangeRunning {
		if prepareMsg.View.GreaterThan(view) {
			n.bufferConsensusMessage(bufferedConsensusMessage{
				kind:      bufferedPrepare,
				view:      prepareMsg.View,
				prepare:   prepareMsg,
				signature: append([]byte(nil), signature...),
			})
			// n.log.Info("Buffered Prepare for future view %d seq %d while current view is %d", prepareMsg.View, prepareMsg.SeqNum, view)
			return
		} else if prepareMsg.View.LessThan(view) {
			n.log.Info("Received Prepare for past view (%d,%d) seq %d while current view is (%d,%d), ignoring and for view is (%d,%d)", prepareMsg.View.Generation, prepareMsg.View.Counter, prepareMsg.SeqNum, view.Generation, view.Counter, forView.Generation, forView.Counter)
		} else if prepareMsg.SeqNum%10 == 0 {
			n.log.Info("Received Prepare for current view (%d,%d) seq %d but currently in view change, ignoring and for view is (%d,%d)", prepareMsg.View.Generation, prepareMsg.View.Counter, prepareMsg.SeqNum, forView.Generation, forView.Counter)
		}
		// n.viewMu.RUnlock()

		return
	}
	// this is possible lets say only leader stall and in meanwhile other do vc and send prepare from bigger view
	if !n.viewChangeRunning && prepareMsg.View.GreaterThan(view) {
		// n.log.Warn("Interesting case: Received Prepare for future view (%d,%d) seq %d while current view is (%d,%d), ignoring and for view is (%d,%d)", prepareMsg.View.Generation, prepareMsg.View.Counter, prepareMsg.SeqNum, view.Generation, view.Counter, forView.Generation, forView.Counter)
		return
	}

	if prepareMsg.View.NotEqual(view) {
		return
	}

	if prepareMsg.SeqNum < n.consensusLog.low {
		// n.log.Info("Received Prepare for seq %d which is below low watermark %d, ignoring", prepareMsg.SeqNum, n.consensusLog.low)
		return
	}
	if prepareMsg.SeqNum > n.consensusLog.high {
		n.log.Info("Received Prepare for seq %d which is above high watermark %d, ignoring", prepareMsg.SeqNum, n.consensusLog.high)
		return
	}

	slot, exists := n.consensusLog.GetorCreateEntry(prepareMsg.SeqNum)
	// slots above masSeq of O only survive if prepared and even for those view aligned at new view, so can raise error if view greater or less
	if exists {
		if slot.view.NotEqual(prepareMsg.View) {
			if slot.view.LessThan(prepareMsg.View) {
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
	// or may receive prepare before preprepare
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
	// Snapshot the prepared certificate now, keyed by the view it prepared in. It must
	// survive every later view change until a stable checkpoint passes this seq, so it
	// can be replayed in the ViewChange P-set (Castro-Liskov 2.3.2).
	slot.recordPreparedIfHigher(slot.view, n.buildPreparedCert(slot))
	slot.commitSent = true
	commitDigest := slot.preprepare.DigestClientMsg
	slot.commits[n.GetNodeID()] = commitDigest
	go n.asyncBroadcastCommit(slot.view, slot.preprepare.SeqNum, commitDigest)
	n.tryExecute(slot)

}

// performance could be optimised
// buildPreparedCert snapshots the slot's current-view pre-prepare together with the 2f
// matching prepares into a self-contained certificate for a future ViewChange P-set.
// The returned value shares nothing mutable with the live slot.
func (n *Node) buildPreparedCert(slot *LogEntry) *core.PreparedCert {
	if slot.preprepare == nil {
		return nil
	}
	preprepareV := core.PreprepareMsgSig{
		PreprepareMsgMini: core.PreprepareMsgMini{
			View:                       slot.preprepare.View,
			SeqNum:                     slot.preprepare.SeqNum,
			DigestClientMsg:            slot.preprepare.DigestClientMsg,
			DigestIndividualClientMsgs: slot.preprepare.DigestIndividualClientMsgs,
		},
		Signature: append([]byte(nil), slot.preprepareSignature...),
	}
	// only heavy part
	if n.cfg.CarryState {
		if actualReqs, ok := n.pool.GetBatch(slot.preprepare.DigestIndividualClientMsgs); ok {
			preprepareV.ActualMsg = actualReqs
		} else {
			// n.log.Error("buildPreparedCert: actual requests for seq %d not in pool", slot.preprepare.SeqNum)
		}
	}
	prepareLog := make(map[int]core.PrepareMsgSig, len(slot.prepares))
	for from, prepare := range slot.prepares {
		if prepare.PrepareMsg.Digest != slot.preprepare.DigestClientMsg {
			continue
		}
		prepareLog[from] = core.PrepareMsgSig{
			PrepareMsg: core.PrepareMsg{
				View:   prepare.PrepareMsg.View,
				SeqNum: prepare.PrepareMsg.SeqNum,
				Digest: prepare.PrepareMsg.Digest,
				From:   prepare.PrepareMsg.From,
			},
			Signature: append([]byte(nil), prepare.Signature...),
		}
	}
	return &core.PreparedCert{PreprepareMsg: preprepareV, PrepareLog: prepareLog}
}

func (n *Node) HandleCommit(commitMsg core.CommitMsg) {
	view := n.GetViewID()
	forView := n.GetForViewID()
	if n.viewChangeRunning {
		if commitMsg.View.GreaterThan(view) {
			n.bufferConsensusMessage(bufferedConsensusMessage{
				kind:   bufferedCommit,
				view:   commitMsg.View,
				commit: commitMsg,
			})
			// n.log.Info("Buffered Commit for future view %d seq %d while current view is %d", commitMsg.View, commitMsg.SeqNum, view)
			return
		} else if commitMsg.View.LessThan(view) {
			n.log.Info("Received Commit for past view (%d,%d) seq %d while current view is (%d,%d), ignoring", commitMsg.View.Generation, commitMsg.View.Counter, commitMsg.SeqNum, view.Generation, view.Counter)
		} else if commitMsg.SeqNum%10 == 0 {
			n.log.Info("Received Commit for current view (%d,%d) (equal views) seq %d but currently in view change, ignoring and for view is (%d,%d)", commitMsg.View.Generation, commitMsg.View.Counter, commitMsg.SeqNum, forView.Generation, forView.Counter)
		}
		// n.viewMu.RUnlock()

		return
	}

	if !n.viewChangeRunning && commitMsg.View.GreaterThan(view) {
		n.log.Warn("Interesting case: Received Commit for future view (%d,%d) seq %d while current view is (%d,%d), ignoring and for view is (%d,%d)", commitMsg.View.Generation, commitMsg.View.Counter, commitMsg.SeqNum, view.Generation, view.Counter, forView.Generation, forView.Counter)
		return
	}

	if commitMsg.View.NotEqual(view) {
		return
	}

	if commitMsg.SeqNum < n.consensusLog.low {
		// n.log.Info("Received Commit for seq %d which is below low watermark %d, ignoring", commitMsg.SeqNum, n.consensusLog.low)
		return
	}
	if commitMsg.SeqNum > n.consensusLog.high {
		n.log.Info("Received Commit for seq %d which is above high watermark %d, ignoring", commitMsg.SeqNum, n.consensusLog.high)
		return
	}

	slot, exists := n.consensusLog.GetorCreateEntry(commitMsg.SeqNum)
	if exists {
		if slot.view.NotEqual(commitMsg.View) {
			if slot.view.LessThan(commitMsg.View) {
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
	// every view change this will hit as executed survive across view and Oset reproposes all thing above last stable
	if slot.executed {
		// n.log.Debug("should not be executed")
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

// func (n *Node) GetView() int64 {
// 	return n.view
// }

func (n *Node) GetViewID() core.ViewID {
	return n.viewID
}

func (n *Node) GetForViewID() core.ViewID {
	return n.forViewID
}

func (n *Node) SetViewID(view core.ViewID) {
	n.viewID = view
}

// SetForViewID is the only place the generation moves (all three generation
// switch paths end here), so it also drives the scenario switch.
func (n *Node) SetForViewID(view core.ViewID) {
	n.forViewID = view
	n.maybeSwitchScenario(view.Generation)
}
func (n *Node) SetCurrAction(action core.Action) {
	n.currAction = action
}

func (n *Node) GetCurrAction() core.Action {
	return n.currAction
}
func (n *Node) GetLeaderId() int {
	return n.leaderId
}

func (n *Node) IsLeader() bool {
	return n.GetNodeID() == n.GetLeaderId()
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

func (n *Node) MinVDFDelay() int {
	return n.cfg.MinVDFDelay
}

func (n *Node) MaxVDFDelay() int {
	return n.cfg.MaxVDFDelay
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
func (n *Node) sendLeaderIdUpdate(newLeaderID int, view core.ViewID) {
	leaderUpdateMsg := core.LeaderIdUpdate{
		From:        n.GetAddr(),
		To:          config.ClientAddr,
		NewLeaderId: newLeaderID,
		View:        view,
	}
	// time.Sleep(1000 * time.Millisecond) // add delay to ensure client receives view change messages before leader update, can remove when client can handle out of order messages
	n.messageHub.Send(core.MsgLeaderIdUpdateMessage, config.ClientAddr, leaderUpdateMsg, nil)
}

func (n *Node) asyncBroadcastCommit(view core.ViewID, seq int64, digest [32]byte) {
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

func (n *Node) ProposalDelayEnabled() bool {
	return n.proposalDelay
}

func (n *Node) SetProposalDelay(enabled bool) {
	n.proposalDelay = enabled
}

type StallState struct {
	stall bool
	view  int64
}

// func (n *Node) SetStall(stall bool) {
// 	n.stallState = StallState{stall: stall, view: n.GetView()}
// }

// // GetStall reports whether a stall was requested in the current view. A stall
// // requested in an earlier view is ignored so it cannot leak into a later view
// // where this replica becomes primary again.
// func (n *Node) GetStall() bool {
// 	return n.stallState.stall && n.stallState.view == n.GetView()
// }

func (n *Node) assert(condition bool, format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)
	if !condition {
		n.log.Error("Assertion failed: %s", message)
	}
}
