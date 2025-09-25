package node

import (
	"fmt"

	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/utils"
)

// handle request message
func (n *Node) HandleRequestMessage(data core.RequestMessage) {
	n.handleMessageLock.Lock()
	defer n.handleMessageLock.Unlock()
	if n.viewChange.IsInViewChange() {
		n.log.Error("Node %d is in view change and Ignore request message", n.NodeID)
		return
	}
	timerID := fmt.Sprintf("request_%d_%d", n.NodeID, data.Id)
	n.StartExpireTimer(timerID)
	n.log.Info(fmt.Sprintf("Received request message from %s to %s with %d transactions", data.From, data.To, len(data.Txs)))
	n.SendPreprepareMessage(data)
}

func (n *Node) HandlePreprepareMessage(data core.PreprepareMessage) {
	n.handleMessageLock.Lock()
	defer n.handleMessageLock.Unlock()
	timerID := fmt.Sprintf("request_%d_%d", n.NodeID, data.RequestMessage.Id)
	n.StartExpireTimer(timerID)
	if n.viewChange.IsInViewChange() {
		n.log.Error("Node %d is expired and Start to trigger view change", n.NodeID)
		return
	}
	n.log.Info(fmt.Sprintf("SeqNumber %d: Received preprepare message from %s, sequence number %d", data.SequenceNumber, data.From, data.SequenceNumber))
	// if n.NodeID == 1 {
	// 	n.log.Error("node 1 is faulty!")
	// 	return
	// }
	if data.Digest != utils.GetDigest(data.RequestMessage) {
		n.log.Error(fmt.Sprintf("SeqNumber %d: Preprepare message digest mismatch. from %s, sequence number %d", data.SequenceNumber, data.From, data.SequenceNumber))
		return
	} else if data.ViewNumber != n.viewNumber {
		n.log.Error(fmt.Sprintf("SeqNumber %d: Preprepare message view number mismatch. from %s, sequence number %d", data.SequenceNumber, data.From, data.SequenceNumber))
		return
	} else if data.SequenceNumber < n.cfg.SeqNumberLowerBound || data.SequenceNumber > n.cfg.SeqNumberUpperBound {
		n.log.Error(fmt.Sprintf("SeqNumber %d: Preprepare message sequence number out of range. from %s, sequence number %d", data.SequenceNumber, data.From, data.SequenceNumber))
		return
	} else if n.GetPreprepareSequenceNumber() != -1 && data.SequenceNumber != n.GetPreprepareSequenceNumber()+1 {
		n.log.Error(fmt.Sprintf("SeqNumber %d: Preprepare message sequence number mismatch. from %s, sequence number %d", data.SequenceNumber, data.From, data.SequenceNumber))
		return
	} else {
		n.log.Info(fmt.Sprintf("SeqNumber %d: Preprepare message sequence number succeeds. from %s, sequence number %d", data.SequenceNumber, data.From, data.SequenceNumber))
		n.SetPreprepareSequenceNumber(data.SequenceNumber)
		n.SendPrepareMessage(data)
	}

}

func (n *Node) HandlePrepareMessage(data core.PrepareMessage) {
	n.handleMessageLock.Lock()
	defer n.handleMessageLock.Unlock()
	if n.viewChange.IsInViewChange() {
		n.log.Error("Node %d is expired and Start to trigger view change", n.NodeID)
		return
	}
	// if n.NodeID == 1 {
	// 	n.log.Error("node 1 is faulty!")
	// 	return
	// }
	n.log.Info(fmt.Sprintf("SeqNumber %d: Received prepare message from %s, sequence number %d", data.SequenceNumber, data.From, data.SequenceNumber))
	if data.Digest != utils.GetDigest(data.RequestMessage) {
		n.log.Error(fmt.Sprintf("SeqNumber %d: Prepare message digest mismatch. from %s, sequence number %d", data.SequenceNumber, data.From, data.SequenceNumber))
		return
	} else if data.ViewNumber != n.viewNumber {
		n.log.Error(fmt.Sprintf("SeqNumber %d: Prepare message view number mismatch. from %s, sequence number %d", data.SequenceNumber, data.From, data.SequenceNumber))
		return
	} else if data.SequenceNumber < n.cfg.SeqNumberLowerBound || data.SequenceNumber > n.cfg.SeqNumberUpperBound {
		n.log.Error(fmt.Sprintf("SeqNumber %d: Prepare message sequence number out of range. from %s, sequence number %d", data.SequenceNumber, data.From, data.SequenceNumber))
		return
	} else if n.GetPrepareSequenceNumber() != -1 && data.SequenceNumber != n.GetPrepareSequenceNumber()+1 {
		n.log.Error(fmt.Sprintf("SeqNumber %d: Prepare message sequence number mismatch. from %s, sequence number %d", data.SequenceNumber, data.From, data.SequenceNumber))
		return
	} else {
		n.AddPrepareMessageNumber(data.SequenceNumber)
		n.log.Info(fmt.Sprintf("SeqNumber %d: Prepare message count for sequence %d is now %d", data.SequenceNumber, data.SequenceNumber, n.prepareMsgNumber[data.SequenceNumber].Load()))
	}
	n.log.Info(fmt.Sprintf("SeqNumber %d: After receiving from %s, current prepare messages number is %d", data.SequenceNumber, data.From, n.prepareMsgNumber[data.SequenceNumber].Load()))

	if n.GetPrepareMessageNumber(data.SequenceNumber) == 2*int32(n.cfg.FaultyNodesNum) {
		n.log.Info(fmt.Sprintf("SeqNumber %d: Received %d prepare messages, enough to commit the block.", data.SequenceNumber, n.prepareMsgNumber[data.SequenceNumber].Load()))
		n.SetPrepareSequenceNumber(data.SequenceNumber)
		n.SendCommitMessage(data)
	}
}

