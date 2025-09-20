package node

import (
	"fmt"
	"sync"

	"github.com/michael112233/pbft/core"
)

var addPrepare sync.WaitGroup

// handle request message
func (n *Node) HandleRequestMessage(data core.RequestMessage) {
	n.log.Info(fmt.Sprintf("Received request message from %s to %s with %d transactions", data.From, data.To, len(data.Txs)))
	n.SendPreprepareMessage(data)
}

func (n *Node) HandlePreprepareMessage(data core.PreprepareMessage) {
	n.log.Info(fmt.Sprintf("Received preprepare message from %s, sequence number %d", data.From, data.SequenceNumber))

	if data.ViewNumber == n.viewNumber {
		n.SendPrepareMessage(data)
	}
}

func (n *Node) HandlePrepareMessage(data core.PrepareMessage) {
	n.log.Info(fmt.Sprintf("Received prepare message from %s, sequence number %d", data.From, data.SequenceNumber))
	if data.ViewNumber == n.viewNumber {
		n.prepareMsgs[data.SequenceNumber].Add(1)
		n.log.Info(fmt.Sprintf("Prepare message count for sequence %d is now %d", data.SequenceNumber, n.prepareMsgs[data.SequenceNumber].Load()))
	}
	n.log.Info(fmt.Sprintf("After receiving from %s, current prepare messages number is %d", data.From, n.prepareMsgs[data.SequenceNumber].Load()))

	// if len(n.prepareMsgs[data.SequenceNumber]) == 2*int(n.cfg.FaultyNodesNum) {
	// 	n.log.Info(fmt.Sprintf("Received %d prepare messages, enough to commit the block.", len(n.prepareMsgs[data.SequenceNumber])))
	// 	n.SendCommitMessage(data)
	// }
}

func (n *Node) HandleCommitMessage(data core.CommitMessage) {
	n.log.Info(fmt.Sprintf("Received commit message from %s, sequence number %d", data.From, data.SequenceNumber))
}
