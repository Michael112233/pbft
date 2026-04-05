package client

import (
	"fmt"
	"math/big"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/crypto"
	"github.com/michael112233/pbft/result"
	"github.com/michael112233/pbft/transportpb"
	"google.golang.org/protobuf/proto"
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
		txns := GenerateDummyTxs(4)
		signedMsgs := make([]core.ClientMsgSignature, len(txns))
		for i, tx := range txns {
			clientMsg := core.ClientMsg{
				Id:         int64(i),
				Timestamp:  time.Now().UnixNano(),
				Txn:        tx,
				ClientName: c.name,
			}

			// Serialize ClientMsg deterministically via protobuf for signing.
			clientMsgBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(transportpb.ClientMsgToPB(clientMsg))
			if err != nil {
				c.log.Error("failed to marshal client message for signing: %v", err)
				continue
			}
			signature := crypto.SignMessageEd25519(clientMsgBytes, c.privateKey)

			signedMsgs[i] = core.ClientMsgSignature{
				Data:      clientMsg,
				Signature: signature,
			}
		}

		var injectTxs []core.ClientMsgSignature
		c.TransactionManager.Start()
		// c.log.Info("Starting to inject transactions...")
		for i := int64(0); (i+1)*1 <= int64(len(txns)); i++ {
			// c.log.Info(fmt.Sprintf("Injecting transaction batch %d", i))
			injectTxs = signedMsgs[i*1 : (i+1)*1] //c.txs[i*c.config.InjectSpeed : (i+1)*c.config.InjectSpeed]
			// leader := c.leaderElection.GetLeader(c.currentView)
			leader := config.NodeAddr[1]
			c.TransactionManager.StartTimer()
			go c.TransactionManager.AddTransaction(injectTxs)

			msg := core.RequestMessage{
				// Timestamp: time.Now().UnixNano(),

				Txs: injectTxs,
				// Id:        int64(i),
			}
			if i == 3 {
				c.TransactionManager.TemporaryStopTimer()
				// ask user input
				fmt.Println("Press Enter to continue...")
				fmt.Scanln()
				c.TransactionManager.ResetTimer()
			}
			c.log.Info(fmt.Sprintf("Send request message to %s with %d transactions", leader, int64(i)))

			c.messageHub.Send(core.MsgRequestMessage, c.addr, leader, msg, nil) // couuld be go as stream locked
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
