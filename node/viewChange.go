package node

import (
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/leader_election"
)

// --------------------------------------------------------
// Principle Definition
// --------------------------------------------------------

type ViewChanger struct {
	isInViewChange        bool
	currentView           int64
	currentSequenceNumber int64
	leaderElection        *leader_election.LeaderElection
	viewChangeMessages    []core.ViewChangeMessage

	isInViewChangeLock     sync.Mutex
	viewChangeMessagesLock sync.Mutex
}

func NewViewChanger(cfg *config.Config) *ViewChanger {
	return &ViewChanger{
		isInViewChange:         false,
		currentView:            0,
		leaderElection:         leader_election.NewLeaderElection(cfg),
		viewChangeMessages:     make([]core.ViewChangeMessage, 0),
		isInViewChangeLock:     sync.Mutex{},
		viewChangeMessagesLock: sync.Mutex{},
	}
}

func (vc *ViewChanger) StartViewChange(currentView int64, currentSequenceNumber int64) {
	vc.isInViewChange = true
	vc.currentView = currentView
	vc.currentSequenceNumber = currentSequenceNumber
	vc.viewChangeMessages = make([]core.ViewChangeMessage, 0)
}

func (vc *ViewChanger) ResetViewChanger() {
	vc.isInViewChangeLock.Lock()
	defer vc.isInViewChangeLock.Unlock()
	vc.isInViewChange = false
	vc.viewChangeMessages = make([]core.ViewChangeMessage, 0)
}

func (vc *ViewChanger) ActivateViewChange() {
	vc.isInViewChangeLock.Lock()
	defer vc.isInViewChangeLock.Unlock()
	vc.isInViewChange = true
}

func (vc *ViewChanger) IsInViewChange() bool {
	vc.isInViewChangeLock.Lock()
	defer vc.isInViewChangeLock.Unlock()
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
	baseTimestamp := time.Now().Unix()
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
		target := othersIp
		n.log.Info(fmt.Sprintf("Send view change message to %s", target))
		go func(targetIP string) {
			msg := core.ViewChangeMessage{
				Timestamp:           baseTimestamp,
				CheckpointSeqNumber: n.lastStableCheckpoint,
				ViewNumber:          n.viewChange.currentView + 1,
				CheckpointMsgNumber: baseCheckpointMsgNumber,
				From:                baseFrom,
				HavePreparedList:    havePreparedList,
				PreprepareMessages:  preprepareSnapshot,
				To:                  targetIP,
			}
			n.messageHub.Send(core.MsgViewChangeMessage, targetIP, msg, nil)
		}(target)
	}
}

