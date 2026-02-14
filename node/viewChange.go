package node

import (
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/leader_election"
)

// --------------------------------------------------------
// Principle Definition
// --------------------------------------------------------

type ViewChanger struct {
	isInViewChange           bool
	currentView              atomic.Int64
	currentSequenceNumber    int64
	leaderElection           *leader_election.LeaderElection
	viewChangeMessages       []core.ViewChangeMessage
	receiveViewChangeMessage *atomic.Int32

	isInViewChangeLock     sync.RWMutex
	viewChangeMessagesLock sync.Mutex
}

func NewViewChanger(cfg *config.Config) *ViewChanger {
	return &ViewChanger{
		isInViewChange:           false,
		currentView:              atomic.Int64{},
		leaderElection:           leader_election.NewLeaderElection(cfg),
		viewChangeMessages:       make([]core.ViewChangeMessage, 0),
		isInViewChangeLock:       sync.RWMutex{},
		viewChangeMessagesLock:   sync.Mutex{},
		receiveViewChangeMessage: &atomic.Int32{},
	}
}

func (vc *ViewChanger) StartViewChange(currentView int64, currentSequenceNumber int64) {
	vc.isInViewChangeLock.Lock()
	vc.isInViewChange = true
	vc.isInViewChangeLock.Unlock()

	vc.currentView.Store(currentView)
	vc.currentSequenceNumber = currentSequenceNumber
	vc.viewChangeMessages = make([]core.ViewChangeMessage, 0)
	vc.receiveViewChangeMessage.Store(0)
}

func (vc *ViewChanger) ResetViewChanger() {
	vc.isInViewChangeLock.Lock()
	defer vc.isInViewChangeLock.Unlock()
	vc.isInViewChange = false
	vc.viewChangeMessages = make([]core.ViewChangeMessage, 0)
	vc.receiveViewChangeMessage.Store(0)
}

func (vc *ViewChanger) ActivateViewChange() {
	vc.isInViewChangeLock.Lock()
	defer vc.isInViewChangeLock.Unlock()
	vc.isInViewChange = true
}

func (vc *ViewChanger) IsInViewChange() bool {
	vc.isInViewChangeLock.RLock()
	defer vc.isInViewChangeLock.RUnlock()
	return vc.isInViewChange
}

// --------------------------------------------------------
// Send View Change Message
// --------------------------------------------------------

// Send View Change Message to Others
func (n *Node) SendViewChangeMessage() {
	havePreparedList := make(map[int64]bool)
	for seqNumber := n.lastStableCheckpoint + 1; seqNumber <= n.lastPrepareSeqNumber; seqNumber++ {
		if n.GetPrepareMessageNumber(seqNumber) == 2*int32(n.cfg.FaultyNodesNum) {
			havePreparedList[seqNumber] = true
		}
	}
	// Take a snapshot of preprepare messages to avoid concurrent map iteration during gob encoding
	preprepareSnapshot := n.SnapshotPreprepareMessages()

	// Precompute shared fields
	baseTimestamp := time.Now().UnixNano()
	baseFrom := n.GetAddr()
	baseCheckpointMsgNumber := func() int32 {
		n.checkpointLock.RLock()
		defer n.checkpointLock.RUnlock()
		counter, ok := n.checkpointList[n.lastStableCheckpoint]
		if !ok || counter == nil {
			return 0
		}
		return counter.Load()
	}()

	for _, othersIp := range config.NodeAddr {
		if othersIp == n.GetAddr() {
			continue
		}
		targetIP := othersIp
		n.log.Info(fmt.Sprintf("Send view change message to %s with sequence number %d", targetIP, n.lastStableCheckpoint))
		msg := core.ViewChangeMessage{
			Timestamp:           baseTimestamp,
			CheckpointSeqNumber: n.lastStableCheckpoint,
			ViewNumber:          n.viewChange.currentView.Load() + 1,
			CheckpointMsgNumber: baseCheckpointMsgNumber,
			From:                baseFrom,
			PreprepareMessages:  preprepareSnapshot,
			To:                  targetIP,
		}
		n.messageHub.Send(core.MsgViewChangeMessage, targetIP, msg, nil)
	}
}

