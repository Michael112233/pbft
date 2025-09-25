package node

import (
	"fmt"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/utils"
)

var sequenceNumber int64 = -1

func (n *Node) SendPreprepareMessage(data core.RequestMessage) {
	if sequenceNumber == -1 {
		sequenceNumber = GenerateRandomSequenceNumber(n.cfg.SeqNumberUpperBound, n.cfg.SeqNumberLowerBound)
	} else {
		sequenceNumber++
	}
	for _, othersIp := range config.NodeAddr {
		if othersIp == n.GetAddr() {
			continue
		}
		preprepareMessage := core.PreprepareMessage{
			Timestamp:      time.Now().Unix(),
			From:           n.GetAddr(),
			To:             othersIp,
			SequenceNumber: sequenceNumber,
			ViewNumber:     n.viewNumber,
			Digest:         utils.GetDigest(&data),
			RequestMessage: &data,
		}
		n.log.Info(fmt.Sprintf("Send preprepare message to %s", othersIp))
		n.messageHub.Send(core.MsgPreprepareMessage, othersIp, preprepareMessage, nil)
	}
}

func (n *Node) SendPrepareMessage(data core.PreprepareMessage) {
	n.AddPrepareMessageNumber(data.SequenceNumber)
	n.log.Info(fmt.Sprintf("SeqNumber %d: After receiving from %s to itself, current prepare messages number is %d", data.SequenceNumber, data.From, n.prepareMsgNumber[data.SequenceNumber].Load()))
	// Send Prepare Message to Others.
	for _, othersIp := range config.NodeAddr {
		if othersIp == n.GetAddr() {
			continue
		}
		prepareMessage := core.PrepareMessage{
			Timestamp:      time.Now().Unix(),
			From:           n.GetAddr(),
			To:             othersIp,
			SequenceNumber: data.SequenceNumber,
			ViewNumber:     n.viewNumber,
			Digest:         data.Digest,
			RequestMessage: data.RequestMessage,
		}
		n.log.Info(fmt.Sprintf("Send prepare message to %s", othersIp))
		n.messageHub.Send(core.MsgPrepareMessage, othersIp, prepareMessage, nil)
	}
}

func (n *Node) SendCommitMessage(data core.PrepareMessage) {
	n.AddCommitMessageNumber(data.SequenceNumber)
	n.log.Info(fmt.Sprintf("SeqNumber %d: After receiving from %s to itself, current commit messages number is %d", data.SequenceNumber, data.From, n.commitMsgNumber[data.SequenceNumber].Load()))

	// Send Prepare Message to Others.
	for _, othersIp := range config.NodeAddr {
		if othersIp == n.GetAddr() {
			continue
		}
		commitMessage := core.CommitMessage{
			Timestamp:      time.Now().Unix(),
			From:           n.GetAddr(),
			To:             othersIp,
			SequenceNumber: data.SequenceNumber,
			ViewNumber:     n.viewNumber,
			Digest:         data.Digest,
			RequestMessage: data.RequestMessage,
		}
		n.log.Info(fmt.Sprintf("Send commit message to %s", othersIp))
		n.messageHub.Send(core.MsgCommitMessage, othersIp, commitMessage, nil)
	}
}

func (n *Node) SendReplyMessage(data core.CommitMessage) {
	replyMessage := core.ReplyMessage{
		Timestamp:      time.Now().Unix(),
		From:           n.GetAddr(),
		To:             config.ClientAddr,
		SequenceNumber: data.SequenceNumber,
		ViewNumber:     n.viewNumber,
		RequestMessage: data.RequestMessage,
	}
	n.log.Info(fmt.Sprintf("Send reply message to %s", config.ClientAddr))
	timerID := fmt.Sprintf("request_%d_%d", n.NodeID, data.RequestMessage.Id)
	n.StopExpireTimer(timerID)
	n.messageHub.Send(core.MsgReplyMessage, config.ClientAddr, replyMessage, nil)
}
