package node

import (
	"fmt"

	"github.com/michael112233/pbft/core"
)

// handle request message
func (n *Node) HandleRequestMessage(data core.RequestMessage) {
	n.viewMu.RLock()

	if n.viewChangeRunning || n.leaderId != n.GetNodeID() {
		n.log.Info(fmt.Sprintf("Node %d is in view change or not leader anymore, drop the request message from client %s, id %d", n.GetNodeID(), data.Txs[0].Data.ClientName, data.Txs[0].Data.Id))
		n.viewMu.RUnlock()
		return
	}

	n.viewMu.RUnlock()
	n.log.Test(fmt.Sprintf("Received request message from client %s, id %d, length of batch is %d", data.Txs[0].Data.ClientName, data.Txs[0].Data.Id, len(data.Txs)))

	for _, clientMsgSig := range data.Txs {
		go n.preprepare(clientMsgSig)
	}
}
