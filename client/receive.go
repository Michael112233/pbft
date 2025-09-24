package client

import (
	"fmt"

	"github.com/michael112233/pbft/core"
)

func (c *Client) HandleReplyMessage(data core.ReplyMessage) {
	c.log.Info(fmt.Sprintf("Received reply message from %s, sequence number %d", data.From, data.SequenceNumber))
	Block := core.NewBlock(data.SequenceNumber, data.RequestMessage.Txs, data.RequestMessage.To)
	Block.AddCommittedNode(data.From)
	core.Chain.AddBlock(Block)
}
