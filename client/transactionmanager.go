package client

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/michael112233/pbft/core"
)

const (
	numShards            = 64
	txnGCRetentionWindow = 20000
)

type transactionDetails struct {
	mu              sync.Mutex // per-txn lock for long operations
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

type TPSPoint struct {
	TimestampUnixNano int64   `json:"timestamp_unix_nano"`
	ElapsedSec        float64 `json:"elapsed_sec"`
	CommittedTotal    int64   `json:"committed_total"`
	WindowTPS         float64 `json:"window_tps"`
	AverageTPS        float64 `json:"average_tps"`
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
	tpsSeries               []TPSPoint
	lastSampleTime          int64
	lastSampleCommitted     int64
	tpsSampleInterval       time.Duration
	tpsSamplerStopCh        chan struct{}
	tpsSamplerRunning       atomic.Bool
	tpsSamplerStopOnce      sync.Once

	latencyMu      sync.Mutex // later will have better fix and concurrent
	latencySamples []int64
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
		tpsSeries:              make([]TPSPoint, 0),
		tpsSampleInterval:      200 * time.Millisecond,
		tpsSamplerStopCh:       make(chan struct{}),
	}
	for i := range tm.shards {
		tm.shards[i].txns = make(map[int64]*transactionDetails)
	}
	return tm
}

func (tm *TransactionManager) TransactionTimerWorker(_ *Client) {

	for {
		select {
		case <-tm.transactionTimer.C:
			// Retry is intentionally disabled for high-volume padded benchmarks:
			// transactionDetails stores lightweight metadata only, not the full payload.
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
	tm.stopTPSSampler()
}

func (tm *TransactionManager) Start() {
	start := time.Now().UnixNano()
	tm.startTime = start
	tm.tpsMu.Lock()
	tm.lastSampleTime = start
	tm.lastSampleCommitted = tm.txnCommited.Load()
	tm.elapsedTime = 0
	tm.averageTps = 0
	tm.tpsMu.Unlock()
	if tm.tpsSamplerRunning.CompareAndSwap(false, true) {
		go tm.tpsSamplerWorker()
	}
}

func (tm *TransactionManager) getShard(id int64) *shard {
	return &tm.shards[uint64(id)%numShards]
}

func (tm *TransactionManager) GetThroughput() (tps float64, elapsed float64, txnCommited int64) {
	tm.captureTPSSample(time.Now())
	tm.tpsMu.RLock()
	defer tm.tpsMu.RUnlock()
	return tm.averageTps, tm.elapsedTime, tm.txnCommited.Load()

}

func (tm *TransactionManager) AddTransaction(batch []core.ClientMsgSignature) {
	for _, msgSig := range batch {
		s := tm.getShard(msgSig.Data.Id)
		s.mu.Lock()
		s.txns[msgSig.Data.Id] = &transactionDetails{
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
	if txn.committed {
		txn.mu.Unlock()
		return
	}

	txn.finishTimestamp = time.Now().UnixNano()
	txn.latency = txn.finishTimestamp - txn.startTimestamp
	txn.committed = true
	tm.txnCommited.Add(1)
	tm.latencyMu.Lock()
	tm.latencySamples = append(tm.latencySamples, txn.latency)
	tm.latencyMu.Unlock()
	txn.mu.Unlock()

	if reply.ClientMsg.Id > txnGCRetentionWindow && reply.ClientMsg.Id%30000 == 0 {
		tm.GCTxns(reply.ClientMsg.Id - txnGCRetentionWindow)
	}
}

func (tm *TransactionManager) GCTxns(cutoff int64) {
	for i := range tm.shards {
		s := &tm.shards[i]
		s.mu.Lock()
		for id := range s.txns {
			if id < cutoff {
				delete(s.txns, id)
			}
		}
		s.mu.Unlock()
	}
}

func (tm *TransactionManager) tpsSamplerWorker() {
	ticker := time.NewTicker(tm.tpsSampleInterval)
	defer ticker.Stop()

	for {
		select {
		case now := <-ticker.C:
			tm.captureTPSSample(now)
		case <-tm.tpsSamplerStopCh:
			return
		}
	}
}

func (tm *TransactionManager) stopTPSSampler() {
	tm.tpsSamplerRunning.Store(false)
	tm.tpsSamplerStopOnce.Do(func() {
		close(tm.tpsSamplerStopCh)
	})
}

func (tm *TransactionManager) captureTPSSample(now time.Time) {
	startTime := tm.startTime
	if startTime == 0 {
		return
	}

	tm.tpsMu.Lock()
	defer tm.tpsMu.Unlock()

	totalCommitted := tm.txnCommited.Load()
	elapsedSec := float64(now.UnixNano()-startTime) / 1e9
	if elapsedSec < 0 {
		elapsedSec = 0
	}

	lastSampleTime := tm.lastSampleTime
	if lastSampleTime == 0 {
		lastSampleTime = startTime
	}
	deltaSec := float64(now.UnixNano()-lastSampleTime) / 1e9
	deltaCommitted := totalCommitted - tm.lastSampleCommitted

	windowTPS := 0.0
	if deltaSec > 0 {
		windowTPS = float64(deltaCommitted) / deltaSec
	}

	averageTPS := 0.0
	if elapsedSec > 0 {
		averageTPS = float64(totalCommitted) / elapsedSec
	}

	tm.elapsedTime = elapsedSec
	tm.averageTps = averageTPS
	tm.tpsSeries = append(tm.tpsSeries, TPSPoint{
		TimestampUnixNano: now.UnixNano(),
		ElapsedSec:        elapsedSec,
		CommittedTotal:    totalCommitted,
		WindowTPS:         windowTPS,
		AverageTPS:        averageTPS,
	})
	tm.lastSampleTime = now.UnixNano()
	tm.lastSampleCommitted = totalCommitted
}

func (tm *TransactionManager) ExportTPSSeries(path string) error {
	tm.captureTPSSample(time.Now())

	tm.tpsMu.RLock()
	points := make([]TPSPoint, len(tm.tpsSeries))
	copy(points, tm.tpsSeries)
	tm.tpsMu.RUnlock()

	data, err := json.MarshalIndent(points, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

type LatencySummaryResult struct {
	Count int     `json:"count"`
	AvgMs float64 `json:"avg_ms"`
	P50Ms float64 `json:"p50_ms"`
	P95Ms float64 `json:"p95_ms"`
	P99Ms float64 `json:"p99_ms"`
}

func (tm *TransactionManager) LatencySummary(path string) error {
	tm.latencyMu.Lock()
	samples := append([]int64(nil), tm.latencySamples...)
	tm.latencyMu.Unlock()

	if len(samples) == 0 {
		data, err := json.MarshalIndent(LatencySummaryResult{}, "", "  ")
		if err != nil {
			return err
		}
		return os.WriteFile(path, data, 0o644)
	}

	sort.Slice(samples, func(i, j int) bool {
		return samples[i] < samples[j]
	})

	var total float64
	for _, latency := range samples {
		total += float64(latency)
	}

	result := LatencySummaryResult{
		Count: len(samples),
		AvgMs: nanosToMs(int64(total / float64(len(samples)))),
		P50Ms: nanosToMs(percentileLatency(samples, 0.50)),
		P95Ms: nanosToMs(percentileLatency(samples, 0.95)),
		P99Ms: nanosToMs(percentileLatency(samples, 0.99)),
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func percentileLatency(sorted []int64, p float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(p*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func nanosToMs(ns int64) float64 {
	return float64(ns) / 1e6
}
