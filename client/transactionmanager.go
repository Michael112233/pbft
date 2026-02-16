package client

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/michael112233/pbft/core"
)

const numShards = 64

type transactionDetails struct {
	mu              sync.Mutex // per-txn lock for long operations
	clientMsg       core.ClientMsg
	startTimestamp  int64
	finishTimestamp int64
	latency         int64
	done            bool
}

type shard struct {
	mu   sync.RWMutex
	txns map[int64]*transactionDetails
}

type TransactionManager struct {
	shards      [numShards]shard
	txnCommited atomic.Int64
	startTime   int64

	tpsMu       sync.RWMutex
	averageTps  float64
	elapsedTime float64
}

func NewTransactionManager() *TransactionManager {
	tm := &TransactionManager{}
	for i := range tm.shards {
		tm.shards[i].txns = make(map[int64]*transactionDetails)
	}
	return tm
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
			clientMsg:      msgSig.Data,
			startTimestamp: time.Now().UnixNano(),
			done:           false,
		}
		s.mu.Unlock()
	}
}

// right now for each reply will spawn a go routine
// can have a channel and batch for few second and then process the batch of reply messages
func (tm *TransactionManager) ReplyTxn(msg core.ClientMsg) {
	s := tm.getShard(msg.Id)

	// Short shard lock just to grab the txn pointer
	s.mu.RLock()
	txn, ok := s.txns[msg.Id]
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
	txn.finishTimestamp = time.Now().UnixNano()
	txn.latency = txn.finishTimestamp - txn.startTimestamp
	txn.done = true
	txnsCommitted := tm.txnCommited.Add(1)

	// Set start time on first commit (CompareAndSwap ensures only first call succeeds)

	if txnsCommitted%100 == 0 {
		tm.tpsMu.Lock()
		tm.elapsedTime = float64(time.Now().UnixNano()-tm.startTime) / 1e9
		tm.averageTps = float64(txnsCommitted) / tm.elapsedTime
		tm.tpsMu.Unlock()
	}

}
