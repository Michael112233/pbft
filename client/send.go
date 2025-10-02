package client

import (
	"fmt"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/result"
)

func (c *Client) InjectTxs() {
	result.SetStartTime(time.Now())
	c.WaitGroup.Add(1)
	go func() {
		defer c.WaitGroup.Done()
		var injectTxs []*core.Transaction
		for i := int64(0); (i+1)*c.config.MaxBlockSize < int64(len(c.txs)); i++ {
			injectTxs = c.txs[i*c.config.MaxBlockSize : (i+1)*c.config.MaxBlockSize]
			leader := c.leaderElection.GetLeader(c.currentView)
			msg := core.RequestMessage{
				Timestamp: time.Now().Unix(),
				From:      c.addr,
				To:        leader,
				Txs:       injectTxs,
				Id:        int64(i),
			}
			c.messageHub.Send(core.MsgRequestMessage, c.addr, msg, nil)
			if (i+1)%c.injectSpeed == 0 {
				time.Sleep(1 * time.Second)
			}
			c.log.Info(fmt.Sprintf("Send request message to %s with %d transactions", leader, int64(i)))
		}
	}()
}

func (c *Client) BroadcastClose() {
	for _, addr := range config.NodeAddr {
		closeMsg := core.CloseMessage{
			Timestamp: time.Now().Unix(),
			From:      c.addr,
			To:        addr,
		}
		c.log.Info(fmt.Sprintf("Send close message to %s", addr))
		c.messageHub.Send(core.MsgCloseMessage, addr, closeMsg, nil)
	}
}
