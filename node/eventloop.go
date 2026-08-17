package node

import (
	"github.com/michael112233/pbft/core"
)

func (n *Node) ReceiveVerifiedClientRequestCh(req core.ClientMsgSignature) {
	select {
	case n.receiveVerifiedClientRequestCh <- req:
	case <-n.eventLoopStopCh:
	}
}

// startEventLoop starts the node's event loop once.
func (n *Node) startEventLoop() {
	if !n.eventLoopStarted.CompareAndSwap(false, true) {
		return
	}

	go n.run()
}

// run serializes the node's event sources until the node is stopped.
func (n *Node) run() {
	defer close(n.eventLoopDoneCh)
	defer n.StopBatchTimer()

	for {
		clientRequestCh := n.receiveVerifiedClientRequestCh
		if n.pendingRequests.Full() {
			// A nil channel disables this select case. The caller will block and
			// naturally apply backpressure until proposal progress frees space.
			n.log.Debug("node event loop pending request queue is full, blocking client request channel")
			clientRequestCh = nil
		}

		select {
		case req := <-clientRequestCh:
			// cheap check to ignore client req
			if !n.pendingRequests.Enqueue(req) {
				n.log.Error("node event loop received a request while the pending queue was full")
			}
			// cheap check to see leader befor propose
			if n.pendingRequests.Len() >= n.batchLogic.maxBatchSize {
				n.tryPropose(true)
			}
		case consensusMsg := <-n.consensusMsgChan:
			switch consensusMsg.MsgType {
			case core.MsgPreprepareMessage:
				n.HandlePrePrepare(consensusMsg.Msg.(core.PreprepareMsg), consensusMsg.Signature)
			case core.MsgPrepareMessage:
				n.HandlePrepare(consensusMsg.Msg.(core.PrepareMsg), consensusMsg.Signature)
			case core.MsgCommitMessage:
				n.HandleCommit(consensusMsg.Msg.(core.CommitMsg))
			default:
				n.log.Error("Unknown consensus message type: %v", consensusMsg.MsgType)
			}

		case <-n.eventLoopStopCh:
			return
		}
	}
}

// stopEventLoop signals the event loop to stop and waits for it to exit. It is
// safe to call more than once. Calling it before startEventLoop has no effect.
func (n *Node) stopEventLoop() {
	if !n.eventLoopStarted.Load() {
		return
	}

	n.eventLoopStopOnce.Do(func() {
		close(n.eventLoopStopCh)
	})

	<-n.eventLoopDoneCh
}

// last few req left in queue may stall as exe only try once