func (n *Node) HandleCommitMessage(data core.CommitMessage) {
	n.handleMessageLock.Lock()
	defer n.handleMessageLock.Unlock()
	if n.viewChange.IsInViewChange() {
		n.log.Error("Node %d is expired and Start to trigger view change", n.NodeID)
		return
	}
	// if n.NodeID == 1 {
	// 	n.log.Error("node 1 is faulty!")
	// 	return
	// }
	n.log.Info(fmt.Sprintf("SeqNumber %d: Received commit message from %s, sequence number %d", data.SequenceNumber, data.From, data.SequenceNumber))
	if data.ViewNumber != n.viewNumber {
		n.log.Error(fmt.Sprintf("SeqNumber %d: Commit message view number mismatch. from %s, sequence number %d", data.SequenceNumber, data.From, data.SequenceNumber))
		return
	} else if data.Digest != utils.GetDigest(data.RequestMessage) {
		n.log.Error(fmt.Sprintf("SeqNumber %d: Commit message digest mismatch. from %s, sequence number %d", data.SequenceNumber, data.From, data.SequenceNumber))
		return
	} else if data.SequenceNumber < n.cfg.SeqNumberLowerBound || data.SequenceNumber > n.cfg.SeqNumberUpperBound {
		n.log.Error(fmt.Sprintf("SeqNumber %d: Commit message sequence number out of range. from %s, sequence number %d", data.SequenceNumber, data.From, data.SequenceNumber))
		return
	} else if n.GetCommitSequenceNumber() != -1 && data.SequenceNumber != n.GetCommitSequenceNumber()+1 {
		n.log.Error(fmt.Sprintf("SeqNumber %d: Commit message sequence number mismatch. from %s, sequence number %d", data.SequenceNumber, data.From, data.SequenceNumber))
		return
	} else {
		n.AddCommitMessageNumber(data.SequenceNumber)
		n.log.Info(fmt.Sprintf("SeqNumber %d: Commit message count for sequence %d is now %d", data.SequenceNumber, data.SequenceNumber, n.commitMsgNumber[data.SequenceNumber].Load()))
	}
	n.log.Info(fmt.Sprintf("SeqNumber %d: After receiving from %s, current commit messages number is %d", data.SequenceNumber, data.From, n.commitMsgNumber[data.SequenceNumber].Load()))

	if n.GetCommitMessageNumber(data.SequenceNumber) == 2*int32(n.cfg.FaultyNodesNum) {
		n.log.Info(fmt.Sprintf("SeqNumber %d: Received %d commit messages, enough to reply to client.", data.SequenceNumber, n.commitMsgNumber[data.SequenceNumber].Load()))
		n.SetCommitSequenceNumber(data.SequenceNumber)
		n.seq2digest[data.SequenceNumber] = data.Digest
		go n.TriggerGarbageCollection(data.SequenceNumber, data.Digest)
		n.SendReplyMessage(data)
	}
}

func (n *Node) HandleCloseMessage(data core.CloseMessage) {
	n.log.Info(fmt.Sprintf("Received close message from %s", data.From))
	n.StopChan <- struct{}{}
}
