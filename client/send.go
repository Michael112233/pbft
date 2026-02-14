package client

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/crypto"
	"github.com/michael112233/pbft/result"
)

func (c *Client) InjectTxs() {
	c.WaitGroup.Add(1)
	go func() {
		defer c.WaitGroup.Done()
		result.SetStartTime(time.Now())

		// Create signed ClientMsgSignature array for all transactions
		signedMsgs := make([]core.ClientMsgSignature, len(c.txs))
		for i, tx := range c.txs {
			clientMsg := core.ClientMsg{
				Id:         int64(i),
				Timestamp:  time.Now().UnixNano(),
				Txn:        tx,
				ClientName: c.name,
			}

			// Serialize ClientMsg for signing
			var buf bytes.Buffer
			gob.NewEncoder(&buf).Encode(clientMsg)
			signature := crypto.SignMessageEd25519(buf.Bytes(), c.privateKey)

			signedMsgs[i] = core.ClientMsgSignature{
				Data:      clientMsg,
				Signature: signature,
			}
		}

		var injectTxs []core.ClientMsgSignature
		for i := int64(0); (i+1)*c.config.InjectSpeed < int64(len(c.txs)); i++ {
			injectTxs = signedMsgs[i*c.config.InjectSpeed : (i+1)*c.config.InjectSpeed] //c.txs[i*c.config.InjectSpeed : (i+1)*c.config.InjectSpeed]
			leader := c.leaderElection.GetLeader(c.currentView)
			msg := core.RequestMessage{
				// Timestamp: time.Now().UnixNano(),

				Txs: injectTxs,
				// Id:        int64(i),
			}
			c.messageHub.Send(core.MsgRequestMessage, c.addr, leader, msg, nil)
			if ((i+1)*c.config.InjectSpeed)%c.injectSpeed == 0 {
				time.Sleep(1 * time.Second)
			}
			c.log.Info(fmt.Sprintf("Send request message to %s with %d transactions", leader, int64(i)))
		}
	}()
}

func (c *Client) BroadcastClose() {
	for _, addr := range config.NodeAddr {
		closeMsg := core.CloseMessage{
			Timestamp: time.Now().UnixNano(),
			From:      c.addr,
			To:        addr,
		}
		c.log.Info(fmt.Sprintf("Send close message to %s", addr))
		c.messageHub.Send(core.MsgCloseMessage, c.addr, addr, closeMsg, nil)
	}
}
