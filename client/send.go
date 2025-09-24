package client

import (
	"time"

	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/result"
)

func (c *Client) InjectTxs() {
	result.SetStartTime(time.Now())
	c.WaitGroup.Add(1)
	go func() {
		defer c.WaitGroup.Done()
		var injectTxs []*core.Transaction
		for i := int64(0); (i+1)*c.injectSpeed <= int64(len(c.txs)); i++ {
			injectTxs = c.txs[i*c.injectSpeed : (i+1)*c.injectSpeed]
			leader := c.leaderElection.GetLeader(c.currentView)
			msg := core.RequestMessage{
				Timestamp: time.Now().Unix(),
				From:      c.addr,
				To:        leader,
				Txs:       injectTxs,
				Id:        int64(i),
			}
			c.messageHub.Send(core.MsgRequestMessage, c.addr, msg, nil)
			time.Sleep(1 * time.Second)
		}
	}()
}