func (n *Node) SendNewViewMessage() {
	n.viewChange.currentView++
	n.viewNumber = n.viewChange.currentView

	viewChangeMessages := n.viewChange.viewChangeMessages
	minSequenceNumber := int64(math.MaxInt64)
	maxSequenceNumber := int64(math.MinInt64)
	// preprepareMessages := make(map[int64][]*core.PreprepareMessage)
	for _, viewChangeMessage := range viewChangeMessages {
		currentSequenceNumber := viewChangeMessage.CheckpointSeqNumber
		if currentSequenceNumber < minSequenceNumber {
			minSequenceNumber = currentSequenceNumber
		}
		if currentSequenceNumber > maxSequenceNumber {
			maxSequenceNumber = currentSequenceNumber
			// preprepareMessages = viewChangeMessage.PreprepareMessages
		}
		n.log.Info("Current Sequence Number: %d", currentSequenceNumber)
	}
	// Pre-serialize the common data to avoid repeated serialization
	baseTimestamp := time.Now().Unix()

	// Filter preprepare messages to include only active non-empty window
	// filteredPreprepare := make(map[int64][]*core.PreprepareMessage)
	// startSeq := n.lastStableCheckpoint + 1
	// endSeq := n.lastPrepareSeqNumber
	// if endSeq < startSeq {
	// 	endSeq = startSeq
	// }
	// for seq := startSeq; seq <= endSeq; seq++ {
	// 	msgs := preprepareMessages[seq]
	// 	if len(msgs) > 0 {
	// 		filteredPreprepare[seq] = msgs
	// 	}
	// }

	// Send messages asynchronously to avoid blocking
	for _, targetIp := range config.NodeAddr {
		newViewMessage := core.NewViewMessage{
			Timestamp: baseTimestamp,
			From:      n.GetAddr(),
			To:        targetIp,
			// ViewChangeMessages: n.viewChange.viewChangeMessages,
			ViewNumber: n.viewChange.currentView,
			// PreprepareMessages: preprepareMessages,
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
			Mempool: injectTxs,
			From:    n.GetAddr(),
			To:      toIp,
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
	if n.GetAddr() != expectedLeader {
		return
	}
	if intendedViewNumber != n.viewChange.currentView+1 {
		n.log.Error(fmt.Sprintf("View number mismatch. from %s, intended view number %d, current view number %d", data.From, intendedViewNumber, n.viewChange.currentView))
		return
	}

	n.log.Info(fmt.Sprintf("Received view change message from %s, intended view number %d, current view number %d", data.From, intendedViewNumber, n.viewChange.currentView))

	n.viewChange.viewChangeMessagesLock.Lock()
	n.viewChange.viewChangeMessages = append(n.viewChange.viewChangeMessages, data)
	vcMsgNumber := len(n.viewChange.viewChangeMessages)
	n.viewChange.viewChangeMessagesLock.Unlock()

	if vcMsgNumber == 2*int(n.cfg.FaultyNodesNum) {
		n.log.Info(fmt.Sprintf("Received enough view change messages, start new view %d", intendedViewNumber))
		if n.viewChange.leaderElection.GetLeader(intendedViewNumber) == n.GetAddr() {
			n.SendNewViewMessage()
		}
	}
}

func (n *Node) HandleNewViewMessage(data core.NewViewMessage) {
	n.handleMessageLock.Lock()
	defer n.handleMessageLock.Unlock()
	n.log.Info(fmt.Sprintf("Received new view message from %s, view number %d", data.From, data.ViewNumber))
	n.viewNumber = data.ViewNumber

	fmt.Printf("Current Leader: %s, New Leader: %s\n", n.viewChange.leaderElection.GetLeader(n.viewNumber-1), n.viewChange.leaderElection.GetLeader(n.viewNumber))
	if n.viewChange.leaderElection.GetLeader(n.viewNumber-1) == n.GetAddr() {
		n.SendMempoolSnapshot(n.viewChange.leaderElection.GetLeader(n.viewNumber))
	}

	n.viewChange.viewChangeMessagesLock.Lock()
	n.viewChange.ResetViewChanger()
	n.log.Info("have reset view changer successfully")
	n.viewChange.viewChangeMessagesLock.Unlock()

	// for seqNumber := n.lastStableCheckpoint + 1; seqNumber <= n.lastPrepareSeqNumber; seqNumber++ {
	// 	preprepareMessages := data.PreprepareMessages[seqNumber]
	// 	// Check if preprepareMessages slice is not empty before accessing elements
	// 	if len(preprepareMessages) == 0 {
	// 		n.log.Warn(fmt.Sprintf("No preprepare messages found for sequence number %d", seqNumber))
	// 		continue
	// 	}
	// 	for _, othersIp := range config.NodeAddr {
	// 		if othersIp == n.GetAddr() {
	// 			continue
	// 		}
	// 		n.log.Info(fmt.Sprintf("Send preprepare message to %s with sequence number %d", othersIp, seqNumber))
	// 		n.SetPreprepareSequenceNumber(seqNumber, preprepareMessages[0])
	// 		n.messageHub.Send(core.MsgPreprepareMessage, othersIp, *preprepareMessages[0], nil)
	// 	}
	// }
}

func (n *Node) HandleMempoolMessage(data core.MempoolMsg) {
	n.log.Info(fmt.Sprintf("Received mempool message from %s", data.From))
	n.log.Info(fmt.Sprintf("Mempool size: %d", len(data.Mempool)))
	n.log.Info(fmt.Sprintf("Mempool: %v", data.Mempool))

	n.Mempool = data.Mempool
	go n.SendPreprepareMessage()
}
