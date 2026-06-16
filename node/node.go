package node

import (
	"bytes"
	"math/big"
	"strconv"

	"crypto/sha256"
	"sort"
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
	defaultPBFTRequestTimeout          = 5 * time.Second
	defaultPBFTRequestTimeoutJitterMax = 500 * time.Millisecond
	CHECKPOINT_INTERVAL                = 250
	defaultTargetThroughput            = 0.90 * 2000
	targetThroughputMaxFactor          = 0.90
	ALPHA                              = 1 / float64(10) // for exponential moving average calculation of throughput
	D                                  = 3
	THROUGHPUTINTERVAL_DELAY           = 100
)

type clientRequestKey struct {
	clientName string
	id         int64
}

type checkpoint struct {
	seq    int64
	digest [32]byte
}

type CheckpointData struct {
	votes    map[int]core.CheckpointMsgSig
	balances map[string]*big.Int
}

type bufferedConsensusMessageKind uint8

const (
	bufferedPrePrepare bufferedConsensusMessageKind = iota + 1
	bufferedPrepare
	bufferedCommit
)

type bufferedConsensusMessage struct {
	kind       bufferedConsensusMessageKind
	view       int64
	preprepare core.PreprepareMsg
	prepare    core.PrepareMsg
	commit     core.CommitMsg
	signature  []byte
}

type Node struct {
	NodeID int

	cfg        *config.Config
	log        *logger.Logger
	messageHub *NodeMessageHub

	////
	unverifiedClientMsgsChan  chan []core.ClientMsgSignature // ptr or no
	verifiedClientMsgsChan    chan core.ClientMsgSignature   // ptr or no
	encryptionKeyStore        *KeyStore
	preprepareSem             chan struct{}
	preprepareSeqNumber       atomic.Int64
	view                      int64
	leaderId                  int
	leaderIdForView           map[int64]int
	forView                   int64
	votedFor                  int
	consensusLog              ConsensusLog
	viewChangeRunning         bool
	viewMu                    sync.RWMutex
	periodInterval            int64 // protected by viewMu
	bufferedMsgsMu            sync.Mutex
	bufferedMsgs              []bufferedConsensusMessage
	vcType                    core.VCType
	fNodes                    int
	verificationWorkerStarted atomic.Bool

	pbftTimerManager *TimerManager

	viewChangeMsgsLog map[int64][]*core.ViewChangeMsgSig
	voteLog           map[int64][]int

	pool *Pool

	executionMachine  execution.StateMachine
	executionMu       sync.Mutex
	lastExecuted      int64
	noOpsExecuted     atomic.Int64
	pendingExecutions map[int64]pendingExecution

	checkpointMu                   sync.Mutex
	checkpoints                    map[checkpoint]CheckpointData
	lastStableCheckpoint           checkpoint //if laststable is updated then balances cant be nil
	stateRequestTransferInProgress int64

	throughputMu                  sync.RWMutex
	checkpointThroughputs         map[int64][]float64
	throughputIntervalStart       time.Time
	throughputIntervalStartSeq    int64
	targetThroughput              float64
	throughputObservationStarted  bool
	throughputMeasurementsChan    chan throughputMeasurement
	throughputMeasurementsStop    chan struct{}
	throughputMeasurementsDone    chan struct{}
	throughputMeasurementsStarted atomic.Bool
	throughputMeasurementsOnce    sync.Once
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

	//locked by viewmu
	scoreboard *Scoreboard

	dead               bool
	split              bool
	periodic           bool
	performanceTrigger bool
	peakTpsTest        bool
	proposalDelay      bool
	gc                 bool
}

func NewNode(nodeID int, cfg *config.Config) *Node {

	log := logger.NewLogger(nodeID, "node")
	return &Node{
		NodeID: nodeID,

		cfg:        cfg,
		log:        log,
		messageHub: NewNodeMessageHub(),

		encryptionKeyStore:             NewKeyStore(nodeID, cfg.NodeNum),
		unverifiedClientMsgsChan:       make(chan []core.ClientMsgSignature, 100), // buffer size can be tuned
		verifiedClientMsgsChan:         make(chan core.ClientMsgSignature, 100),   // buffer size can be tuned
		preprepareSem:                  make(chan struct{}, 5000),
		preprepareSeqNumber:            atomic.Int64{},
		view:                           1,
		forView:                        1,
		vcType:                         cfg.LeaderTypeEnum,
		leaderId:                       1,
		leaderIdForView:                map[int64]int{1: 1},
		consensusLog:                   NewConsensusLog(),
		viewChangeRunning:              false,
		bufferedMsgs:                   make([]bufferedConsensusMessage, 0),
		fNodes:                         (int(cfg.NodeNum) - 1) / 3,
		pbftTimerManager:               NewTimerManager(log),
		viewChangeMsgsLog:              make(map[int64][]*core.ViewChangeMsgSig),
		voteLog:                        make(map[int64][]int),
		pool:                           NewPool(),
		executionMachine:               execution.NewAccountStateMachine(),
		pendingExecutions:              make(map[int64]pendingExecution),
		checkpoints:                    make(map[checkpoint]CheckpointData),
		lastStableCheckpoint:           checkpoint{seq: 0, digest: [32]byte{}},
		stateRequestTransferInProgress: -1,
		checkpointThroughputs:          make(map[int64][]float64),
		throughputIntervalStartSeq:     THROUGHPUTINTERVAL_DELAY,
		targetThroughput:               defaultTargetThroughput,
		throughputObservationStarted:   false,

		throughputMeasurementsChan: make(chan throughputMeasurement, throughputMeasurementBufferSize),
		throughputMeasurementsStop: make(chan struct{}),
		throughputMeasurementsDone: make(chan struct{}),
		clientReceiveRateStop:      make(chan struct{}),
		clientReceiveRateDone:      make(chan struct{}),
		leaderPreprepareRateStop:   make(chan struct{}),
		leaderPreprepareRateDone:   make(chan struct{}),
		memoryLoggerStop:           make(chan struct{}),
		memoryLoggerDone:           make(chan struct{}),
		split:                      false,
		dead:                       cfg.NodesDead[nodeID],
		proposalDelay:              cfg.ProposalDelayNode == nodeID,
		periodic:                   cfg.Periodic,
		performanceTrigger:         cfg.PerformanceTrigger,
		peakTpsTest:                cfg.PeakTpsTest,
		periodInterval:             cfg.Period,
		scoreboard:                 NewScoreboard(cfg.NodeNum),
		gc:                         cfg.GC,
	}
}

func (n *Node) Start() {
	n.messageHub.Start(n, &sync.WaitGroup{})
	if n.throughputMeasurementsStarted.CompareAndSwap(false, true) {
		go n.throughputMeasurementCSVWriter()
	}
	if n.cfg.Logging {
		if n.memoryLoggerStarted.CompareAndSwap(false, true) {
			component := "node_" + strconv.Itoa(n.NodeID)
			go utils.StartMemoryLogger("logs/"+component+"_mem.log", component, 10*time.Second, n.memoryLoggerStop, n.memoryLoggerDone)
		}
		if n.clientReceiveRateStarted.CompareAndSwap(false, true) {
			go n.clientReceiveRateLogger()
		}
		if n.leaderPreprepareRateStarted.CompareAndSwap(false, true) {
			go n.leaderPreprepareRateLogger()
		}
	}
	go n.ClientSignatureVerifier()
	go n.VerifiedClientMessageHandler()
	if n.pbftTimerManager.pbftTimerStarted.CompareAndSwap(false, true) {
		go n.pbftTimerManager.pbftTimerWorker(n)
	}
	n.log.Info("node started")
}

