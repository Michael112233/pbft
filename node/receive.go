package node

import (
	"github.com/michael112233/pbft/core"
)

// handle request message
func (n *Node) HandleRequestMessage(data core.RequestMessage) {
	n.log.Info("Received request message from %s to %s with %d transactions", data.From, data.To, len(data.Txs))
}
