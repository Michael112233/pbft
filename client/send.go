package client

import (
	"fmt"
	"math/big"
	"time"

	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/crypto"
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

		// Create signed ClientMsgSignature array for all transactions
		txns := GenerateDummyTxs(int(c.config.Period) * 10)
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

		for i := int64(0); (i+1)*int64(c.config.InjectSpeed) <= int64(len(txns)); i++ {

			injectTxs = signedMsgs[i*int64(c.config.InjectSpeed) : (i+1)*int64(c.config.InjectSpeed)]

			// c.TransactionManager.StartTimer()
			go c.TransactionManager.AddTransaction(injectTxs)

			msg := core.RequestMessage{
				// Timestamp: time.Now().UnixNano(),

				Txs: injectTxs,
				// Id:        int64(i),
			}
			// if i == 3 {
			// 	c.TransactionManager.TemporaryStopTimer()
			// 	// ask user input
			// 	fmt.Println("Press Enter to continue...")
			// 	fmt.Scanln()
			// 	c.TransactionManager.ResetTimer()
			// }
			for {
				c.leaderMu.RLock()
				leader := c.leaderAddr
				c.leaderMu.RUnlock()

				c.log.Info(fmt.Sprintf("Send request message to %s with batch %d and %d transactions", leader, int64(i), len(injectTxs)))
				c.messageHub.Send(core.MsgRequestMessage, c.addr, leader, msg, nil) // couuld be go as stream locked

				vcStatus := <-c.vcrunChan
				if !vcStatus.VCRunning {
					c.log.Info("Received view change not running status, moving to next batch")
					time.Sleep(500 * time.Millisecond) // small sleep to allow system to stabilize before next wave
					break
				}

				c.log.Info("Received view change running status with %d transactions in flight, pausing injection until view change completes", len(vcStatus.Txs))
				<-c.cchan                   // wait for signal to continue injection after view change completes
				time.Sleep(1 * time.Second) // small sleep to allow system to stabilize after view change before retry
			}

			// Wait for the next leader update before sending the next periodic wave.
			// if c.config.Periodic {
			// 	lastWave := (i+1)*int64(c.config.Period) >= int64(len(txns))
			// 	if !lastWave {
			// 		<-c.cchan
			// 	}
			// 	time.Sleep(1 * time.Second) // small sleep to allow system to stabilize after leader change before next wave
			// } else {
			// 	time.Sleep(1 * time.Second)

			// }
			// }

		}
	}()
}
