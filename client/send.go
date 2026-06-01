package client

import (
	"math/big"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/crypto"
	"github.com/michael112233/pbft/transportpb"
	"google.golang.org/protobuf/proto"
)

const clientSendInterval = 50 * time.Millisecond

func GenerateDummyTxs(count int) []*core.Transaction {
	txs := make([]*core.Transaction, count)
	for i := 0; i < count; i++ {
		txs[i] = GenerateDummyTx(int64(i))
	}
	return txs
}

func GenerateDummyTx(id int64) *core.Transaction {
	return core.NewTransaction(
		string(rune('A'+id%26)),
		string(rune('A'+(id+1)%26)),
		big.NewInt(1),
	)
}

func signedTxQueueCapacity(batchSize int64) int {
	if batchSize <= 0 {
		return 1
	}
	return int(batchSize) * 200
}

func signerWorkerCount() int {
	workers := runtime.NumCPU()
	if workers < 1 {
		return 1
	}
	return workers
}

func (c *Client) startSignedTxPipeline(totalTxs int64, padding string, queueCapacity int, workerCount int) <-chan core.ClientMsgSignature {
	if queueCapacity < 1 {
		queueCapacity = 1
	}
	if workerCount < 1 {
		workerCount = 1
	}

	signedTxs := make(chan core.ClientMsgSignature, queueCapacity)
	var nextID atomic.Int64
	var workers sync.WaitGroup

	for w := 0; w < workerCount; w++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				id := nextID.Add(1) - 1
				if id >= totalTxs {
					return
				}

				clientMsg := core.ClientMsg{
					Id:         id,
					Timestamp:  time.Now().UnixNano(),
					Txn:        GenerateDummyTx(id),
					ClientName: c.name,
					Padding:    padding,
				}

				clientMsgBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(transportpb.ClientMsgToPB(clientMsg))
				if err != nil {
					if c.log != nil {
						c.log.Error("failed to marshal client message for signing: %v", err)
					}
					continue
				}

				signedTxs <- core.ClientMsgSignature{
					Data:      clientMsg,
					Signature: crypto.SignMessageEd25519(clientMsgBytes, c.privateKey),
				}
			}
		}()
	}

	go func() {
		workers.Wait()
		close(signedTxs)
	}()

	return signedTxs
}

func collectSignedBatch(signedTxs <-chan core.ClientMsgSignature, batchSize int) ([]core.ClientMsgSignature, bool) {
	if batchSize <= 0 {
		return nil, false
	}

	batch := make([]core.ClientMsgSignature, 0, batchSize)
	for len(batch) < batchSize {
		msg, ok := <-signedTxs
		if !ok {
			return batch, len(batch) > 0
		}
		batch = append(batch, msg)
	}
	return batch, true
}

func (c *Client) InjectTxs() {
	c.WaitGroup.Add(1)
	go func() {
		defer c.WaitGroup.Done()

		totaltxns := c.config.Period * 5
		if totaltxns <= 0 {
			c.log.Info("No transactions to inject")
			return
		}
		if c.config.InjectSpeed <= 0 {
			c.log.Error("invalid inject_speed %d; must be > 0", c.config.InjectSpeed)
			return
		}

		paddingBytes := c.config.ClientMsgPaddingBytes
		if paddingBytes < 0 {
			paddingBytes = 0
		}
		padding := strings.Repeat("x", paddingBytes)
		batchSize := int(c.config.InjectSpeed)
		signedTxs := c.startSignedTxPipeline(
			totaltxns,
			padding,
			signedTxQueueCapacity(c.config.InjectSpeed),
			signerWorkerCount(),
		)

		batch, ok := collectSignedBatch(signedTxs, batchSize)
		if !ok {
			c.log.Info("No signed transactions generated")
			return
		}

		c.TransactionManager.Start()
		injected := int64(0)
		for ok {
			timestart := time.Now()

			injected += int64(len(batch))
			if injected == int64(len(batch)) || injected%10000 == 0 || injected == totaltxns {
				c.log.Info("upto %d transactions injected", injected)
			}

			// c.TransactionManager.StartTimer()
			c.TransactionManager.AddTransaction(batch)

			msg := core.RequestMessage{

				Txs: batch,
			}

			for {
				c.leaderMu.RLock()
				leader := c.leaderAddr
				c.leaderMu.RUnlock()

				// c.log.Info(fmt.Sprintf("Send request message to %s with batch %d and %d transactions", leader, int64(i), len(injectTxs)))
				c.messageHub.Send(core.MsgRequestMessage, c.addr, leader, msg, nil) // couuld be go as stream locked

				// vcStatus := <-c.vcrunChan
				if true {
					// c.log.Info("Received view change not running status, moving to next batch")
					if remaining := clientSendInterval - time.Since(timestart); remaining > 0 {
						time.Sleep(remaining)
					}
					break
				}

				// c.log.Info("Received view change running status with %d transactions in flight, pausing injection until view change completes", len(vcStatus.Txs))
				<-c.cchan                         // wait for signal to continue injection after view change completes
				time.Sleep(50 * time.Millisecond) // small sleep to allow system to stabilize after view change before retry
			}
			timetaken := time.Since(timestart)
			if timetaken > clientSendInterval {
				c.log.Info("Injected %d transactions in %s; signer/send path missed %s interval", len(batch), timetaken, clientSendInterval)
			} else {
				c.log.Info("Injected %d transactions in %s", len(batch), timetaken)
			}

			collectStart := time.Now()
			batch, ok = collectSignedBatch(signedTxs, batchSize)
			if wait := time.Since(collectStart); ok && wait > 5*time.Millisecond {
				c.log.Info("Waited %s for signed transaction batch; signer pipeline may be bottlenecked", wait)
			}
		}
	}()
}
