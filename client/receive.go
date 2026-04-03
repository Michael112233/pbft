package client

import (
	"fmt"

	"github.com/michael112233/pbft/core"
)

func (c *Client) HandleReplyMessage(data core.ReplyMessage) {
	c.log.Info(fmt.Sprintf("Received reply message from %s, client message id %d success=%t error=%q seq=%d", data.From, data.ClientMsg.Id, data.Result.Success, data.Result.Error, data.Result.ExecutedSeqNum))
	// Block := core.NewBlock(data.SequenceNumber, data.RequestMessage.Txs, data.RequestMessage.To, data.RequestMessage.Timestamp)
	// Block.AddCommittedNode(data.From)
	// core.Chain.AddBlock(Block)
	go c.TransactionManager.ReplyTxn(data)
}
