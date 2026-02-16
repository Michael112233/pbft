package client

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"math/big"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/crypto"
	"github.com/michael112233/pbft/result"
)

func GenerateDummyTxs(count int) []*core.Transaction {
	txs := make([]*core.Transaction, count)
	for i := 0; i < count; i++ {
		txs[i] = core.NewTransaction(
			"sender_"+string(rune('A'+i%26)),
			"receiver_"+string(rune('A'+(i+1)%26)),
			big.NewInt(1),
		)
	}
	return txs
}

func (c *Client) InjectTxs() {
	c.WaitGroup.Add(1)
	go func() {
		defer c.WaitGroup.Done()
		result.SetStartTime(time.Now())

		// Create signed ClientMsgSignature array for all transactions
		txns := GenerateDummyTxs(48000)
		signedMsgs := make([]core.ClientMsgSignature, len(txns))
		for i, tx := range txns {
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
		for i := int64(0); (i+1)*4000 <= int64(len(txns)); i++ {
			injectTxs = signedMsgs[i*4000 : (i+1)*4000] //c.txs[i*c.config.InjectSpeed : (i+1)*c.config.InjectSpeed]
			// leader := c.leaderElection.GetLeader(c.currentView)
			leader := config.NodeAddr[1]
			go c.TransactionManager.AddTransaction(injectTxs)
			c.TransactionManager.Start()
			msg := core.RequestMessage{
				// Timestamp: time.Now().UnixNano(),

				Txs: injectTxs,
				// Id:        int64(i),
			}
			c.log.Info(fmt.Sprintf("Send request message to %s with %d transactions", leader, int64(i)))

			c.messageHub.Send(core.MsgRequestMessage, c.addr, leader, msg, nil)
			// if ((i+1)*c.config.InjectSpeed)%c.injectSpeed == 0 {
			time.Sleep(1 * time.Second)
			// }

		}
	}()
}

// func (c *Client) BroadcastClose() {
// 	for _, addr := range config.NodeAddr {
// 		closeMsg := core.CloseMessage{
// 			Timestamp: time.Now().UnixNano(),
// 			From:      c.addr,
// 			To:        addr,
// 		}
// 		c.log.Info(fmt.Sprintf("Send close message to %s", addr))
// 		c.messageHub.Send(core.MsgCloseMessage, c.addr, addr, closeMsg, nil)
// 	}
// }
