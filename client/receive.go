package client

import (
	"fmt"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
)

func (c *Client) HandleReplyMessage(data core.ReplyMessage) {
	c.log.Info(fmt.Sprintf("Received reply message from %s, client message id %d success=%t error=%q seq=%d", data.From, data.ClientMsg.Id, data.Result.Success, data.Result.Error, data.Result.ExecutedSeqNum))
	// Block := core.NewBlock(data.SequenceNumber, data.RequestMessage.Txs, data.RequestMessage.To, data.RequestMessage.Timestamp)
	// Block.AddCommittedNode(data.From)
	// core.Chain.AddBlock(Block)
	go c.TransactionManager.ReplyTxn(data)
}

func (c *Client) HandleCommitTpsMessage(data core.CommitTps) {

	// c.log.Info(fmt.Sprintf("Received commit tps message from %s, client message id %d", data.From, data.ClientMsg.Id))
	go c.TransactionManager.CommitTps(data)
}

func (c *Client) HandleLeaderUpdate(data core.LeaderIdUpdate) {
	c.leaderMu.Lock()
	if data.View <= c.currentView {
		c.leaderMu.Unlock()
		c.log.Info(fmt.Sprintf("Received old leader update message with view %d, current view is %d, ignore the message", data.View, c.currentView))
		return
	}
	c.leaderAddr = config.NodeAddr[data.NewLeaderId]
	c.currentView = data.View
	leaderAddr := c.leaderAddr
	c.leaderMu.Unlock()
	c.log.Info(fmt.Sprintf("Received leader update message, new leader id %d, new leader addr %s", data.NewLeaderId, leaderAddr))
	// select {
	// case c.cchan <- struct{}{}:
	// 	c.log.Info(fmt.Sprintf("send to chan Received leader update message, new leader id %d, new leader addr %s", data.NewLeaderId, leaderAddr))
	// default:
	// 	c.log.Info(fmt.Sprintf("leader update signal already pending, new leader id %d, new leader addr %s", data.NewLeaderId, leaderAddr))
	// }
}

func (c *Client) HandleVCRunningStatus(data core.VCRunningStatus) {
	// c.vcrunChan <- data
}
