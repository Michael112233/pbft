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
			// n.pbftTimerManager.trackPreprepareRequest()
			n.verifiedClientMsgsChan <- clientMsgSig
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

func (n *Node) HandleRetry(data core.RetryMessage) {
	n.viewMu.RLock()
	defer n.viewMu.RUnlock()
	if n.viewChangeRunning {
		n.log.FeatureInfo(fmt.Sprintf("Node %d is in view change, drop the retry message from client %s, id %d", n.GetNodeID(), data.Txn.Data.ClientName, data.Txn.Data.Id))
		return
	}
	digest, err := ComputeBatchDigest(data.Txn.Data)
	if err != nil {
		n.log.FeatureError(fmt.Sprintf("Error computing batch digest: %v", err))
		return
	}

	if n.leaderId != n.GetNodeID() {
		n.fairnessMu.Lock()
		defer n.fairnessMu.Unlock()

		_, exists, executed := n.pool.Get(digest)
		if exists || executed {
			n.log.FeatureInfo(fmt.Sprintf("Found existing or executed message for client %s, id %d", data.Txn.Data.ClientName, data.Txn.Data.Id))
			return
		}

		currentPreprepareSeqNumber := n.preprepareSeqNumber.Load()

		if val, exists := n.monitorFairnessCname[data.Txn.Data.Txn.Sender]; !exists {
			n.monitorFairnessCname[data.Txn.Data.Txn.Sender] = monitorData{
				digest: digest,
				seq:    currentPreprepareSeqNumber,
			}
		} else {

			if val.digest != digest {
				n.log.FeatureInfo(fmt.Sprintf("Found existing monitor data for client and found different request for client %s, id %d", data.Txn.Data.ClientName, data.Txn.Data.Id))
			} else {
				n.log.FeatureInfo(fmt.Sprintf("Found existing monitor data for client and found same request for client %s, id %d", data.Txn.Data.ClientName, data.Txn.Data.Id))
			}
		}

	}

}
