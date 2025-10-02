package node

/*
import (
	"fmt"
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

	viewChangeMessagesLock sync.Mutex
}

func NewViewChanger(cfg *config.Config) *ViewChanger {
	return &ViewChanger{
		isInViewChange:     false,
		currentView:        -1,
		leaderElection:     leader_election.NewLeaderElection(cfg),
		viewChangeMessages: make([]core.ViewChangeMessage, 0),
	}
}

func (vc *ViewChanger) StartViewChange(currentView int64, currentSequenceNumber int64) {
	vc.isInViewChange = true
	vc.currentView = currentView
	vc.currentSequenceNumber = currentSequenceNumber
	vc.viewChangeMessages = make([]core.ViewChangeMessage, 0)
}

func (vc *ViewChanger) ResetViewChanger() {
	vc.isInViewChange = false
	vc.currentView = -1
	vc.currentSequenceNumber = -1
	vc.viewChangeMessages = make([]core.ViewChangeMessage, 0)
}

func (vc *ViewChanger) IsInViewChange() bool {
	return vc.isInViewChange
}

// --------------------------------------------------------
// Send View Change Message
// --------------------------------------------------------

// Send View Change Message to Others
func (n *Node) SendViewChangeMessage() {
	havePreparedList := make(map[int64]bool)
	for seqNumber := n.lastStableCheckpoint + 1; seqNumber <= n.lastPrepareSeqNumber; seqNumber++ {
		if n.prepareMsgNumber[seqNumber].Load() == 2*int32(n.cfg.FaultyNodesNum) {
			havePreparedList[seqNumber] = true
		}
	}

	viewChangeMessage := core.ViewChangeMessage{
		Timestamp:           time.Now().Unix(),
		CheckpointSeqNumber: n.lastStableCheckpoint,
		ViewNumber:          n.viewChange.currentView + 1,
		CheckpointMsgNumber: func() int32 {
			n.checkpointLock.RLock()
			defer n.checkpointLock.RUnlock()
			return n.checkpointList[n.lastStableCheckpoint].Load()
		}(),
		From:                n.GetAddr(),
		HavePreparedList:    havePreparedList,
		PreprepareMessages:  n.preprepareMsg,
		To:                  "",
	}
	for _, othersIp := range config.NodeAddr {
		if othersIp == n.GetAddr() {
			continue
		}
		viewChangeMessage.To = othersIp
		n.log.Info(fmt.Sprintf("Send view change message to %s", othersIp))
		n.messageHub.Send(core.MsgViewChangeMessage, othersIp, viewChangeMessage, nil)
	}
}

func (n *Node) sendNewViewMessage() {
	n.viewChange.currentView++
	n.viewNumber = n.viewChange.currentView

	viewChangeMessages := n.viewChange.viewChangeMessages
	WaitingPreprepareMessages := make(map[int64]*core.PreprepareMessage)
	// Waiting to do, judge whether the leader can send different transactions to different nodes
	for _, viewChangeMessage := range viewChangeMessages {
		preprepareMessages := viewChangeMessage.PreprepareMessages
		currentMsg := preprepareMessages[0][0]
		preprepareMsg := core.PreprepareMessage{
			Timestamp:      time.Now().Unix(),
			From:           n.GetAddr(),
			To:             "",
			SequenceNumber: currentMsg.SequenceNumber,
			ViewNumber:     n.viewChange.currentView,
			Digest:         currentMsg.Digest,
			RequestMessage: currentMsg.RequestMessage,
		}
		WaitingPreprepareMessages[currentMsg.SequenceNumber] = &preprepareMsg
	}

	newViewMessage := core.NewViewMessage{
		Timestamp:          time.Now().Unix(),
		From:               n.GetAddr(),
		To:                 "",
		ViewChangeMessages: n.viewChange.viewChangeMessages,
		ViewNumber:         n.viewChange.currentView,
		PreprepareMessages: WaitingPreprepareMessages,
	}
	for _, othersIp := range config.NodeAddr {
		if othersIp == n.GetAddr() {
			continue
		}
		n.messageHub.Send(core.MsgNewViewMessage, othersIp, newViewMessage, nil)
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
		n.log.Error(fmt.Sprintf("View number mismatch. from %s, sequence number %d", data.From, data.CheckpointSeqNumber))
		return
	}

	n.log.Info(fmt.Sprintf("Received view change message from %s, sequence number %d", data.From, data.CheckpointSeqNumber))

	n.viewChange.viewChangeMessagesLock.Lock()
	n.viewChange.viewChangeMessages = append(n.viewChange.viewChangeMessages, data)
	vcMsgNumber := len(n.viewChange.viewChangeMessages)
	n.viewChange.viewChangeMessagesLock.Unlock()

	if vcMsgNumber == 2*int(n.cfg.FaultyNodesNum) {
		n.log.Info(fmt.Sprintf("Received enough view change messages, start new view %d", intendedViewNumber))
		n.sendNewViewMessage()
	}
}

func (n *Node) HandleNewViewMessage(data core.NewViewMessage) {
	n.handleMessageLock.Lock()
	defer n.handleMessageLock.Unlock()
	n.log.Info(fmt.Sprintf("Received new view message from %s, view number %d", data.From, data.ViewNumber))
	// n.preprepareMsg = data.PreprepareMessages
	n.Stop()
}
*/
