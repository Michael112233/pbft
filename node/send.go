package node

import (
	"fmt"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
)

var sequenceNumber int64 = -1

func (n *Node) SendPreprepareMessage(data core.RequestMessage) {
	if sequenceNumber == -1 {
		sequenceNumber = GenerateRandomSequenceNumber() % 100000
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
			RequestMessage: &data,
		}
		n.log.Info(fmt.Sprintf("Send preprepare message to %s", othersIp))
		n.messageHub.Send(core.MsgPreprepareMessage, othersIp, preprepareMessage, nil)
	}
}

func (n *Node) SendPrepareMessage(data core.PreprepareMessage) {
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
			RequestMessage: data.RequestMessage,
		}
		n.log.Info(fmt.Sprintf("Send prepare message to %s", othersIp))
		n.messageHub.Send(core.MsgPrepareMessage, othersIp, prepareMessage, nil)
	}
}

func (n *Node) SendCommitMessage(data core.PrepareMessage) {
	// Send Prepare Message to Others.
	for _, othersIp := range config.NodeAddr {
		if othersIp == n.GetAddr() {
			continue
		}
		prepareMessage := core.CommitMessage{
			Timestamp:      time.Now().Unix(),
			From:           n.GetAddr(),
			To:             othersIp,
			SequenceNumber: data.SequenceNumber,
			ViewNumber:     n.viewNumber,
			RequestMessage: data.RequestMessage,
		}
		n.log.Info(fmt.Sprintf("Send commit message to %s", othersIp))
		n.messageHub.Send(core.MsgCommitMessage, othersIp, prepareMessage, nil)
	}
}
