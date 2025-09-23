package node

import (
	"fmt"

	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/utils"
)

// handle request message
func (n *Node) HandleRequestMessage(data core.RequestMessage) {
	n.log.Info(fmt.Sprintf("Received request message from %s to %s with %d transactions", data.From, data.To, len(data.Txs)))
	n.StartExpireTimer()
	n.SendPreprepareMessage(data)
}

func (n *Node) HandlePreprepareMessage(data core.PreprepareMessage) {
	n.log.Info(fmt.Sprintf("Received preprepare message from %s, sequence number %d", data.From, data.SequenceNumber))
	if data.Digest != utils.GetDigest(data.RequestMessage) {
		n.log.Error(fmt.Sprintf("Preprepare message digest mismatch. from %s, sequence number %d", data.From, data.SequenceNumber))
		return
	} else if data.ViewNumber != n.viewNumber {
		n.log.Error(fmt.Sprintf("Preprepare message view number mismatch. from %s, sequence number %d", data.From, data.SequenceNumber))
		return
	} else if data.SequenceNumber < n.cfg.SeqNumberLowerBound || data.SequenceNumber > n.cfg.SeqNumberUpperBound {
		n.log.Error(fmt.Sprintf("Preprepare message sequence number out of range. from %s, sequence number %d", data.From, data.SequenceNumber))
		return
	} else if n.GetPreprepareSequenceNumber() != -1 && data.SequenceNumber != n.GetPreprepareSequenceNumber() + 1 {
		n.log.Error(fmt.Sprintf("Preprepare message sequence number mismatch. from %s, sequence number %d", data.From, data.SequenceNumber))
		return
	} else {
		n.SetPreprepareSequenceNumber(data.SequenceNumber)
		n.StartExpireTimer()
		n.SendPrepareMessage(data)
	}

}

func (n *Node) HandlePrepareMessage(data core.PrepareMessage) {
	n.log.Info(fmt.Sprintf("Received prepare message from %s, sequence number %d", data.From, data.SequenceNumber))
	if data.Digest != utils.GetDigest(data.RequestMessage) {
		n.log.Error(fmt.Sprintf("Prepare message digest mismatch. from %s, sequence number %d", data.From, data.SequenceNumber))
		return
	} else if data.ViewNumber != n.viewNumber {
		n.log.Error(fmt.Sprintf("Prepare message view number mismatch. from %s, sequence number %d", data.From, data.SequenceNumber))
		return
	} else if data.SequenceNumber < n.cfg.SeqNumberLowerBound || data.SequenceNumber > n.cfg.SeqNumberUpperBound {
		n.log.Error(fmt.Sprintf("Prepare message sequence number out of range. from %s, sequence number %d", data.From, data.SequenceNumber))
		return
	} else if n.GetPrepareSequenceNumber() != -1 && data.SequenceNumber != n.GetPrepareSequenceNumber() + 1 {
		n.log.Error(fmt.Sprintf("Prepare message sequence number mismatch. from %s, sequence number %d", data.From, data.SequenceNumber))
		return
	} else {
		n.prepareMsgNumber[data.SequenceNumber].Add(1)
		n.log.Info(fmt.Sprintf("Prepare message count for sequence %d is now %d", data.SequenceNumber, n.prepareMsgNumber[data.SequenceNumber].Load()))
	}
	n.log.Info(fmt.Sprintf("After receiving from %s, current prepare messages number is %d", data.From, n.prepareMsgNumber[data.SequenceNumber].Load()))

	if n.prepareMsgNumber[data.SequenceNumber].Load() == 2*int32(n.cfg.FaultyNodesNum) {
		n.log.Info(fmt.Sprintf("Received %d prepare messages, enough to commit the block.", n.prepareMsgNumber[data.SequenceNumber].Load()))
		n.SetPrepareSequenceNumber(data.SequenceNumber)
		n.StartExpireTimer()
		// if n.NodeID == 3 {
		// 	return
		// }
		n.SendCommitMessage(data)
	}
}

func (n *Node) HandleCommitMessage(data core.CommitMessage) {
	n.log.Info(fmt.Sprintf("Received commit message from %s, sequence number %d", data.From, data.SequenceNumber))
	if data.ViewNumber != n.viewNumber {
		n.log.Error(fmt.Sprintf("Commit message view number mismatch. from %s, sequence number %d", data.From, data.SequenceNumber))
		return
	} else if data.Digest != utils.GetDigest(data.RequestMessage) {
		n.log.Error(fmt.Sprintf("Commit message digest mismatch. from %s, sequence number %d", data.From, data.SequenceNumber))
		return
	} else if data.SequenceNumber < n.cfg.SeqNumberLowerBound || data.SequenceNumber > n.cfg.SeqNumberUpperBound {
		n.log.Error(fmt.Sprintf("Commit message sequence number out of range. from %s, sequence number %d", data.From, data.SequenceNumber))
		return
	} else if n.GetCommitSequenceNumber() != -1 && data.SequenceNumber != n.GetCommitSequenceNumber() + 1 {
		n.log.Error(fmt.Sprintf("Commit message sequence number mismatch. from %s, sequence number %d", data.From, data.SequenceNumber))
		return
	} else {
		n.commitMsgNumber[data.SequenceNumber].Add(1)
		n.log.Info(fmt.Sprintf("Commit message count for sequence %d is now %d", data.SequenceNumber, n.commitMsgNumber[data.SequenceNumber].Load()))
	}
	n.log.Info(fmt.Sprintf("After receiving from %s, current prepare messages number is %d", data.From, n.prepareMsgNumber[data.SequenceNumber].Load()))

	if n.commitMsgNumber[data.SequenceNumber].Load() == 2*int32(n.cfg.FaultyNodesNum) {
		n.log.Info(fmt.Sprintf("Received %d prepare messages, enough to commit the block.", n.prepareMsgNumber[data.SequenceNumber].Load()))
		n.SetCommitSequenceNumber(data.SequenceNumber)
		n.StartExpireTimer()
		n.SendReplyMessage(data)
	}
}
