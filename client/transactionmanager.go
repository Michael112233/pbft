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
	"github.com/michael112233/pbft/logger"
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
	clientMsgSig    core.ClientMsgSignature
	nextRetryTime   time.Time
	retryCount      int
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
	shards              [numShards]shard
	txnCommited         atomic.Int64
	startTime           int64
	log                 *logger.Logger
	tpsMu               sync.RWMutex
	averageTps          float64
	elapsedTime         float64
	txnRetryManager     *TransactionRetryManager
	tpsSeries           []TPSPoint
	lastSampleTime      int64
	lastSampleCommitted int64
	tpsSampleInterval   time.Duration
	tpsSamplerStopCh    chan struct{}
	tpsSamplerRunning   atomic.Bool
	tpsSamplerStopOnce  sync.Once
	client              ClientTxnManager

	latencyMu      sync.Mutex // later will have better fix and concurrent
	latencySamples []int64
}

type ClientTxnManager interface {
	sendTransactions([]core.ClientMsgSignature)
	TotalTxnsToInject() int64
}

type TransactionRetryManager struct {
	timer         *time.Timer
	timerStopCh   chan struct{}
	timerDoneCh   chan struct{}
	timerStarted  atomic.Bool
	timerStopOnce sync.Once
}

func NewTransactionManager(client ClientTxnManager, log *logger.Logger) *TransactionManager {
	transactionRetryTimer := time.NewTimer(5 * time.Second)
	transactionRetryTimer.Stop()

	tm := &TransactionManager{
		txnRetryManager:   &TransactionRetryManager{timer: transactionRetryTimer, timerStopCh: make(chan struct{}), timerDoneCh: make(chan struct{})},
		tpsSeries:         make([]TPSPoint, 0),
		tpsSampleInterval: 500 * time.Millisecond,
		tpsSamplerStopCh:  make(chan struct{}),
		client:            client,
		log:               log,
	}
	for i := range tm.shards {
		tm.shards[i].txns = make(map[int64]*transactionDetails)
	}
	return tm
}

func (tm *TransactionManager) retryTimerWorker(normalStart bool) {
	if normalStart {
		tm.txnRetryManager.timer.Reset(3 * time.Second)
	} else {
		tm.txnRetryManager.timer.Reset(10 * time.Millisecond)
	}
	defer close(tm.txnRetryManager.timerDoneCh)
	for {
		select {
		case <-tm.txnRetryManager.timer.C:
			tm.sendTxsForRetry()
			tm.txnRetryManager.timer.Reset(1 * time.Second)
		case <-tm.txnRetryManager.timerStopCh:
			return
		}
	}
}

func (tm *TransactionManager) StartRetryTimer(normalStart bool) {
	if tm.txnRetryManager.timerStarted.CompareAndSwap(false, true) {
		go tm.retryTimerWorker(normalStart)
	}
}

// func (tm *TransactionManager) TemporaryStopTimer() {
// 	tm.transactionTimerRunning.Store(false)
// 	if !tm.transactionTimer.Stop() {
// 		select {
// 		case <-tm.transactionTimer.C:
// 		default:
// 		}
// 	}
// }

func (tm *TransactionManager) StopRetryTimer() {
	tm.txnRetryManager.timerStopOnce.Do(func() {
		close(tm.txnRetryManager.timerStopCh)
	})
	if tm.txnRetryManager.timerStarted.Load() {
		<-tm.txnRetryManager.timerDoneCh
	}
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
		timeNow := time.Now()
		s.txns[msgSig.Data.Id] = &transactionDetails{
			startTimestamp: timeNow.UnixNano(),
			done:           false,
			clientMsgSig:   msgSig,
			retryCount:     0,
			nextRetryTime:  timeNow.Add(1 * time.Second),
		}
		s.mu.Unlock()
	}
}

