package node

import (
	"fmt"
	"time"

	"github.com/michael112233/pbft/core"
)

// handle request message
func (n *Node) HandleRequestMessage(data core.RequestMessage) {
	n.viewMu.RLock()
	defer n.viewMu.RUnlock()
	if n.viewChangeRunning {
		n.log.Info(fmt.Sprintf("Node %d is in view change, drop the request message from client %s, id %d", n.GetNodeID(), data.Txs[0].Data.ClientName, data.Txs[0].Data.Id))
		n.sendVCRunningStatus(data.Txs, true) // notify client that view change is running and batch is paused
		return
	}
	go n.sendVCRunningStatus(data.Txs, false) // notify client that view change is not running and batch can proceed
	n.log.Test(fmt.Sprintf("Received request message from client %s, id %d, length of batch is %d", data.Txs[0].Data.ClientName, data.Txs[0].Data.Id, len(data.Txs)))
	if n.leaderId == n.GetNodeID() {

		// select {
		// case n.unverifiedClientMsgsChan <- data.Txs:

		// default:
		// 	n.log.Error(fmt.Sprintf("Dropped the batch"))
		// }
		for _, clientMsgSig := range data.Txs {
			// n.pool.Add(clientMsgSig)
			digestClientMsg, err := ComputeBatchDigest(clientMsgSig.Data)
			if err != nil {
				n.log.Error(fmt.Sprintf("Error computing batch digest: %v", err))
				continue
			}
			n.pool.Add(digestClientMsg, clientMsgSig)
			// n.pbftTimerManager.trackPreprepareRequest()
			time.AfterFunc(4*time.Second, func() {
				n.altPreprepare(digestClientMsg, clientMsgSig)
			})

			n.verifiedClientMsgsChan <- MsgandDigest{
				msg:    clientMsgSig,
				digest: digestClientMsg,
			}
		}
	} else {
		// for _, clienMsgSig := range data.Txs {
		// 	digest, err := ComputeBatchDigest(clienMsgSig.Data)
		// 	if err != nil {
		// 		n.log.Error(fmt.Sprintf("Error computing batch digest: %v", err))
		// 		continue
		// 	}
		// 	n.pool.Add(digest, clienMsgSig)
		// 	n.pbftTimerManager.trackPreprepareRequest()

		// }
	}
}

func (n *Node) altPreprepare(digest [32]byte, clientMsgSig core.ClientMsgSignature) {
	// n.viewMu.RLock()
	// defer n.viewMu.RUnlock()
	// if n.viewChangeRunning || n.GetNodeID() != n.leaderId {
	// 	n.log.Info(fmt.Sprintf("Node %d is in view change, drop the alt preprepare for client %s, id %d", n.GetNodeID(), clientMsgSig.Data.ClientName, clientMsgSig.Data.Id))
	// 	return
	// }
	go n.preprepare(MsgandDigest{
		msg:    clientMsgSig,
		digest: digest,
	}, true)

}
