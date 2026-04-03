package node

import (
	"fmt"

	"github.com/michael112233/pbft/core"
)

// handle request message
func (n *Node) HandleRequestMessage(data core.RequestMessage) {
	n.viewMu.RLock()
	defer n.viewMu.RUnlock()
	if n.viewChangeRunning {
		n.log.Info(fmt.Sprintf("Node %d is in view change, drop the request message from client %s, id %d", n.GetNodeID(), data.Txs[0].Data.ClientName, data.Txs[0].Data.Id))
		return
	}
	n.log.Info(fmt.Sprintf("Received request message from client %s, id %d, length of batch is %d", data.Txs[0].Data.ClientName, data.Txs[0].Data.Id, len(data.Txs)))
	if n.leaderId == n.GetNodeID() {

		// select {
		// case n.unverifiedClientMsgsChan <- data.Txs:

		// default:
		// 	n.log.Error(fmt.Sprintf("Dropped the batch"))
		// }
		for _, clientMsgSig := range data.Txs {
			// n.pool.Add(clientMsgSig)
			// n.pbftTimerManager.trackPreprepareRequest()
			n.verifiedClientMsgsChan <- clientMsgSig
		}
	} else {
		for _, clienMsgSig := range data.Txs {
			digest, err := ComputeBatchDigest(clienMsgSig.Data)
			if err != nil {
				n.log.Error(fmt.Sprintf("Error computing batch digest: %v", err))
				continue
			}
			n.pool.Add(digest, clienMsgSig)
			n.pbftTimerManager.trackPreprepareRequest()

		}
	}
}