func (n *Node) Stop() {
	// Stop all expire timers to prevent resource leaks
	// n.StopAllExpireTimers()
	// Close network resources to stop listeners and connections
	if n.messageHub != nil {
		n.messageHub.Close()
	}
	n.pbftTimerManager.pbftTimerStopOnce.Do(func() {
		close(n.pbftTimerManager.pbftTimerStopCh)
	})
	n.pbftTimerManager.lock.Lock()
	n.pbftTimerManager.stopPBFTTimerLocked()
	n.pbftTimerManager.lock.Unlock()
	n.throughputMeasurementsOnce.Do(func() {
		close(n.throughputMeasurementsStop)
	})
	if n.throughputMeasurementsStarted.Load() {
		<-n.throughputMeasurementsDone
	}
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

func (n *Node) ClientSignatureVerifier() {
	for {
		select {
		case clientMsgSigs := <-n.unverifiedClientMsgsChan:

			// Verify signatures
			for _, clientMsgSig := range clientMsgSigs {

				n.verifiedClientMsgsChan <- clientMsgSig
			}
			n.log.Info("Verifying client message signatures, count: %d", len(clientMsgSigs))
		}
	}
}

func (n *Node) VerifiedClientMessageHandler() {
	const (
		batchTimeout = 5000 * time.Millisecond // Adjust as needed
	)
	batch := make([]core.ClientMsgSignature, 0, n.cfg.MaxBlockSize)
	timer := time.NewTimer(batchTimeout)
	timer.Stop() // Initially stop the timer
	for {
		select {
		case clientMsgSig := <-n.verifiedClientMsgsChan: // can be block and then leftover txn can go in next batch
			batch = append(batch, clientMsgSig)
			if len(batch) == 1 {
				// Start the timer when the first message arrives
				timer.Reset(batchTimeout)
			}
			if len(batch) >= int(n.cfg.MaxBlockSize) {
				// Process full batch
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				n.processClientMessageBatch(batch) // will block on sem and put backpressure, maybe pool when block
				batch = nil

			}
		case <-timer.C:
			if len(batch) > 0 {
				// Process whatever is in the batch when the timer expires
				n.log.Info("Batch timeout reached, processing batch of size: %d", len(batch))
				n.processClientMessageBatch(batch)
				batch = nil
			}
		}
	}
}

func (n *Node) processClientMessageBatch(batch []core.ClientMsgSignature) {
	n.preprepareSem <- struct{}{} // Acquire semaphore, may add default to drop batch if full

	go func() {
		defer func() { <-n.preprepareSem }()
		n.preprepare(batch[0])
	}()
}

func ComputeBatchDigest(batch core.ClientMsg) ([32]byte, error) {
	// can use buf pool and blake 2b later for optimization
	// also can have worker for batch digest computation if it becomes bottleneck
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(transportpb.ClientMsgToPB(batch))
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(data), nil
}
func matchingVotes(votes map[int]*core.PrepareMsgSig, target [32]byte) int {
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
func (n *Node) broadcastPrepare(msg core.PrepareMsg, signature []byte) {
	for _, othersIp := range config.NodeAddr {
		if othersIp == n.GetAddr() {
			continue
		}
		go n.messageHub.Send(core.MsgPrepareMessage, othersIp, msg, signature)
	}
}
func (n *Node) broadcastViewChange(msg core.ViewChangeMsg, signature []byte) {
	n.log.Info("Broadcasting ViewChange for view %d from node %d", msg.ViewNumber, n.GetNodeID())
	for _, othersIp := range config.NodeAddr {
		if othersIp == n.GetAddr() {
			continue
		}
		go n.messageHub.Send(core.MsgViewChangeMessage, othersIp, msg, signature)
	}
}

// sig := n.sign(marshal(msg))
// signed := SignedPrepare{Data: msg, Signature: sig}

// for _, othersIp := range config.NodeAddr {
// 	if othersIp == n.GetAddr() {
// 		continue
// 	}
// 	// msg.To = othersIp
// 	n.messageHub.Send(core.MsgPrepareMessage, othersIp, msg, nil) // cant do go in current
// }
// }

func (n *Node) broadcastCommit(view, seq int64, digest [32]byte) {
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
func (n *Node) preprepare(batch core.ClientMsgSignature) {

	n.viewMu.RLock()
	defer n.viewMu.RUnlock()
	if n.viewChangeRunning || n.leaderId != n.GetNodeID() {
		// n.viewMu.RUnlock()
		return
	}
	view := n.view
	periodInterval := n.periodInterval
	// n.viewMu.RUnlock()
	var seqNum int64
	if n.cfg.Periodic {
		for {
			currentSeq := n.preprepareSeqNumber.Load()
			if currentSeq >= periodInterval {
				n.log.Info("PrePrepare skipped for view %d because seq %d reached period interval %d", view, currentSeq, periodInterval)
				return
			}
			if n.preprepareSeqNumber.CompareAndSwap(currentSeq, currentSeq+1) {
				seqNum = currentSeq + 1
				break
			}
		}
	} else {
		seqNum = n.preprepareSeqNumber.Add(1)
	}

	digestClientMsg, err := ComputeBatchDigest(batch.Data)
	if err != nil {
		n.log.Error("Failed to compute batch digest: %v", err)
		return
	}

	preprepareMsg := core.PreprepareMsg{
		View:            view,
		SeqNum:          seqNum,
		ClientMsg:       batch,
		DigestClientMsg: digestClientMsg,
		// DigestClientMsg: digestClientMsg,
		// ideally sign preprepare with digest so less costly and piggy back client messages, but for simplicity we sign whole preprepare message here
	}

	// pbMsg := transportpb.PreprepareToPB(preprepareMsg)
	payloadBytes, err := marshalDeterministic(preprepareSignPayload(view, seqNum, digestClientMsg[:]))
	signature := crypto.SignMessageEd25519(payloadBytes, n.encryptionKeyStore.GetPrivateKey())

	slot := n.consensusLog.getOrCreateLog(seqNum, view)
	slot.mu.Lock()
	slot.prePrepare = &preprepareMsg
	slot.missingData = false
	// slot.prePrepare.
	// slot.digest = digestClientMsg
	slot.prePrepareSig = signature
	slot.prepareSent = true
	slot.mu.Unlock()
	if n.cfg.Logging {
		n.leaderPrepreparesProcessed.Add(1)
	}
	// n.viewMu.RUnlock()

	n.pool.Add(digestClientMsg, batch)
	n.pbftTimerManager.trackPreprepareRequest()
	n.log.Test("PrePrepare sent for view %d seq %d with batch size %d", view, seqNum, 1)
	// go func() {
	if n.proposalDelay {

		time.Sleep(time.Duration(n.cfg.ProposalDelayMS) * time.Millisecond)

	}
	for _, othersIp := range config.NodeAddr {
		if othersIp == n.GetAddr() {
			continue
		}
		// preprepareMsg.To = othersIp
		go n.messageHub.Send(core.MsgPreprepareMessage, othersIp, preprepareMsg, signature) // cant do go in current state race
	}
	// }()

}

func (n *Node) HandlePrePrepare(preprepareMsg core.PreprepareMsg, signature []byte) {
	n.viewMu.RLock()
	view := n.view
	forView := n.forView
	viewChangeRunning := n.viewChangeRunning
	defer n.viewMu.RUnlock()

	if viewChangeRunning {
		if preprepareMsg.View > view {
			n.bufferConsensusMessage(bufferedConsensusMessage{
				kind:       bufferedPrePrepare,
				view:       preprepareMsg.View,
				preprepare: preprepareMsg,
				signature:  append([]byte(nil), signature...),
			})
			n.log.Info("Buffered PrePrepare for future view %d seq %d while current view is %d", preprepareMsg.View, preprepareMsg.SeqNum, view)
			return
		} else if preprepareMsg.View < view {
			n.log.Info("Received PrePrepare for past view %d seq %d while current view is %d, ignoring and for view is %d", preprepareMsg.View, preprepareMsg.SeqNum, view, forView)
		} else {
			n.log.Info("Received PrePrepare for current view %d (equal views) seq %d but currently in view change, ignoring and for view is %d", preprepareMsg.View, preprepareMsg.SeqNum, forView)
		}
		// n.viewMu.RUnlock()

		return
	}
	n.log.Test("Received PrePrepare for view %d seq %d", preprepareMsg.View, preprepareMsg.SeqNum)
	// n.viewMu.RUnlock()

	// --- Validation ---
	if preprepareMsg.View != view {
		// n.viewMu.RUnlock()
		return
	}
	// if pp.SenderID != n.leaderID() {
	// 	return
	// }
	// if pp.SeqNum <= n.LowWaterMark || pp.SeqNum > n.HighWaterMark {
	// 	return
	// }
	// if !n.verify(pp.SenderID, marshal(pp), msg.Signature) {
	// 	return
	// }
	clientMsgBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(transportpb.ClientMsgToPB(preprepareMsg.ClientMsg.Data))
	if err != nil {
		n.log.Error("Failed to marshal client message for signature verification: %v", err)
		return
	}
	verified := crypto.VerifySignatureEd25519(clientMsgBytes, preprepareMsg.ClientMsg.Signature, n.encryptionKeyStore.clientKey)
	if !verified {
		n.log.Error("Failed to verify client message signature in PrePrepare from %d, seqNum %d", preprepareMsg.View, preprepareMsg.SeqNum)
		return
	}

	digestClientMsg, err := ComputeBatchDigest(preprepareMsg.ClientMsg.Data)
	if err != nil {
		n.log.Error("Failed to compute batch digest: %v", err)
		return
	}
	if digestClientMsg != preprepareMsg.DigestClientMsg {
		n.log.Error("PrePrepare digest mismatch from view %d seq %d", preprepareMsg.View, preprepareMsg.SeqNum)
		return
	}

	slot := n.consensusLog.getOrCreateLog(preprepareMsg.SeqNum, view)
	slot.mu.Lock()

	// // View comparison on the log entry itself
	// if preprepareMsg.View < slot.view {
	// 	// Stale PrePrepare from an older view — ignore
	// 	slot.mu.Unlock()
	// 	return
	// }
	// if preprepareMsg.View > slot.view {
	// 	// New view for this seq — wipe old state
	// 	slot.resetForView(preprepareMsg.View)
	// }

	// Already have a PrePrepare for this (view, seq)? Reject duplicate / conflicting.
	if slot.prePrepare != nil {
		slot.mu.Unlock()
		return
	}

	slot.prePrepare = &preprepareMsg
	slot.prePrepareSig = signature
	slot.missingData = false
	// slot.digest = digestClientMsg
	accepted := true
	if accepted {

		n.pbftTimerManager.trackPreprepareRequest()
	}
	if !slot.prepareSent {

		msg := core.PrepareMsg{
			View:   view,
			SeqNum: preprepareMsg.SeqNum,
			Digest: digestClientMsg,
			From:   n.GetNodeID(),
		}
		pbMsg := transportpb.PrepareToPB(msg)
		payloadBytes, err := marshalDeterministic(pbMsg)
		if err != nil {
			n.log.Error("Failed to marshal Prepare message for signing: %v", err)
			// to be decided
		}
		signature := crypto.SignMessageEd25519(payloadBytes, n.encryptionKeyStore.GetPrivateKey())
		msgForLog := core.PrepareMsgSig{
			PrepareMsg: msg,
			Signature:  signature,
		}
		slot.prepareSent = true

		slot.prepares[n.GetNodeID()] = &msgForLog
		slot.mu.Unlock()

		n.broadcastPrepare(msg, signature)

	} else { //redundant else ?
		slot.mu.Unlock()
	}
	n.pool.Add(digestClientMsg, preprepareMsg.ClientMsg)

	// Buffered prepares may now form quorum with the PrePrepare
	n.tryAdvancePrepare(slot, view, preprepareMsg.SeqNum, digestClientMsg)
}

func (n *Node) HandlePrePrepareNewView(preprepareMsg core.PreprepareMsgMini, signature []byte, actualMsg core.ClientMsgSignature) bool {

	view := n.view
	// n.viewMu.RUnlock()

	// --- Validation ---
	if preprepareMsg.View != view {
		// n.viewMu.RUnlock()
		return false
	}
	// if pp.SenderID != n.leaderID() {
	// 	return
	// }
	// if pp.SeqNum <= n.LowWaterMark || pp.SeqNum > n.HighWaterMark {
	// 	return
	// }

	payloadBytes, err := marshalDeterministic(preprepareSignPayload(preprepareMsg.View, preprepareMsg.SeqNum, preprepareMsg.DigestClientMsg[:]))
	if err != nil {
		n.log.Error("Failed to marshal preprepare mini message for signature verification: %v", err)
		return false
	}
	leaderPubKey, exists := n.encryptionKeyStore.GetPublicKey(n.leaderId)
	if !exists {
		n.log.Error("Failed to get leader public key: %v", err)
		return false
	}
	verified := crypto.VerifySignatureEd25519(payloadBytes, signature, leaderPubKey)
	if !verified {
		n.log.Error("Failed to verify signature in PrePrepareMini from leader %d, view %d seqNum %d", n.leaderId, preprepareMsg.View, preprepareMsg.SeqNum)
		return false
	}

	slot := n.consensusLog.getOrCreateLog(preprepareMsg.SeqNum, preprepareMsg.View)
	missingSlot := false
	slot.mu.Lock()

	// if preprepareMsg.View < slot.view {

	// 	slot.mu.Unlock()
	// 	return
	// } // already did match O this is not possible
	// if preprepareMsg.View > slot.view {
	// 	// New view for this seq — wipe old state
	// 	n.log.Info("Resetting slot for new view %d seq %d due to PrePrepareMini", preprepareMsg.View, preprepareMsg.SeqNum)
	// 	disgestMismatch := slot.resetForNewView(preprepareMsg.View, preprepareMsg.DigestClientMsg)
	// 	if disgestMismatch { // only possible in equivocation
	// 		n.log.Error("Digest mismatch when resetting slot for new view %d seq %d", preprepareMsg.View, preprepareMsg.SeqNum)
	// 		slot.mu.Unlock()
	// 		return false
	// 	}
	// } // should always run
	if slot.prePrepare == nil {
		slot.prePrepare = &core.PreprepareMsg{
			View:            preprepareMsg.View,
			SeqNum:          preprepareMsg.SeqNum,
			DigestClientMsg: preprepareMsg.DigestClientMsg,
		}

	} else {
		n.log.Error("Should be nil")
	}
	// slot.prePrepare.SeqNum = preprepareMsg.SeqNum
	// slot.prePrepare.View = preprepareMsg.View
	slot.prePrepareSig = signature
	// slot.prePrepare.DigestClientMsg = preprepareMsg.DigestClientMsg
	if preprepareMsg.DigestClientMsg != [32]byte{} {
		clientMsg, exists, executed := n.pool.Get(preprepareMsg.DigestClientMsg)
		if exists {
			slot.prePrepare.ClientMsg = clientMsg
			slot.missingData = false
		} else if !exists && !executed {
			// n.log.Error("Replica missing slot on new view for %d", preprepareMsg.SeqNum)
			if actualMsg.Data.Txn == nil {
				n.log.Error("Replica missing slot on new view for seq %d and actual msg is nil", preprepareMsg.SeqNum)
			} else {
				n.log.Error("Replica missing slot but received txn from %s and to %s ", actualMsg.Data.Txn.Sender, actualMsg.Data.Txn.Receiver)
			}
			missingSlot = true
			slot.missingData = true
		} else if !exists && executed {
			slot.missingData = false
			// n.log.Info("PrePrepareMini for already executed client message, view %d seq %d", preprepareMsg.View, preprepareMsg.SeqNum)
		}
		// fail safe for now to handle missing state
		slot.prePrepare.ClientMsg = actualMsg
		slot.missingData = false
	}

	if !slot.prepareSent {

		msg := core.PrepareMsg{
			View:   view,
			SeqNum: preprepareMsg.SeqNum,
			Digest: slot.prePrepare.DigestClientMsg,
			From:   n.GetNodeID(),
		}
		pbMsg := transportpb.PrepareToPB(msg)
		payloadBytes, err := marshalDeterministic(pbMsg)
		if err != nil {
			n.log.Error("Failed to marshal Prepare message for signing: %v", err)
			// to be decided
		}
		signature := crypto.SignMessageEd25519(payloadBytes, n.encryptionKeyStore.GetPrivateKey())
		msgForLog := core.PrepareMsgSig{
			PrepareMsg: msg,
			Signature:  signature,
		}
		slot.prepareSent = true

		slot.prepares[n.GetNodeID()] = &msgForLog

		n.broadcastPrepare(msg, signature)

	}
	slot.mu.Unlock()
	// if accepted {
	// 	n.pool.Add(clientMsg)
	// 	// n.pbftTimerManager.trackPreprepareRequest()
	// }
	return missingSlot

}

func (n *Node) HandlePrepare(prepareMsg core.PrepareMsg, signature []byte) {
	n.viewMu.RLock()
	view := n.view
	forView := n.forView
	viewChangeRunning := n.viewChangeRunning
	defer n.viewMu.RUnlock()
	if viewChangeRunning {
		if prepareMsg.View > view {
			n.bufferConsensusMessage(bufferedConsensusMessage{
				kind:      bufferedPrepare,
				view:      prepareMsg.View,
				prepare:   prepareMsg,
				signature: append([]byte(nil), signature...),
			})
			n.log.Info("Buffered Prepare for future view %d seq %d while current view is %d", prepareMsg.View, prepareMsg.SeqNum, view)
			return
		} else if prepareMsg.View < view {
			n.log.Info("Received Prepare for past view %d seq %d while current view is %d, ignoring and for view is %d", prepareMsg.View, prepareMsg.SeqNum, view, forView)
		} else {
			n.log.Info("Received Prepare for current view %d (equal views) seq %d but currently in view change, ignoring and for view is %d", prepareMsg.View, prepareMsg.SeqNum, forView)
		}
		// n.viewMu.RUnlock()

		return
	}
	n.log.Test("Received Prepare for view %d seq %d", prepareMsg.View, prepareMsg.SeqNum)
	// n.viewMu.RUnlock()

	if prepareMsg.View != view {
		return
	}
	// if p.SeqNum <= n.LowWaterMark || p.SeqNum > n.HighWaterMark {
	// 	return
	// }
	// if !n.verify(p.SenderID, marshal(p), msg.Signature) {
	// 	return
	// }

	slot := n.consensusLog.getOrCreateLog(prepareMsg.SeqNum, view)
	slot.mu.Lock()

	// View check on the log entry
	// if prepareMsg.View < slot.view {
	// 	slot.mu.Unlock()
	// 	return
	// }
	// if prepareMsg.View > slot.view {
	// 	// Prepare arrived before its PrePrepare in a new view — reset and buffer
	// 	n.log.Info("Resetting slot for new view %d seq %d due to Prepare", prepareMsg.View, prepareMsg.SeqNum)
	// 	slot.resetForView(prepareMsg.View)
	// }

	// Digest check: if we have the PrePrepare, the digest must match.
	// If we don't have it yet, store the prepare anyway (out-of-order).
	if slot.prePrepare != nil && slot.prePrepare.DigestClientMsg != prepareMsg.Digest {
		slot.mu.Unlock()
		return
	}
	msgForLog := core.PrepareMsgSig{
		PrepareMsg: prepareMsg,
		Signature:  signature,
	}
	slot.prepares[prepareMsg.From] = &msgForLog
	slot.mu.Unlock()

	n.tryAdvancePrepare(slot, view, prepareMsg.SeqNum, prepareMsg.Digest)
}

func (n *Node) HandleCommit(commitMsg core.CommitMsg) {
	n.viewMu.RLock()
	view := n.view
	forView := n.forView
	viewChangeRunning := n.viewChangeRunning
	defer n.viewMu.RUnlock()
	if viewChangeRunning {
		if commitMsg.View > view {
			n.bufferConsensusMessage(bufferedConsensusMessage{
				kind:   bufferedCommit,
				view:   commitMsg.View,
				commit: commitMsg,
			})
			n.log.Info("Buffered Commit for future view %d seq %d while current view is %d", commitMsg.View, commitMsg.SeqNum, view)
			return
		} else if commitMsg.View < view {
			n.log.Info("Received Commit for past view %d seq %d while current view is %d, ignoring", commitMsg.View, commitMsg.SeqNum, view)
		} else {
			n.log.Info("Received Commit for current view %d (equal views) seq %d but currently in view change, ignoring and for view is %d", commitMsg.View, commitMsg.SeqNum, forView)
		}
		// n.viewMu.RUnlock()

		return
	}
	n.log.Test("Received Commit for view %d seq %d", commitMsg.View, commitMsg.SeqNum)
	// n.viewMu.RUnlock()

	if commitMsg.View != view {
		return
	}
	// if c.SeqNum <= n.LowWaterMark || c.SeqNum > n.HighWaterMark {
	// 	return
	// }
	// if !n.verify(c.SenderID, marshal(c), msg.Signature) {
	// 	return
	// }

	slot := n.consensusLog.getOrCreateLog(commitMsg.SeqNum, view)
	slot.mu.Lock()

	// View check on the log entry
	// if commitMsg.View < slot.view {
	// 	slot.mu.Unlock()
	// 	return
	// }
	// if commitMsg.View > slot.view {
	// 	// Commit arrived before its PrePrepare in a new view — reset and buffer
	// 	slot.resetForView(commitMsg.View)
	// }

	if slot.prePrepare != nil && slot.prePrepare.DigestClientMsg != commitMsg.Digest {
		slot.mu.Unlock()
		return
	} // msybe not needed here

	slot.commits[commitMsg.From] = commitMsg.Digest
	slot.mu.Unlock()

	n.tryExecute(slot, commitMsg.SeqNum)
}

func (n *Node) tryAdvancePrepare(slot *consensusSlot, view, seq int64, digest [32]byte) {
	slot.mu.Lock()

	if slot.commitSent {
		slot.mu.Unlock()
		return // already advanced past prepare
	}
	if slot.prePrepare == nil {
		slot.mu.Unlock()
		return // can't be prepared without PrePrepare
	}
	// Need 2f prepares matching our accepted digest.
	// Leader's PrePrepare is its implicit prepare-phase vote.
	if len(slot.prepares) < 2*n.fNodes || matchingVotes(slot.prepares, slot.prePrepare.DigestClientMsg) < 2*n.fNodes {
		slot.mu.Unlock()
		return
	}

	slot.commitSent = true
	commitDigest := slot.prePrepare.DigestClientMsg
	// Add own commit vote with digest before releasing lock
	slot.commits[n.GetNodeID()] = commitDigest
	slot.mu.Unlock()

	// Broadcast Commit (release lock first to avoid holding during I/O)

	go n.broadcastCommit(view, seq, commitDigest)
	// Commits may already be buffered from peers before we flip commitSent.
	// Re-check execute to avoid leaving executable slots stuck.

	n.tryExecute(slot, seq)
}

func (n *Node) tryExecute(slot *consensusSlot, seq int64) {
	var executedMsg core.ClientMsg

	slot.mu.Lock()
	if slot.executed || slot.executionPending {
		slot.mu.Unlock()
		return
	}
	if slot.prePrepare == nil {
		slot.mu.Unlock()
		return
	}
	// Must be prepared: have PrePrepare + 2f matching prepares
	// if matchingVotes(cl.prepares, cl.digest) < 2*n.f {
	// 	return
	// }
	if slot.commitSent == false {
		slot.mu.Unlock()
		return
	}
	// Committed-local: 2f+1 matching commits
	if len(slot.commits) < 2*n.fNodes+1 || matchingVotesC(slot.commits, slot.prePrepare.DigestClientMsg) < 2*n.fNodes+1 {
		slot.mu.Unlock()
		return
	}

	slot.executionPending = true
	noOp := slot.prePrepare.DigestClientMsg == [32]byte{}
	missingData := slot.missingData

	if !noOp && !missingData {
		executedMsg = slot.prePrepare.ClientMsg.Data
	}

	if missingData && !noOp {
		n.log.Error("Executing with missing data for seq %d, view %d", seq, slot.prePrepare.View)
	}

	// digest := slot.prePrepare.DigestClientMsg
	slot.mu.Unlock()
	// n.log.Info("reached here")
	go n.sendCommitTps(executedMsg)

	go n.queueCommittedExecution(seq, slot, executedMsg, noOp, missingData)
}

func (n *Node) sendReply(clientMsg core.ClientMsg, result execution.Result, seq int64) {
	replyMsg := core.ReplyMessage{
		From:      n.GetAddr(),
		To:        config.ClientAddr,
		ClientMsg: clientMsg,
		Result: core.ExecutionResult{
			Success:        result.Success,
			Error:          result.Error,
			ExecutedSeqNum: seq,
		},
	}
	n.messageHub.Send(core.MsgReplyMessage, config.ClientAddr, replyMsg, nil)
}

func (n *Node) sendCommitTps(clientMsg core.ClientMsg) {
	commitTpsMsg := core.CommitTps{
		From:      n.GetAddr(),
		To:        config.ClientAddr,
		ClientMsg: clientMsg,
	}
	n.messageHub.Send(core.MsgCommitTpsMessage, config.ClientAddr, commitTpsMsg, nil)
}

func (n *Node) sendLeaderIdUpdate(newLeaderID int, view int64) {
	leaderUpdateMsg := core.LeaderIdUpdate{
		From:        n.GetAddr(),
		To:          config.ClientAddr,
		NewLeaderId: newLeaderID,
		View:        view,
	}
	n.messageHub.Send(core.MsgLeaderIdUpdateMessage, config.ClientAddr, leaderUpdateMsg, nil)
}

func (n *Node) sendVCRunningStatus(txns []core.ClientMsgSignature, vcRunning bool) {
	vcStatusMsg := core.VCRunningStatus{
		Txs:       txns,
		VCRunning: vcRunning,
	}
	n.messageHub.Send(core.MsgVCRunningStatusMessage, config.ClientAddr, vcStatusMsg, nil)
}

func makeClientRequestKey(msg core.ClientMsg) clientRequestKey {
	return clientRequestKey{
		clientName: msg.ClientName,
		id:         msg.Id,
	}
}

func (n *Node) primaryForView(forView int64, currView int64) int {
	if n.cfg == nil || n.cfg.NodeNum <= 0 || forView <= 0 {
		return 0
	}
	if n.cfg.ActiveL {
		if leaderID := n.primaryFromStableCheckpointVotes(); leaderID != 0 {
			return leaderID
		}
	}
	if n.vcType == core.VCTypeWRR {
		if leaderId := n.scoreboard.GetLeader(forView, currView); leaderId != 0 {

			return leaderId
		} else {
			n.log.Error("WRR enabled but no leader found in scoreboard for n.view %d (forView %d)", currView, forView)
		}

	}
	return int((forView-1)%n.cfg.NodeNum) + 1
}

func (n *Node) primaryFromStableCheckpointVotes() int {
	n.checkpointMu.Lock()
	defer n.checkpointMu.Unlock()

	checkpointData, exists := n.checkpoints[n.lastStableCheckpoint]
	if !exists || len(checkpointData.votes) == 0 {
		n.log.Warn("ActiveL enabled but no stable checkpoint votes found for seq=%d", n.lastStableCheckpoint.seq)
		return 0
	}

	voters := make([]int, 0, len(checkpointData.votes))
	for nodeID := range checkpointData.votes {
		voters = append(voters, nodeID)
	}
	sort.Ints(voters)

	if len(voters) != 2*n.fNodes+1 {
		n.log.Warn("ActiveL stable checkpoint voter count mismatch for seq=%d: got=%d want=%d voters=%v", n.lastStableCheckpoint.seq, len(voters), 2*n.fNodes+1, voters)
	}
	if voters[0] != 1 {
		n.log.Warn("ActiveL stable checkpoint lowest voter is not node 1 for seq=%d: got=%d voters=%v", n.lastStableCheckpoint.seq, voters[0], voters)
	}

	return voters[0]
}

func (n *Node) leaderForView(view int64) int {
	if view <= 0 {
		return 0
	}
	if leaderID, exists := n.leaderIdForView[view]; exists {
		return leaderID
	}
	if n.vcType == core.VCTypeRoundRobin {
		return n.primaryForView(view, -1)
	}
	return 0
}

func (n *Node) bufferConsensusMessage(msg bufferedConsensusMessage) {
	n.bufferedMsgsMu.Lock()
	n.bufferedMsgs = append(n.bufferedMsgs, msg)
	n.bufferedMsgsMu.Unlock()
}

func (n *Node) drainBufferedMessagesForView(view int64) []bufferedConsensusMessage {
	n.bufferedMsgsMu.Lock()
	defer n.bufferedMsgsMu.Unlock()

	if len(n.bufferedMsgs) == 0 {
		return nil
	}

	replay := make([]bufferedConsensusMessage, 0)
	remaining := n.bufferedMsgs[:0]
	for _, msg := range n.bufferedMsgs {
		if msg.view == view {
			replay = append(replay, msg)
			continue
		}
		remaining = append(remaining, msg)
	}
	if len(remaining) > 0 {
		n.log.Info("Still have %d buffered consensus messages for future views after draining for view %d", len(remaining), view)
	}
	n.bufferedMsgs = remaining
	return replay
}

func (n *Node) replayBufferedMessagesForView(view int64) {
	buffered := n.drainBufferedMessagesForView(view)
	if len(buffered) == 0 {
		n.log.Info("No buffered consensus messages to replay for view %d", view)
		return
	}

	n.log.Info("Replaying %d buffered consensus messages for view %d", len(buffered), view)
	for _, msg := range buffered {
		switch msg.kind {
		case bufferedPrePrepare: //maybe async them
			go n.HandlePrePrepare(msg.preprepare, msg.signature)
		case bufferedPrepare:
			go n.HandlePrepare(msg.prepare, msg.signature)
		case bufferedCommit:
			go n.HandleCommit(msg.commit)
		}
	}
}

func (n *Node) verifyPreprepare(preprepareMsg core.PreprepareMsgSig) (bool, int64, int64, [32]byte) {
	view := preprepareMsg.PreprepareMsgMini.View
	seq := preprepareMsg.PreprepareMsgMini.SeqNum
	digest := preprepareMsg.PreprepareMsgMini.DigestClientMsg

	from := n.leaderForView(view)
	if from == 0 {
		n.log.Error("leader not found for preprepare verification: view=%d", view)
		return false, 0, 0, [32]byte{}
	}

	senderPubKey, exists := n.encryptionKeyStore.GetPublicKey(from)
	if !exists {
		n.log.Error("public key not found for preprepare sender node ID: %d", from)
		return false, 0, 0, [32]byte{}
	}
	payload := preprepareSignPayload(preprepareMsg.PreprepareMsgMini.View, preprepareMsg.PreprepareMsgMini.SeqNum, preprepareMsg.PreprepareMsgMini.DigestClientMsg[:])
	// payload := &transportpb.PreprepareSignPayload{
	// 	View:            view,
	// 	SeqNum:          seq,
	// 	DigestClientMsg: digest[:],
	// }
	payloadBytes, err := marshalDeterministic(payload)
	if err != nil {
		n.log.Error("preprepare payload marshal failed: err=%v", err)
		return false, 0, 0, [32]byte{}
	}

	if !crypto.VerifySignatureEd25519(payloadBytes, preprepareMsg.Signature, senderPubKey) {
		n.log.Error("preprepare signature verification failed for node ID: %d", from)
		return false, 0, 0, [32]byte{}
	}
	return true, view, seq, digest
}

func (n *Node) verifyPrepareLog(prepareLog map[int]*core.PrepareMsgSig, view, seq int64, digest [32]byte) bool {
	required := 2 * n.fNodes
	if required <= 0 {
		return true
	}
	if len(prepareLog) < required {
		return false
	}

	validCount := 0
	for from, prepareMsgSig := range prepareLog {
		if prepareMsgSig == nil {
			continue
		}

		prepareMsg := prepareMsgSig.PrepareMsg
		if prepareMsg.View != view || prepareMsg.SeqNum != seq || prepareMsg.Digest != digest {
			continue
		}
		if prepareMsg.From != from {
			n.log.Error("prepare sender mismatch: map key=%d payload from=%d", from, prepareMsg.From)
			continue
		}

		senderPubKey, exists := n.encryptionKeyStore.GetPublicKey(from)
		if !exists {
			n.log.Error("public key not found for prepare sender node ID: %d", from)
			continue
		}

		payloadBytes, err := marshalDeterministic(transportpb.PrepareToPB(prepareMsg))
		if err != nil {
			n.log.Error("prepare payload marshal failed: err=%v", err)
			continue
		}
		if !crypto.VerifySignatureEd25519(payloadBytes, prepareMsgSig.Signature, senderPubKey) {
			n.log.Error("prepare signature verification failed for node ID: %d", from)
			continue
		}

		validCount++
		if validCount >= required {
			return true
		}
	}

	n.log.Error("not enough valid prepare messages: valid=%d required=%d view=%d seq=%d", validCount, required, view, seq)
	return false
}

func (n *Node) verifyPreparedCerts(preparedCerts map[int64]*core.PreparedCert) bool {
	for certSeq, cert := range preparedCerts {
		if cert == nil {
			return false
		}
		ok, view, seq, digest := n.verifyPreprepare(cert.PreprepareMsg)
		if !ok {
			return false
		}
		if seq != certSeq {
			n.log.Error("prepared cert seq mismatch: map key=%d preprepare seq=%d", certSeq, seq)
			return false
		}
		if !n.verifyPrepareLog(cert.PrepareLog, view, seq, digest) {
			return false
		}
	}
	return true
}

func (n *Node) verifyVC(vc core.ViewChangeMsg) bool {
	verifiedPreparedCerts := n.verifyPreparedCerts(vc.PreparedCerts)
	// Additional checks can be added here, such as verifying the signature of the ViewChangeMsg itself.
	return verifiedPreparedCerts
}

func (n *Node) verifyVoteLog(voteLog []int) bool {
	required := 2*n.fNodes + 1
	if len(voteLog) == required {
		return true
	} else {
		return false
	}

	// seenFrom := make(map[int]struct{}, len(voteLog))
	// for _, vote := range voteLog {
	// 	seenFrom[vote.GrantVoteMsg.From] = struct{}{}
	// 	if len(seenFrom) >= required {
	// 		return true
	// 	}
	// }

	// return false
}

func (n *Node) duplicateCheckVC(vcMsgSigs []*core.ViewChangeMsgSig) bool {
	required := 2*n.fNodes + 1
	if len(vcMsgSigs) == required {
		return true
	} else {
		return false
	}

	// seenFrom := make(map[int]struct{}, len(vcMsgSigs))
	// for _, vcMsgSig := range vcMsgSigs {
	// 	if vcMsgSig == nil {
	// 		continue
	// 	}
	// 	seenFrom[vcMsgSig.ViewChangeMsg.From] = struct{}{}
	// 	if len(seenFrom) >= required {
	// 		return true
	// 	}
	// }

	// return false
}

func (n *Node) HandleViewChange(viewChange core.ViewChangeMsg, signature []byte) {
	n.log.Test("Received ViewChange for view %d from node %d", viewChange.ViewNumber, viewChange.From)
	if n.vcType == core.VCTypeElection {
		n.log.Info("Election Path received view change")
		n.HandleViewChangeElection(viewChange, signature)
	} else if n.vcType == core.VCTypeRoundRobin {
		n.log.Info("Round Robin Path received view change")
		n.HandleViewChangeRoundRobin(viewChange, signature)
	} else if n.vcType == core.VCTypeWRR {
		n.log.Info("WRR Path received view change")
		n.HandleViewChangeWRR(viewChange, signature)
	}

}

// func (n *Node) HandleGrantVote(grantVote core.GrantVoteMsg, signature []byte) {
// 	n.viewMu.Lock()
// 	defer n.viewMu.Unlock()

// 	if grantVote.ViewNumber != n.forView || n.votedFor != n.GetNodeID() {
// 		n.log.Error("Received grant vote for view %d but my for view is %d and my node ID is %d", grantVote.ViewNumber, n.forView, n.GetNodeID())
// 		return
// 	}

// 	if _, exists := n.voteLog[grantVote.ViewNumber]; !exists {
// 		n.voteLog[grantVote.ViewNumber] = make([]core.GrantVoteMsgSig, 0)
// 		n.log.Error("vote log should exists for view %d, this should not happen in grant vote handler", grantVote.ViewNumber)
// 	}

// 	n.voteLog[grantVote.ViewNumber] = append(n.voteLog[grantVote.ViewNumber], core.GrantVoteMsgSig{
// 		GrantVoteMsg: grantVote,
// 		Signature:    append([]byte(nil), signature...),
// 	})

// 	verfiedVoteLog := n.verifyVoteLog(n.voteLog[grantVote.ViewNumber])
// 	if verfiedVoteLog {
// 		duplicateCheckVC := n.duplicateCheckVC(n.viewChangeMsgsLog[n.forView])
// 		if duplicateCheckVC {
// 			n.log.Info("New view from grant vote")
// 			n.newView()
// 		}
// 	}
// }

func (n *Node) verifyNewView(newViewMsg core.NewViewMsg) bool {
	seenFrom := make(map[int]struct{}, len(newViewMsg.ViewChangeLog))
	viewChangeMsgsCached := n.viewChangeMsgsLog[newViewMsg.NewViewNumber]
	for _, vcMsgSig := range newViewMsg.ViewChangeLog {
		if vcMsgSig == nil {
			n.log.Error("nil view change message in new view message log for view %d", newViewMsg.NewViewNumber)
			return false
		}
		if _, exists := seenFrom[vcMsgSig.ViewChangeMsg.From]; exists {
			continue
		}

		foundInCache := false
		for _, cachedMsg := range viewChangeMsgsCached {
			if cachedMsg == nil {
				continue
			}
			if cachedMsg.ViewChangeMsg.From == vcMsgSig.ViewChangeMsg.From {
				cachedPayloadBytes, err := marshalDeterministic(transportpb.ViewChangeToPB(cachedMsg.ViewChangeMsg))
				if err != nil {
					n.log.Error("failed to marshal cached view change message for view %d from node %d: %v", newViewMsg.NewViewNumber, cachedMsg.ViewChangeMsg.From, err)
					continue
				}
				incomingPayloadBytes, err := marshalDeterministic(transportpb.ViewChangeToPB(vcMsgSig.ViewChangeMsg))
				if err != nil {
					n.log.Error("failed to marshal incoming view change message for view %d from node %d: %v", newViewMsg.NewViewNumber, vcMsgSig.ViewChangeMsg.From, err)
					continue
				}
				if !bytes.Equal(cachedPayloadBytes, incomingPayloadBytes) {
					n.log.Error("cached view change payload mismatch for view %d from node %d", newViewMsg.NewViewNumber, vcMsgSig.ViewChangeMsg.From)
					continue
				}
				foundInCache = true
				break
			}
		}
		if !foundInCache {
			payloadBytes, err := marshalDeterministic(transportpb.ViewChangeToPB(vcMsgSig.ViewChangeMsg))
			if err != nil {
				n.log.Error("failed to marshal view change message for view %d from node %d: %v", newViewMsg.NewViewNumber, vcMsgSig.ViewChangeMsg.From, err)
				continue
			}
			senderPubKey, exists := n.encryptionKeyStore.GetPublicKey(vcMsgSig.ViewChangeMsg.From)
			if !exists {
				n.log.Error("public key not found for view change sender node ID: %d", vcMsgSig.ViewChangeMsg.From)
				continue
			}
			if !crypto.VerifySignatureEd25519(payloadBytes, vcMsgSig.Signature, senderPubKey) {
				n.log.Error("signature verification failed for view change message for view %d from node %d", newViewMsg.NewViewNumber, vcMsgSig.ViewChangeMsg.From)
				continue
			}
			n.log.Info("verifying VC in new newview")
			verifiedVC := n.verifyVC(vcMsgSig.ViewChangeMsg)
			if verifiedVC {
				seenFrom[vcMsgSig.ViewChangeMsg.From] = struct{}{}
			}
		} else {
			seenFrom[vcMsgSig.ViewChangeMsg.From] = struct{}{}
		}
	}

	if len(seenFrom) >= 2*n.fNodes+1 {
		return true
	} else {
		n.log.Error("not enough unique view change messages in new view message log for view %d: unique=%d required=%d", newViewMsg.NewViewNumber, len(seenFrom), 2*n.fNodes+1)
		return false
	}
}

func verifyOSet(Ocreated map[int64]core.PreprepareMsgSig, Oreceived []core.PreprepareMsgSig) bool {
	for _, preprepareMsgSig := range Oreceived {
		if o, exists := Ocreated[preprepareMsgSig.PreprepareMsgMini.SeqNum]; !exists {
			return false
		} else {
			if o.PreprepareMsgMini.View != preprepareMsgSig.PreprepareMsgMini.View ||
				o.PreprepareMsgMini.SeqNum != preprepareMsgSig.PreprepareMsgMini.SeqNum ||
				o.PreprepareMsgMini.DigestClientMsg != preprepareMsgSig.PreprepareMsgMini.DigestClientMsg {
				return false
			}
		}
	}
	return true
}

func (n *Node) HandleNewView(newViewMsg core.NewViewMsg, _ []byte) {
	n.viewMu.Lock()
	n.log.Info("Received new view message for view %d from leader %d and my current view is %d and my for view is %d", newViewMsg.NewViewNumber, newViewMsg.From, n.view, n.forView)

	if newViewMsg.NewViewNumber < n.view {
		n.viewMu.Unlock()
		return
	}
	if newViewMsg.NewViewNumber < n.forView {
		n.log.Error("Received new view message for view %d which is less than my for view %d, ignoring", newViewMsg.NewViewNumber, n.forView)
		// return
	}
	// n.pbftTimerManager.stopNewViewTimer()
	// return
	verifiedNewView := n.verifyNewView(newViewMsg)
	if !verifiedNewView {
		n.viewMu.Unlock()
		return
	}
	Oset, maxSeq := n.createOReplica(newViewMsg.ViewChangeLog, newViewMsg.NewViewNumber)

	verifiedOsets := verifyOSet(Oset, newViewMsg.PreprepareLog)
	if !verifiedOsets {
		n.log.Error("O set verification failed for new view message for view %d", newViewMsg.NewViewNumber)
		n.viewMu.Unlock()
		return
	}
	n.preprepareSeqNumber.Store(maxSeq)
	n.periodInterval = maxSeq + n.cfg.Period
	n.log.Info("Next Period end is %d", n.periodInterval)
	oldView := n.view
	n.view = newViewMsg.NewViewNumber
	n.forView = newViewMsg.NewViewNumber
	n.leaderId = newViewMsg.From
	n.leaderIdForView[newViewMsg.NewViewNumber] = newViewMsg.From
	n.viewChangeRunning = false
	n.pbftTimerManager.stopNewViewTimer()
	n.pbftTimerManager.stopPeriodicElectionTimer()
	if n.cfg.Performance {
		// n.executionMu.Lock()
		// lastexe := n.lastExecuted //locking check
		// n.executionMu.Unlock()
		n.throughputMu.Lock()
		// n.throughputIntervalStart = time.Now()
		n.throughputIntervalStartSeq = maxSeq + THROUGHPUTINTERVAL_DELAY
		n.log.Info("Throughput interval start seq set to %d for new view %d", n.throughputIntervalStartSeq, n.view)
		n.throughputObservationStarted = false
		// maxRecentThroughput := n.maxRecentViewFinalThroughputLocked(n.view)
		maxRecentThroughput := newViewMsg.Throughput
		n.targetThroughput = targetThroughputMaxFactor * maxRecentThroughput
		n.log.Info("Length of preprepare log in new view message for view %d is %d and max seq number is %d ", n.view, len(newViewMsg.PreprepareLog), maxSeq)
		n.log.Info("Max recent throughput for new view %d is %.2f; target throughput set to %.2f", n.view, maxRecentThroughput, n.targetThroughput)
		n.throughputMu.Unlock()

	}
	if n.vcType == core.VCTypeWRR {
		n.log.Info("Scoreboard before update oldview %d new view %d", oldView, n.view)
		n.log.Info("%s", n.scoreboard.String())
		rleaderId := n.scoreboard.UpdatePriorities(n.view, oldView)
		n.log.Info("Returned leaderid from UpdatePriorities is %d and my id is %d", rleaderId, n.GetNodeID())
		throughputs := n.ThroughputListFromVC(newViewMsg.ViewChangeLog)
		if throughputs == nil || len(throughputs) == 0 {
			n.log.Error("Throughputs missing for WRR scoreboard update at new view %d", n.view)
		}
		leaderId, exists := n.leaderIdForView[oldView]
		if !exists {
			n.log.Error("Old leader ID missing for WRR scoreboard update at new view %d", n.view)
		}

		score, err := n.scoreboard.UpdateScore(leaderId, throughputs, ALPHA, D)
		if err != nil {
			n.log.Error("Failed to update scoreboard for node %d: %v", leaderId, err)
		}
		n.log.Info("Updated score for leader %d is %d for old view %d", leaderId, score, oldView)
		n.scoreboard.Update(oldView, score, leaderId)
		n.log.Info("Scoreboard after update oldview %d new view %d", oldView, n.view)
		n.log.Info("%s", n.scoreboard.String())

	}

	n.log.Info("Transitioned to new view %d with leader %d", n.view, n.leaderId)
	needSyncLog := make([]*core.PreprepareMsgSig, len(newViewMsg.PreprepareLog))
	for _, preprepareMsg := range newViewMsg.PreprepareLog {
		needSync := n.HandlePrePrepareNewView(preprepareMsg.PreprepareMsgMini, preprepareMsg.Signature, preprepareMsg.ActualMsg)
		if needSync {
			needSyncLog = append(needSyncLog, &preprepareMsg)
		}

	}
	replayView := n.view
	n.viewMu.Unlock()
	go n.sendLeaderIdUpdate(n.leaderId, n.view)
	n.pbftTimerManager.forceResetPBFTTimer()
	n.replayBufferedMessagesForView(replayView)
	go n.gcViewChangeMsgs(replayView)

}

func (n *Node) createO(vcMsgSigs []*core.ViewChangeMsgSig, view int64, oldView int64) ([]core.PreprepareMsgSig, int64) {
	O := make([]core.PreprepareMsgSig, 0)
	preprepareLog := make(map[int64]core.PreprepareMsgSig)
	n.checkpointMu.Lock()
	// minS := n.lastStableCheckpoint.seq + 1
	minS := vcMsgSigs[0].ViewChangeMsg.CheckpointSeqNumber + 1
	myStableCheckpoint := n.lastStableCheckpoint
	latestStableCheckpoint := checkpoint{
		seq:    vcMsgSigs[0].ViewChangeMsg.CheckpointSeqNumber,
		digest: vcMsgSigs[0].ViewChangeMsg.CheckpointDigest,
	}
	checkpointProof := []core.CheckpointMsgSig{}
	// checkpointNeedsSync := false
	for _, vcMsgSig := range vcMsgSigs {
		if vcMsgSig.ViewChangeMsg.CheckpointSeqNumber > latestStableCheckpoint.seq {
			// n.log.Error("missing the latest stable checkpoint at o primary") // would need to pass digest and application state in vc message for sync
			// n.lastStableCheckpoint = checkpoint{
			// 	seq: vcMsgSig.ViewChangeMsg.CheckpointSeqNumber,
			// }
			latestStableCheckpoint = checkpoint{
				seq:    vcMsgSig.ViewChangeMsg.CheckpointSeqNumber,
				digest: vcMsgSig.ViewChangeMsg.CheckpointDigest,
			}
			checkpointProof = vcMsgSig.ViewChangeMsg.CheckpointProof
			minS = latestStableCheckpoint.seq + 1

		}
	}

	if latestStableCheckpoint.seq > myStableCheckpoint.seq {
		n.log.Error("missing the latest stable checkpoint at o primary")
		checkpointData, exists := n.checkpoints[latestStableCheckpoint]
		if !exists {
			checkpointData = CheckpointData{
				votes:    make(map[int]core.CheckpointMsgSig),
				balances: nil,
			}
			n.checkpoints[latestStableCheckpoint] = checkpointData

		} else if exists && len(checkpointData.votes) < 2*n.fNodes+1 { // this path is mainly when i have executed and have balances but need more votes to stabalize
			for _, checkpointMsgSig := range checkpointProof {
				n.checkpoints[latestStableCheckpoint].votes[checkpointMsgSig.CheckpointMsg.From] = checkpointMsgSig
			} // if not exists then copy proof, if exists may replace some with incoming we will verify checkpoint before when receive vc
			if checkpointData.balances == nil { // most likely this case not run
				n.log.Info("requesting state transfer from creat 0 primary")
				go n.RequestStateTransfer(latestStableCheckpoint.seq, latestStableCheckpoint.digest)
			} else {
				n.lastStableCheckpoint = latestStableCheckpoint // unsafe checkpoint forwarding
				go n.gcConsensusState(latestStableCheckpoint.seq)
				go n.gcCheckpoints(latestStableCheckpoint)
			}
		} else {
			// if votess >= then from cehckpoint receive path should have called state transfer
			n.log.Error("should not come here for checkpoint in create O at primary")
		}

	}
	n.checkpointMu.Unlock()
	n.executionMu.Lock()
	if latestStableCheckpoint.seq > n.lastExecuted {
		// n.lastExecuted = latestStableCheckpoint.seq // unsafe checkpoint forwarding
		// n.log.Error("updating last executed to stable checkpoint seq %d", n.lastExecuted)

	} else if latestStableCheckpoint.seq < n.lastExecuted {
		n.log.Error("my stable checkpoint seq %d is less than my last executed %d, this should not happen", latestStableCheckpoint.seq, n.lastExecuted)
	}
	n.executionMu.Unlock()
	maxS := minS - 1
	for _, viewChangeMsg := range vcMsgSigs {
		for seqNumber, pm := range viewChangeMsg.ViewChangeMsg.PreparedCerts {
			if seqNumber > maxS {
				maxS = seqNumber

			}
			if pm.PreprepareMsg.PreprepareMsgMini.View < oldView {
				n.log.Error("preprepare message has an older view number createO at Primary")
			}
			preprepareLog[seqNumber] = pm.PreprepareMsg

		}
	}

	if maxS < minS {
		n.log.Error("no suffix at o primary")
		return O, minS - 1
	}
	for seq := minS; seq <= maxS; seq++ {
		if preprepare, exists := preprepareLog[seq]; exists {
			preprepare.PreprepareMsgMini.View = view
			// pbMsg := transportpb.PreprepareMiniToPB2(preprepare.PreprepareMsgMini)
			payloadBytes, err := marshalDeterministic(preprepareSignPayload(preprepare.PreprepareMsgMini.View, preprepare.PreprepareMsgMini.SeqNum, preprepare.PreprepareMsgMini.DigestClientMsg[:]))
			if err != nil {
				// handle error, maybe skip this preprepare
				continue
			}
			signature := crypto.SignMessageEd25519(payloadBytes, n.encryptionKeyStore.GetPrivateKey())
			O = append(O, core.PreprepareMsgSig{
				PreprepareMsgMini: preprepare.PreprepareMsgMini,
				Signature:         signature,
				ActualMsg:         preprepare.ActualMsg,
			})

		} else {
			n.log.Info("No prepared cert for seq %d in view change messages, creating dummy preprepare the min and max seq are %d and %d", seq, minS, maxS)
			dummyPreprepare := core.PreprepareMsgMini{
				View:            view,
				SeqNum:          seq,
				DigestClientMsg: [32]byte{},
			}
			// pbMsg := transportpb.PreprepareMiniToPB2(dummyPreprepare)
			payloadBytes, err := marshalDeterministic(preprepareSignPayload(dummyPreprepare.View, dummyPreprepare.SeqNum, dummyPreprepare.DigestClientMsg[:]))
			if err != nil {
				// handle error, maybe skip this preprepare
				continue
			}
			signature := crypto.SignMessageEd25519(payloadBytes, n.encryptionKeyStore.GetPrivateKey())
			O = append(O, core.PreprepareMsgSig{
				PreprepareMsgMini: dummyPreprepare,
				Signature:         signature,
			})
		}
	}
	return O, maxS

}

func (n *Node) createOReplica(vcMsgSigs []*core.ViewChangeMsgSig, view int64) (map[int64]core.PreprepareMsgSig, int64) {

	preprepareLog := make(map[int64]core.PreprepareMsgSig)
	n.checkpointMu.Lock()
	// minS := n.lastStableCheckpoint.seq + 1
	minS := vcMsgSigs[0].ViewChangeMsg.CheckpointSeqNumber + 1
	myStableCheckpoint := n.lastStableCheckpoint
	latestStableCheckpoint := checkpoint{
		seq:    vcMsgSigs[0].ViewChangeMsg.CheckpointSeqNumber,
		digest: vcMsgSigs[0].ViewChangeMsg.CheckpointDigest,
	}
	checkpointProof := []core.CheckpointMsgSig{}
	// checkpointNeedsSync := false
	for _, vcMsgSig := range vcMsgSigs {
		if vcMsgSig.ViewChangeMsg.CheckpointSeqNumber > latestStableCheckpoint.seq {
			// n.log.Error("missing the latest stable checkpoint at o replica") // would need to pass digest and application state in vc message for sync
			// n.lastStableCheckpoint = checkpoint{
			// 	seq: vcMsgSig.ViewChangeMsg.CheckpointSeqNumber,
			// }
			latestStableCheckpoint = checkpoint{
				seq:    vcMsgSig.ViewChangeMsg.CheckpointSeqNumber,
				digest: vcMsgSig.ViewChangeMsg.CheckpointDigest,
			}
			checkpointProof = vcMsgSig.ViewChangeMsg.CheckpointProof
			minS = latestStableCheckpoint.seq + 1

		}
	}

	if latestStableCheckpoint.seq > myStableCheckpoint.seq {
		n.log.Error("missing the latest stable checkpoint at o replica")
		checkpointData, exists := n.checkpoints[latestStableCheckpoint]
		if !exists {
			checkpointData = CheckpointData{
				votes:    make(map[int]core.CheckpointMsgSig),
				balances: nil,
			}
			n.checkpoints[latestStableCheckpoint] = checkpointData

		} else if exists && len(checkpointData.votes) < 2*n.fNodes+1 { // this path is mainly when i have executed and have balances but need more votes to stabalize
			for _, checkpointMsgSig := range checkpointProof {
				n.checkpoints[latestStableCheckpoint].votes[checkpointMsgSig.CheckpointMsg.From] = checkpointMsgSig
			} // if not exists then copy proof, if exists may replace some with incoming we will verify checkpoint before when receive vc
			if checkpointData.balances == nil { // most likely this case not run
				n.log.Info("requesting state transfer from creat 0 replica")
				go n.RequestStateTransfer(latestStableCheckpoint.seq, latestStableCheckpoint.digest)
			} else {
				n.lastStableCheckpoint = latestStableCheckpoint // unsafe checkpoint forwarding
				go n.gcConsensusState(latestStableCheckpoint.seq)
				go n.gcCheckpoints(latestStableCheckpoint)
			}
		} else {
			// if votess >= then from cehckpoint receive path should have called state transfer
			n.log.Error("should not come here for checkpoint in create O at replica")
		}

	}
	n.checkpointMu.Unlock()

	n.executionMu.Lock()
	if latestStableCheckpoint.seq > n.lastExecuted {
		// n.lastExecuted = latestStableCheckpoint.seq // unsafe checkpoint forwarding
		// n.log.Error("updating last executed to stable checkpoint seq %d", n.lastExecuted)

	} else if latestStableCheckpoint.seq < n.lastExecuted {
		n.log.Error("my stable checkpoint seq %d is less than my last executed %d, this should not happen", latestStableCheckpoint.seq, n.lastExecuted)
	}
	n.executionMu.Unlock()
	maxS := minS - 1
	// maxS := minS
	for _, viewChangeMsg := range vcMsgSigs {
		for seqNumber, pm := range viewChangeMsg.ViewChangeMsg.PreparedCerts {
			if seqNumber > maxS {
				maxS = seqNumber

			}
			preprepareLog[seqNumber] = pm.PreprepareMsg
			if pm.PreprepareMsg.PreprepareMsgMini.View < n.view {
				n.log.Error("preprepare message has an older view number createOReplica at o replica")
			}

		}
	}

	if maxS < minS {
		n.log.Error("no suffix o at replica")
		return preprepareLog, minS - 1
	}
	for seq := minS; seq <= maxS; seq++ {
		if preprepare, exists := preprepareLog[seq]; exists {
			preprepare.PreprepareMsgMini.View = view
			// pbMsg := transportpb.PreprepareMiniToPB2(preprepare.PreprepareMsgMini)
			// payloadBytes, err := marshalDeterministic(preprepareSignPayload(pbMsg))
			// if err != nil {
			// 	// handle error, maybe skip this preprepare
			// 	continue
			// }
			// signature := crypto.SignMessageEd25519(payloadBytes, n.encryptionKeyStore.GetPrivateKey())
			preprepareLog[seq] = preprepare

		} else {
			dummyPreprepare := core.PreprepareMsgMini{
				View:            view,
				SeqNum:          seq,
				DigestClientMsg: [32]byte{},
			}
			// pbMsg := transportpb.PreprepareMiniToPB2(dummyPreprepare)
			// payloadBytes, err := marshalDeterministic(preprepareSignPayload(pbMsg))
			// if err != nil {
			// 	// handle error, maybe skip this preprepare
			// 	continue
			// }
			// signature := crypto.SignMessageEd25519(payloadBytes, n.encryptionKeyStore.GetPrivateKey())
			preprepareLog[seq] = core.PreprepareMsgSig{
				PreprepareMsgMini: dummyPreprepare,
				Signature:         nil, // dummy preprepare doesn't need signature for replica verification
			}
		}
	}
	return preprepareLog, maxS

}

func (n *Node) broadcastNewView(newViewMsg core.NewViewMsg, signature []byte) {
	for _, othersIp := range config.NodeAddr {
		if othersIp == n.GetAddr() {
			continue
		}
		n.messageHub.Send(core.MsgNewViewMessage, othersIp, newViewMsg, signature)
	}
}

func (n *Node) newView() {
	// if n.dead {
	// 	n.log.Info("Node is dead, not transitioning to new view")
	// 	return
	// } else {
	// 	if n.scoreboard.scores[n.GetNodeID()] >= 17 {
	// 		n.log.Info("Node %d has score %d which is above threshold, not transitioning to new view", n.GetNodeID(), n.scoreboard.scores[n.GetNodeID()])
	// 		n.dead = true
	// 		return
	// 	}
	// }
	oldView := n.view
	n.view = n.forView
	n.leaderId = n.GetNodeID()
	n.leaderIdForView[n.view] = n.leaderId
	n.viewChangeRunning = false
	n.pbftTimerManager.stopNewViewTimer()
	n.pbftTimerManager.stopPeriodicElectionTimer()
	n.log.Info("Became leader for new view %d and my id is %d", n.view, n.GetNodeID())

	O, maxSeq := n.createO(n.viewChangeMsgsLog[n.view], n.view, oldView)

	n.preprepareSeqNumber.Store(maxSeq)
	n.periodInterval = maxSeq + n.cfg.Period
	n.log.Info("Next Period end is %d", n.periodInterval)
	maxRecentThroughput := 0.0
	if n.cfg.Performance {
		// n.executionMu.Lock()
		// lastexe := n.lastExecuted //locking check
		// n.executionMu.Unlock()
		n.throughputMu.Lock()
		// n.throughputIntervalStart = time.Now().Add(30 * time.Millisecond)
		n.throughputIntervalStartSeq = maxSeq + THROUGHPUTINTERVAL_DELAY
		n.log.Info("Throughput interval start seq set to %d for new view %d", n.throughputIntervalStartSeq, n.view)
		n.throughputObservationStarted = false
		maxRecentThroughput = n.maxRecentViewFinalThroughputLocked(n.view)
		n.targetThroughput = targetThroughputMaxFactor * maxRecentThroughput
		n.log.Info("Created O with maxSeq %d for new view %d at primary and len O is %d", maxSeq, n.view, len(O))
		n.log.Info("Max recent throughput for new view %d is %.2f; target throughput set to %.2f", n.view, maxRecentThroughput, n.targetThroughput)
		n.throughputMu.Unlock()
	}
	if n.vcType == core.VCTypeWRR {
		n.log.Info("Scoreboard before update oldview %d new view %d", oldView, n.view)
		n.log.Info("%s", n.scoreboard.String())
		rleaderId := n.scoreboard.UpdatePriorities(n.view, oldView)
		n.log.Info("Returned leaderid from UpdatePriorities is %d and my id is %d", rleaderId, n.GetNodeID())
		throughputs := n.ThroughputListFromVC(n.viewChangeMsgsLog[n.view])
		if throughputs == nil || len(throughputs) == 0 {
			n.log.Error("Throughputs missing for WRR scoreboard update at new view %d", n.view)
		}
		leaderId, exists := n.leaderIdForView[oldView]
		if !exists {
			n.log.Error("Old leader ID missing for WRR scoreboard update at new view %d", n.view)
		}

		score, err := n.scoreboard.UpdateScore(leaderId, throughputs, ALPHA, D)
		if err != nil {
			n.log.Error("Failed to update scoreboard for node %d: %v", leaderId, err)
		}
		n.log.Info("Updated score for leader %d is %d for old view %d", leaderId, score, oldView)
		n.scoreboard.Update(oldView, score, leaderId)
		n.log.Info("Scoreboard after update oldview %d new view %d", oldView, n.view)
		n.log.Info("%s", n.scoreboard.String())

	}

	for _, preprepareMsg := range O {
		slot := n.consensusLog.getOrCreateLog(preprepareMsg.PreprepareMsgMini.SeqNum, preprepareMsg.PreprepareMsgMini.View)
		slot.mu.Lock()
		if slot.prePrepare == nil {
			slot.prePrepare = &core.PreprepareMsg{
				View:            preprepareMsg.PreprepareMsgMini.View,
				SeqNum:          preprepareMsg.PreprepareMsgMini.SeqNum,
				DigestClientMsg: preprepareMsg.PreprepareMsgMini.DigestClientMsg,
			}
		} else {
			n.log.Error("Should be nil primary")
		}
		// slot.prePrepare.SeqNum = preprepareMsg.PreprepareMsgMini.SeqNum
		// slot.prePrepare.View = preprepareMsg.PreprepareMsgMini.View
		slot.prePrepareSig = preprepareMsg.Signature
		// slot.prePrepare.DigestClientMsg = preprepareMsg.PreprepareMsgMini.DigestClientMsg
		if preprepareMsg.PreprepareMsgMini.DigestClientMsg != [32]byte{} {
			clientMsg, exists, executed := n.pool.Get(preprepareMsg.PreprepareMsgMini.DigestClientMsg)
			if exists {
				slot.prePrepare.ClientMsg = clientMsg
				slot.missingData = false
			} else if !exists && !executed {
				// n.log.Error("Primary missing slot at new view for seq %d", preprepareMsg.PreprepareMsgMini.SeqNum)
				if preprepareMsg.ActualMsg.Data.Txn == nil {
					n.log.Error("Primary missing slot at new view for seq %d and actual msg is nil", preprepareMsg.PreprepareMsgMini.SeqNum)
				} else {
					n.log.Error("Primary missing slot but received txn from %s and to %s ", preprepareMsg.ActualMsg.Data.Txn.Sender, preprepareMsg.ActualMsg.Data.Txn.Receiver)
				}
				slot.missingData = true
			} else if !exists && executed {
				slot.missingData = false
			}
			// fail safe for
			slot.prePrepare.ClientMsg = preprepareMsg.ActualMsg
			slot.missingData = false
		}
		slot.mu.Unlock()

	}

	// if n.split {
	// 	n.log.Info("Node in split got votes")
	// } else {
	// 	n.log.Info("Node not in split got votes")
	// }

	newViewMsg := core.NewViewMsg{
		NewViewNumber: n.view,
		From:          n.GetNodeID(),
		PreprepareLog: O,
		ViewChangeLog: n.viewChangeMsgsLog[n.view],
		Throughput:    maxRecentThroughput,
	}
	pbMsg := transportpb.NewViewToPB(newViewMsg)
	payloadBytes, err := marshalDeterministic(pbMsg)
	if err != nil {
		n.log.Error("Failed to marshal NewView message for signing: %v", err)
		return
	}
	signature := crypto.SignMessageEd25519(payloadBytes, n.encryptionKeyStore.GetPrivateKey())
	// should strart pbft timer
	if n.GetNodeID() == 3 {
		n.broadcastNewView(newViewMsg, signature)
	} else {
		n.broadcastNewView(newViewMsg, signature)
	}

	n.log.Info("Entering replay from primary might not need at primary")
	go n.replayBufferedMessagesForView(n.view)
	go n.gcViewChangeMsgs(n.view)

}

func (n *Node) handleViewChangeTimeoutDummy() {

	n.viewChangeRunning = true

	n.forView = n.forView + 1
	if n.vcType == core.VCTypeElection {
		n.log.Info("Election Path View Change Timeout")

		n.electionVCTimeout()
	} else if n.vcType == core.VCTypeRoundRobin {
		n.roundRobinVCTimeout()
	} else if n.vcType == core.VCTypeWRR {
		n.WRRVCTimeout()
	}

}

func (n *Node) createVCContent(stableCheckpointSeq int64) map[int64]*core.PreparedCert {
	preparedCerts := make(map[int64]*core.PreparedCert)
	// n.checkpointMu.Lock()
	// stableCheckpointSeq := n.lastStableCheckpoint.seq
	// n.checkpointMu.Unlock()
	n.consensusLog.slotsMu.RLock()
	// n.log.Info("inside create vc")
	// we go over slots after stable checkpoint
	for _, slot := range n.consensusLog.slots {

		slot.mu.Lock()
		if slot.prePrepare == nil || !slot.commitSent || slot.prePrepare.SeqNum <= stableCheckpointSeq {
			slot.mu.Unlock()
			continue
		}
		if slot.prePrepare.View < n.view {
			n.log.Error("Sending a prepare for a view lower than n.view where my n.view is %d and prepare is for view %d and checkpoint is %d", n.view, slot.prePrepare.View, stableCheckpointSeq)
		}
		seqNum := slot.prePrepare.SeqNum
		preprepareV := core.PreprepareMsgSig{

			PreprepareMsgMini: core.PreprepareMsgMini{
				View:            slot.prePrepare.View,
				SeqNum:          seqNum,
				DigestClientMsg: slot.prePrepare.DigestClientMsg,
			},
			Signature: append([]byte(nil), slot.prePrepareSig...),
			ActualMsg: slot.prePrepare.ClientMsg,
		}

		prepareLog := make(map[int]*core.PrepareMsgSig, len(slot.prepares))
		for from, prepare := range slot.prepares {
			if prepare == nil {
				prepareLog[from] = nil
				continue
			}
			prepareCopy := *prepare
			prepareCopy.Signature = append([]byte(nil), prepare.Signature...)
			prepareLog[from] = &prepareCopy
		}
		slot.mu.Unlock()

		preparedCerts[seqNum] = &core.PreparedCert{
			PreprepareMsg: preprepareV,
			PrepareLog:    prepareLog,
		}

	}
	n.consensusLog.slotsMu.RUnlock()
	return preparedCerts
}
