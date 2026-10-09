package client

import (
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
)

func (c *Client) HandleReplyMessage(data core.ReplyMessage) {
	c.log.Info("Received reply message from %s, client message id %d success=%t error=%q seq=%d", data.From, data.ClientMsg.Id, data.Result.Success, data.Result.Error, data.Result.ExecutedSeqNum)
	// Block := core.NewBlock(data.SequenceNumber, data.RequestMessage.Txs, data.RequestMessage.To, data.RequestMessage.Timestamp)
	// Block.AddCommittedNode(data.From)
	// core.Chain.AddBlock(Block)
	go c.TransactionManager.ReplyTxn(data)
}

func (c *Client) HandleCommitTpsMessage(data core.CommitTps) {

	// c.log.Info(fmt.Sprintf("Received commit tps message from %s, client message id %d", data.From, data.ClientMsg.Id))
	c.TransactionManager.CommitTps(data)
}

func (c *Client) HandleLeaderUpdate(data core.LeaderIdUpdate) {
	c.leaderMu.Lock()
	if data.View.LessThanOrEqual(c.currentView) {
		c.leaderMu.Unlock()
		c.log.Info("Received old leader update message with view (%d,%d), current view is (%d,%d), ignore the message", data.View.Generation, data.View.Counter, c.currentView.Generation, c.currentView.Counter)
		return
	}
	leaderUpdate := LeaderUpdate{
		view:     data.View,
		leaderId: data.NewLeaderId,
		action:   data.Action,
	}
	c.newLeaderQuorum[leaderUpdate]++
	oldLeader := c.leaderAddr
	observed := false
	var observedAt time.Time
	// leader doesnt send update wait for rest of 2f
	if c.newLeaderQuorum[leaderUpdate] == 2*c.fNodes {
		c.leaderAddr = config.NodeAddr[data.NewLeaderId]
		c.leaderChangedAt = time.Now()
		c.currentView = data.View
		leaderAddr := c.leaderAddr
		observed = true
		observedAt = c.leaderChangedAt
		c.log.Info("Received leader update message, new leader id %d, new leader addr %s", data.NewLeaderId, leaderAddr)

	}
	newLeader := c.leaderAddr
	c.leaderMu.Unlock()

	// This quorum is the controller's first permitted notice of the new leader
	// (client/throttlemanager.go). Off the mutex, never blocking.
	if observed {
		c.notifyLeaderObserved(leaderNotice{view: data.View, leaderID: data.NewLeaderId, action: data.Action, observedAt: observedAt})
	}

	// a non-leader drops client requests, so what is still queued for the old
	// leader is stale
	if newLeader != oldLeader && oldLeader != "" && c.messageHub != nil {
		if n := c.messageHub.DiscardQueued(oldLeader); n > 0 {
			c.log.Info("STALL: discarded %d queued requests for old leader %s", n, oldLeader)
		}
	}

}

func (c *Client) HandleVCRunningStatus(data core.VCRunningStatus) {
	// c.vcrunChan <- data
}
