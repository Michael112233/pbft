package client

import (
	"fmt"
	"time"

	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/result"
)

func (c *Client) HandleReplyMessage(data core.ReplyMessage) {
	c.log.Info(fmt.Sprintf("Received reply message from %s, sequence number %d", data.From, data.SequenceNumber))
	Block := core.NewBlock(data.SequenceNumber, data.RequestMessage.Txs, data.RequestMessage.To)
	Block.AddCommittedNode(data.From)
	core.Chain.AddBlock(Block)
	result.SetEndTime(time.Now())
}
