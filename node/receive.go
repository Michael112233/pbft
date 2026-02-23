package node

import (
	"fmt"

	"github.com/michael112233/pbft/core"
)

// handle request message
func (n *Node) HandleRequestMessage(data core.RequestMessage) {
	// n.handleMessageLock.Lock()
	// defer n.handleMessageLock.Unlock()
	// if n.viewChange.IsInViewChange() {
	// 	// n.log.Error("Node %d is in view change and Ignore request message", n.NodeID)
	// 	return
	// }
	// n.log.Info(fmt.Sprintf("Received request message from %s to %s with %d transactions", data.From, data.To, len(data.Txs)))

	// n.mempoolLock.Lock()
	// n.Mempool = append(n.Mempool, data.Txs...)
	// n.mempoolLock.Unlock()
	// n.handleMessageLock.Lock()
	// if n.preprepareStarted {
	// 	n.handleMessageLock.Unlock()
	// 	return
	// }
	// n.preprepareStarted = true
	// n.handleMessageLock.Unlock()
	// n.log.Info(fmt.Sprintf("Preprepare started, send preprepare message to %s", data.To))
	// go n.SendPreprepareMessage(-1)
	// if !n.verificationWorkerStarted.Load() {
	// 	n.verificationWorkerStarted.
	// 	go n.VerifiedClientMessageHandler()
	// 	go n.ClientSignatureVerifier()
	// }
	select {
	case n.unverifiedClientMsgsChan <- data.Txs:

	default:
		n.log.Error(fmt.Sprintf("Dropped the batch"))
	}
}