func (tm *TransactionManager) sendTxsForRetry() {
	now := time.Now()
	candidates := make([]core.ClientMsgSignature, 0)
	txnsIterated := 0
	candidatesWithMultipleRetries := 0
	for i := range tm.shards {
		s := &tm.shards[i]
		s.mu.RLock()
		for _, txn := range s.txns {
			txnsIterated++
			txn.mu.Lock()
			if !txn.committed && now.After(txn.nextRetryTime) {
				candidates = append(candidates, txn.clientMsgSig)
				txn.retryCount++
				if txn.retryCount > 1 {
					candidatesWithMultipleRetries++
				}
				delay := retryDelay(txn.retryCount)
				txn.nextRetryTime = now.Add(delay)
			}
			txn.mu.Unlock()
		}
		s.mu.RUnlock()
	}
	if len(candidates) > 0 {
		tm.log.Info("Iterated through %d txns, found %d candidates for retry, %d of which have been retried multiple times\n", txnsIterated, len(candidates), candidatesWithMultipleRetries)

		timestart := time.Now()
		// tm.client.sendTransactions(candidates)
		timeduration := time.Since(timestart)
		tm.log.Info("Time taken to send %d transactions for retry: %s\n", len(candidates), timeduration)
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
	// even if delete from map but someone else has a pointer to the txn, it can still access it. go gc will not delete it until all references are gone
	txn.mu.Lock()
	if txn.committed {
		txn.mu.Unlock()
		return
	}

	txn.finishTimestamp = time.Now().UnixNano()
	txn.latency = txn.finishTimestamp - txn.startTimestamp
	txn.committed = true
	latency := txn.latency
	retried := txn.retryCount
	txn.mu.Unlock()
	s.mu.Lock()
	if current, exists := s.txns[reply.ClientMsg.Id]; exists && current == txn {
		delete(s.txns, reply.ClientMsg.Id)
	}
	s.mu.Unlock()
	numberOfCommittedTxns := tm.txnCommited.Add(1)
	if numberOfCommittedTxns == tm.client.TotalTxnsToInject() {
		tm.log.Info("All transactions committed")
	}
	if retried == 0 {
		tm.latencyMu.Lock()
		tm.latencySamples = append(tm.latencySamples, latency)
		tm.latencyMu.Unlock()
	}

	// if reply.ClientMsg.Id > txnGCRetentionWindow && reply.ClientMsg.Id%30000 == 0 {
	// 	go tm.GCTxns(reply.ClientMsg.Id - txnGCRetentionWindow)
	// }
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
	Count            int       `json:"count"`
	AvgMs            float64   `json:"avg_ms"`
	P50Ms            float64   `json:"p50_ms"`
	P95Ms            float64   `json:"p95_ms"`
	P99Ms            float64   `json:"p99_ms"`
	LatencySamplesMs []float64 `json:"latency_samples_ms"`
}

func (tm *TransactionManager) LatencySummary(path string) error {
	tm.latencyMu.Lock()
	samples := append([]int64(nil), tm.latencySamples...)
	tm.latencyMu.Unlock()

	sampleCount := len(samples)
	if sampleCount > 10 {
		sampleCount = 10
	}
	latencySamplesMs := make([]float64, sampleCount)
	for i := 0; i < sampleCount; i++ {
		latencySamplesMs[i] = nanosToMs(samples[i])
	}

	if len(samples) == 0 {
		data, err := json.MarshalIndent(LatencySummaryResult{
			LatencySamplesMs: latencySamplesMs,
		}, "", "  ")
		if err != nil {
			return err
		}
		return os.WriteFile(path, data, 0o644)
	}

	sortedSamples := append([]int64(nil), samples...)
	sort.Slice(sortedSamples, func(i, j int) bool {
		return sortedSamples[i] < sortedSamples[j]
	})

	var total float64
	for _, latency := range samples {
		total += float64(latency)
	}

	result := LatencySummaryResult{
		Count:            len(samples),
		AvgMs:            nanosToMs(int64(total / float64(len(samples)))),
		P50Ms:            nanosToMs(percentileLatency(sortedSamples, 0.50)),
		P95Ms:            nanosToMs(percentileLatency(sortedSamples, 0.95)),
		P99Ms:            nanosToMs(percentileLatency(sortedSamples, 0.99)),
		LatencySamplesMs: latencySamplesMs,
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
