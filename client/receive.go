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
	current_latency := time.Now().Sub(time.Unix(data.RequestMessage.Timestamp, 0)).Milliseconds()
	result.AddLatency(float64(current_latency))
	go core.Chain.AddBlock(Block)
}
