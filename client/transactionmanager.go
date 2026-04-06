package client

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
)

const numShards = 64

type transactionDetails struct {
	mu              sync.Mutex // per-txn lock for long operations
	clientMsg       core.ClientMsgSignature
	startTimestamp  int64
	finishTimestamp int64
	latency         int64
	done            bool
	committed       bool
}

type shard struct {
	mu   sync.RWMutex
	txns map[int64]*transactionDetails
}

type TransactionManager struct {
	shards      [numShards]shard
	txnCommited atomic.Int64
	startTime   int64

	tpsMu                   sync.RWMutex
	averageTps              float64
	elapsedTime             float64
	transactionTimer        *time.Timer
	transactionTimerStopCh  chan struct{}
	transactionTimerRunning atomic.Bool
	retryx                  bool
}

func NewTransactionManager() *TransactionManager {
	transactionTimer := time.NewTimer(5 * time.Second)
	if !transactionTimer.Stop() {
		select {
		case <-transactionTimer.C:
		default:
		}
	}
	tm := &TransactionManager{
		transactionTimer:       transactionTimer,
		transactionTimerStopCh: make(chan struct{}),
		retryx:                 false,
	}
	for i := range tm.shards {
		tm.shards[i].txns = make(map[int64]*transactionDetails)
	}
	return tm
}

func (tm *TransactionManager) TransactionTimerWorker(c *Client) {

	for {
		select {
		case <-tm.transactionTimer.C:
			// tm.handlePBFTTimerExpiry(n)

			for i := range tm.shards {
				s := &tm.shards[i]
				s.mu.RLock()
				for _, txn := range s.txns {
					txn.mu.Lock()
					if !txn.done && time.Since(time.Unix(0, txn.startTimestamp)) > 4*time.Second {
						txn.startTimestamp = time.Now().UnixNano() // reset start time to now for next timeout check
						// Here we can also implement retry logic if needed
						msg := core.RequestMessage{
							// Timestamp: time.Now().UnixNano(),

							Txs: []core.ClientMsgSignature{txn.clientMsg},
							// Id:        int64(i),
						}

						if !tm.retryx { // only retry once for simplicity
							c.log.Info(fmt.Sprintf("Retrying transaction ID %d due to timeout", txn.clientMsg.Data.Id))
							for _, nodeAddr := range config.NodeAddr {
								c.messageHub.Send(core.MsgRequestMessage, c.addr, nodeAddr, msg, nil)
							}
						}
						tm.retryx = true
					}
					txn.mu.Unlock()

				}
				s.mu.RUnlock()
			}
			tm.transactionTimer.Reset(10 * time.Second)
		case <-tm.transactionTimerStopCh:
			return
		}
	}
}

func (tm *TransactionManager) StartTimer() {
	tm.transactionTimerRunning.Store(true)
	tm.transactionTimer.Reset(5 * time.Second)
}
func (tm *TransactionManager) ResetTimer() {
	if !tm.transactionTimer.Stop() {
		select {
		case <-tm.transactionTimer.C:
		default:
		}
	}
	tm.transactionTimer.Reset(5 * time.Second)
}
func (tm *TransactionManager) TemporaryStopTimer() {
	tm.transactionTimerRunning.Store(false)
	if !tm.transactionTimer.Stop() {
		select {
		case <-tm.transactionTimer.C:
		default:
		}
	}
}

func (tm *TransactionManager) StopTimer() {
	tm.transactionTimerRunning.Store(false)
	if !tm.transactionTimer.Stop() {
		select {
		case <-tm.transactionTimer.C:
		default:
		}
	}
	close(tm.transactionTimerStopCh)
}

func (tm *TransactionManager) Start() {
	tm.startTime = time.Now().UnixNano()
}

func (tm *TransactionManager) getShard(id int64) *shard {
	return &tm.shards[uint64(id)%numShards]
}

func (tm *TransactionManager) GetThroughput() (tps float64, elapsed float64, txnCommited int64) {
	tm.tpsMu.RLock()
	defer tm.tpsMu.RUnlock()
	return tm.averageTps, tm.elapsedTime, tm.txnCommited.Load()

}

func (tm *TransactionManager) AddTransaction(batch []core.ClientMsgSignature) {
	for _, msgSig := range batch {
		s := tm.getShard(msgSig.Data.Id)
		s.mu.Lock()
		s.txns[msgSig.Data.Id] = &transactionDetails{
			clientMsg:      msgSig,
			startTimestamp: time.Now().UnixNano(),
			done:           false,
		}
		s.mu.Unlock()
	}
}

// right now for each reply will spawn a go routine
// can have a channel and batch for few second and then process the batch of reply messages
func (tm *TransactionManager) ReplyTxn(reply core.ReplyMessage) {
	s := tm.getShard(reply.ClientMsg.Id)

	// Short shard lock just to grab the txn pointer
	s.mu.RLock()
	txn, ok := s.txns[reply.ClientMsg.Id]
	s.mu.RUnlock()
	if !ok {
		return
	}

	// Per-txn lock for longer operations - doesn't block other txns in shard
	txn.mu.Lock()
	defer txn.mu.Unlock()
	if txn.done {
		return
	}
	if !reply.Result.Success {
		fmt.Printf("transaction %d rejected: %s\n", reply.ClientMsg.Id, reply.Result.Error)
	}
	txn.done = true

}

func (tm *TransactionManager) CommitTps(reply core.CommitTps) {
	s := tm.getShard(reply.ClientMsg.Id)

	// Short shard lock just to grab the txn pointer
	s.mu.RLock()
	txn, ok := s.txns[reply.ClientMsg.Id]
	s.mu.RUnlock()
	if !ok {
		return
	}

	// Per-txn lock for longer operations - doesn't block other txns in shard
	txn.mu.Lock()
	defer txn.mu.Unlock()
	if txn.committed {
		return
	}

	txn.finishTimestamp = time.Now().UnixNano()
	txn.latency = txn.finishTimestamp - txn.startTimestamp
	txn.committed = true
	txnsCommitted := tm.txnCommited.Add(1)

	// Set start time on first commit (CompareAndSwap ensures only first call succeeds)

	if txnsCommitted%100 == 0 {
		tm.tpsMu.Lock()
		tm.elapsedTime = float64(time.Now().UnixNano()-tm.startTime) / 1e9
		tm.averageTps = float64(txnsCommitted) / tm.elapsedTime
		tm.tpsMu.Unlock()
	}

}