func (n *Node) SendNewViewMessage() {
	// n.viewChange.currentView++

	n.viewChange.currentView.Add(1)
	// n.viewNumber = n.viewChange.currentView

	viewChangeMessages := n.viewChange.viewChangeMessages

	minSeq := int64(math.MaxInt64)
	maxSeq := int64(math.MinInt64)

	for _, viewChangeMessage := range viewChangeMessages {
		if viewChangeMessage.CheckpointSeqNumber < minSeq {
			minSeq = viewChangeMessage.CheckpointSeqNumber
		}
		if viewChangeMessage.PreprepareMessages != nil {
			for seqNumber := range viewChangeMessage.PreprepareMessages {
				if seqNumber > maxSeq {
					maxSeq = seqNumber
				}
			}
		}
	}

	OngoingTxs := make([]*core.Transaction, 0)
	for _, viewChangeMessage := range viewChangeMessages {
		if viewChangeMessage.PreprepareMessages != nil {
			for seqNumber := minSeq; seqNumber <= maxSeq; seqNumber++ {
				if preprepareMsgs, exists := viewChangeMessage.PreprepareMessages[seqNumber]; exists && len(preprepareMsgs) > 0 {
					if preprepareMsgs[0].RequestMessage != nil {
						OngoingTxs = append(OngoingTxs, preprepareMsgs[0].RequestMessage.Txs...)
					}
				}
			}
		}
	}
	n.log.Debug(fmt.Sprintf("Ongoing txs length is %d", len(OngoingTxs)))

	// Send messages asynchronously to avoid blocking
	for _, targetIp := range config.NodeAddr {
		newViewMessage := core.NewViewMessage{
			Timestamp:           time.Now().UnixNano(),
			From:                n.GetAddr(),
			To:                  targetIp,
			OngoingTxs:          OngoingTxs,
			ViewNumber:          n.viewChange.currentView.Load(),
			CheckpointSeqNumber: minSeq,
		}
		n.log.Info(fmt.Sprintf("Send new view message to %s", targetIp))
		n.messageHub.Send(core.MsgNewViewMessage, targetIp, newViewMessage, nil)
	}
}

func (n *Node) SendMempoolSnapshot(toIp string) {
	// Snapshot mempool to include in view-change without holding onto the live slice
	mempoolSnapshot := make([]*core.Transaction, len(n.Mempool))
	copy(mempoolSnapshot, n.Mempool)
	n.Mempool = make([]*core.Transaction, 0)

	for i := int64(0); (i+1)*n.cfg.InjectSpeed < int64(len(mempoolSnapshot)); i++ {
		injectTxs := mempoolSnapshot[i*n.cfg.InjectSpeed : (i+1)*n.cfg.InjectSpeed]

		memMsg := core.MempoolMsg{
			Mempool:    injectTxs,
			From:       n.GetAddr(),
			To:         toIp,
			ViewNumber: n.viewChange.currentView.Load(),
		}
		n.log.Info(fmt.Sprintf("Send mempool message to %s with %d transactions", toIp, len(injectTxs)))
		n.messageHub.Send(core.MsgMempoolMessage, toIp, memMsg, nil)
		if ((i+1)*n.cfg.InjectSpeed)%n.cfg.InjectSpeed == 0 {
			time.Sleep(1 * time.Second)
		}
	}
}

