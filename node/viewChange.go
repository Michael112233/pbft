package node

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
	addr2vcMsg            map[string]core.ViewChangeMessage

	addr2vcMsgLock sync.Mutex
}

func NewViewChanger(cfg *config.Config) *ViewChanger {
	return &ViewChanger{
		isInViewChange: false,
		currentView:    -1,
		leaderElection: leader_election.NewLeaderElection(cfg),
		addr2vcMsg:     make(map[string]core.ViewChangeMessage),
	}
}

func (vc *ViewChanger) StartViewChange(currentView int64, currentSequenceNumber int64) {
	vc.isInViewChange = true
	vc.currentView = currentView
	vc.currentSequenceNumber = currentSequenceNumber
	vc.addr2vcMsg = make(map[string]core.ViewChangeMessage)
}

func (vc *ViewChanger) ResetViewChanger() {
	vc.isInViewChange = false
	vc.currentView = -1
	vc.currentSequenceNumber = -1
	vc.addr2vcMsg = make(map[string]core.ViewChangeMessage)
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
		CheckpointMsgNumber: n.checkpointList[n.lastStableCheckpoint].Load(),
		From:                n.GetAddr(),
		HavePreparedList:    havePreparedList,
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
	// n.viewChange.currentView++
	// n.viewNumber = n.viewChange.currentView

	// // TODO: Set V - valid view change message

	// newViewMessage := core.NewViewMessage{
	// 	Timestamp:  time.Now().Unix(),
	// 	From:       n.GetAddr(),
	// 	To:         "",
	// 	ViewNumber: n.viewChange.currentView,
	// }
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

	n.viewChange.addr2vcMsgLock.Lock()
	n.viewChange.addr2vcMsg[data.From] = data
	vcMsgNumber := len(n.viewChange.addr2vcMsg)
	n.viewChange.addr2vcMsgLock.Unlock()

	if vcMsgNumber == 2*int(n.cfg.FaultyNodesNum) {
		n.log.Info(fmt.Sprintf("Received enough view change messages, start new view %d", intendedViewNumber))
		n.sendNewViewMessage()
	}
}
