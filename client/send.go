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

const clientSendInterval = 24 * time.Millisecond

// 1000/clientsned * injectspeed = rate
const (
	normalRequestMessageType = "RequestMessage"
	retryRequestMessageType  = "RetryRequestMessage"
)

func GenerateDummyTxs(count int) []core.Transaction {
	txs := make([]core.Transaction, count)
	for i := 0; i < count; i++ {
		txs[i] = GenerateDummyTx(int64(i))
	}
	return txs
}

func GenerateDummyTx(id int64) core.Transaction {
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
					Timestamp:  time.Now(),
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

func (c *Client) TotalTxnsToInject() int64 {
	return c.config.Period * int64(c.config.NumberOfPeriods)
}

func (c *Client) InjectTxs() {
	c.WaitGroup.Add(1)
	go func() {
		defer c.WaitGroup.Done()

		totaltxns := c.TotalTxnsToInject()
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
			injected += int64(len(batch))
			if injected == int64(len(batch)) || injected%10000 == 0 || injected == totaltxns {
				c.log.Info("upto %d transactions injected", injected)
			}

			createdAt := time.Now()
			// Register inside the pacer callback, right before the send, so the
			// pacer's slot wait is reported as client queueing, not latency.
			c.pacedSendRequestTransactions(batch, normalRequestMessageType, func(sent []core.ClientMsgSignature) {
				c.TransactionManager.AddTransaction(sent, createdAt)
			})
			// c.sendRequestTransactions(batch, normalRequestMessageType)

			collectStart := time.Now()
			batch, ok = collectSignedBatch(signedTxs, batchSize)
			if wait := time.Since(collectStart); ok && wait > 5*time.Millisecond {
				c.log.Info("Waited %s for signed transaction batch; signer pipeline may be bottlenecked", wait)
			}
		}
		if c.config.CompleteSuite {
			c.TransactionManager.StartRetryTimer(false)
		}
	}()
}

func (c *Client) sendTransactions(txs []core.ClientMsgSignature) {
	c.sendRequestTransactions(txs, retryRequestMessageType)
}

func forEachTransactionBatch(txs []core.ClientMsgSignature, batchSize int, visit func([]core.ClientMsgSignature)) {
	if batchSize <= 0 || visit == nil {
		return
	}

	for start := 0; start < len(txs); start += batchSize {
		end := start + batchSize
		if end > len(txs) {
			end = len(txs)
		}
		visit(txs[start:end])
	}
}

func (c *Client) sendRequestTransactions(txs []core.ClientMsgSignature, requestMessageType string) {
	c.pacedSendRequestTransactions(txs, requestMessageType, nil)
}

// pacedSendRequestTransactions sends txs in paced batches. beforeSend, if set, runs
// under the pacer right before each batch goes out (after any slot wait); the
// normal path uses it to register the batch, the retry path passes nil so retried
// transactions keep their registration and retry count.
func (c *Client) pacedSendRequestTransactions(txs []core.ClientMsgSignature, requestMessageType string, beforeSend func([]core.ClientMsgSignature)) {
	//retry path never send less than zero
	if len(txs) == 0 {
		return
	}
	// never happen
	if c.config == nil || c.config.InjectSpeed <= 0 {
		if c.log != nil {
			c.log.Error("invalid inject_speed; must be > 0")
		}
		return
	}

	batchSize := int(c.config.InjectSpeed)
	// creates batches of batch size if txns len greater which usually happen when coming from retry path
	forEachTransactionBatch(txs, batchSize, func(batch []core.ClientMsgSignature) {
		c.requestPacer.pace(len(batch), batchSize, clientSendInterval, c.log, func() {
			c.leaderMu.RLock()
			leader := c.leaderAddr
			c.leaderMu.RUnlock()

			// Registered before the enqueue, as before: the latency clock starts here
			// and the entry exists before any reply for it could arrive.
			if beforeSend != nil { // retry send nil
				beforeSend(batch)
			}
			// non-blocking: the node's sender goroutine does the stream write, so a
			// node that stops reading cannot hold the pacer (sendqueue.go). A batch
			// dropped on a full queue stays registered when a retry path can resend
			// it; otherwise it is forgotten and counted as dropped_unsent.
			enqueued := c.messageHub.EnqueueRequest(leader, core.RequestMessage{Txs: batch, MsgType: requestMessageType})
			if !enqueued && beforeSend != nil && c.TransactionManager != nil {
				c.TransactionManager.ForgetUnsent(batch)
			}
		})
	})
}

// logFirstSendToNewLeader logs, once per leader change, how long after the leader
// update the first request to the new leader finished sending (STALL monitoring).
// Called by that node's sender goroutine after each successful request send.
func (c *Client) logFirstSendToNewLeader(sentTo string) {
	c.leaderMu.Lock()
	defer c.leaderMu.Unlock()
	if c.leaderChangedAt.IsZero() || sentTo != c.leaderAddr {
		return
	}
	c.log.Info("STALL: first request to new leader %s sent %v after the leader update", sentTo, time.Since(c.leaderChangedAt))
	c.leaderChangedAt = time.Time{}
}

func (c *Client) sendEventMsg(leaderAddr string) {
	c.messageHub.Send(
		core.MsgEventMessage,
		c.addr,
		leaderAddr,
		core.EventMsg{EventType: "LeaderStall"},
		nil,
	)

}