// --------------------------------------------------------
// Handle View Change Message
// --------------------------------------------------------
func (n *Node) HandleViewChangeMessage(data core.ViewChangeMessage) {
	n.handleMessageLock.Lock()
	defer n.handleMessageLock.Unlock()
	intendedViewNumber := data.ViewNumber
	expectedLeader := n.viewChange.leaderElection.GetLeader(intendedViewNumber)
	if intendedViewNumber != n.viewChange.currentView.Load()+1 {
		n.log.Error(fmt.Sprintf("View number mismatch. from %s, intended view number %d, current view number %d", data.From, intendedViewNumber, n.viewChange.currentView))
		return
	}
	n.log.Info(fmt.Sprintf("Received view change message from %s, intended view number %d, current view number %d", data.From, intendedViewNumber, n.viewChange.currentView))

	n.viewChange.viewChangeMessagesLock.Lock()
	n.viewChange.viewChangeMessages = append(n.viewChange.viewChangeMessages, data)
	n.viewChange.viewChangeMessagesLock.Unlock()

	n.viewChange.receiveViewChangeMessage.Add(1)
	vcMsgNumber := n.viewChange.receiveViewChangeMessage.Load()
	n.log.Info(fmt.Sprintf("Received view change messages number %d", vcMsgNumber))
	if vcMsgNumber == 2*int32(n.cfg.FaultyNodesNum) {
		if n.viewChange.leaderElection.GetLeader(intendedViewNumber) == n.GetAddr() {
			n.log.Info(fmt.Sprintf("Received enough view change messages, start new view %d", intendedViewNumber))
			n.SendNewViewMessage()
			return
		}
	}

	if expectedLeader != n.GetAddr() {
		if !n.viewChange.IsInViewChange() && n.viewChange.receiveViewChangeMessage.Load() == int32(n.cfg.FaultyNodesNum)+1 {
			n.log.Info(fmt.Sprintf("Received enough view change messages, start new view %d", intendedViewNumber))
			n.SendViewChangeMessage()
			return
		}
	}
}

func (n *Node) HandleNewViewMessage(data core.NewViewMessage) {
	n.handleMessageLock.Lock()
	defer n.handleMessageLock.Unlock()
	n.log.Info(fmt.Sprintf("Received new view message from %s, view number %d", data.From, data.ViewNumber))
	// n.viewNumber = data.ViewNumber
	n.viewChange.currentView.Store(data.ViewNumber)

	if n.viewChange.leaderElection.GetLeader(data.ViewNumber-1) == n.GetAddr() {
		n.SendMempoolSnapshot(n.viewChange.leaderElection.GetLeader(data.ViewNumber))
	}
	n.RecoverToCheckpoint(data.CheckpointSeqNumber)

	n.viewChange.viewChangeMessagesLock.Lock()
	n.viewChange.ResetViewChanger()
	n.setTimerAllowed(true)
	n.log.Info("have reset view changer successfully")
	n.viewChange.viewChangeMessagesLock.Unlock()

	// if the node is the leader, add the preprepare messages of the ongoing txs to the front of the mempool
	if n.GetAddr() == n.viewChange.leaderElection.GetLeader(data.ViewNumber) {
		n.Mempool = append(data.OngoingTxs, n.Mempool...)
	}
	n.log.Test(fmt.Sprintf(("Preprepare started in HandleNewViewMessage: %t"), n.preprepareStarted))
	if n.preprepareStarted {
		return
	}
	n.preprepareStarted = true
	go n.SendPreprepareMessage(n.lastStableCheckpoint)
}

func (n *Node) HandleMempoolMessage(data core.MempoolMsg) {
	if data.ViewNumber != n.viewChange.currentView.Load() {
		return
	}
	fmt.Printf("HandleMempoolMessage: view number %d, mempool size %d\n", data.ViewNumber, len(data.Mempool))
	n.Mempool = append(n.Mempool, data.Mempool...)
	n.log.Test(fmt.Sprintf(("Preprepare started in HandleMempoolMessage: %t"), n.preprepareStarted))
	if n.preprepareStarted {
		return
	}
	n.preprepareStarted = true
	go n.SendPreprepareMessage(n.lastStableCheckpoint)
}
