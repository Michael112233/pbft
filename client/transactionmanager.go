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

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/logger"
)

const (
	numShards            = 64
	txnGCRetentionWindow = 20000
)

type transactionDetails struct {
	mu               sync.Mutex // per-txn lock for long operations
	createdTimestamp int64      // when the batch was taken from the signer pipeline
	startTimestamp   int64      // when the batch was handed to the network (latency starts here)
	finishTimestamp  int64
	latency          int64
	done             bool
	committed        bool
	// clientMsgSig is the signed request, kept only so a retry can resend it; nil
	// when the manager does not keep request bodies (no retry path in this run).
	// A request that never commits is never removed from the shards, so without
	// retries this field would be kept, unread, for the whole run.
	clientMsgSig  *core.ClientMsgSignature
	nextRetryTime time.Time
	retryCount    int
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
	retry               retryPolicy
	// keepRequestBody stores each request's signed body for resending. Only a
	// retry path reads it (client_retry, or the complete_suite drain), so runs
	// without one drop it and an uncommitted request costs a few timestamps instead.
	keepRequestBody bool
	// droppedUnsent counts requests registered for a batch that was then dropped
	// before sending (full send queue), and removed again because nothing would
	// ever resend them (ForgetUnsent). They are not in uncommittedCount.
	droppedUnsent atomic.Int64
	retriesSent   atomic.Int64
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
	// retriedLatencySamples is the subset of latencySamples from requests that were
	// retried at least once (only filled when retry.includeRetried).
	retriedLatencySamples []int64
	// queueSamples holds one creation->send wait per sent batch (all txs in a
	// batch share it), so it stays small even on long runs.
	queueSamples []int64
}

type ClientTxnManager interface {
	sendTransactions([]core.ClientMsgSignature)
	TotalTxnsToInject() int64
	sendQueueStats() (dropped, discarded int64)
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
		retry:             defaultRetryPolicy(),
		tpsSeries:         make([]TPSPoint, 0),
		tpsSampleInterval: 100 * time.Millisecond,
		tpsSamplerStopCh:  make(chan struct{}),
		client:            client,
		log:               log,
		keepRequestBody:   true, // safe default; NewClient turns it off when nothing retries
	}
	for i := range tm.shards {
		tm.shards[i].txns = make(map[int64]*transactionDetails)
	}
	return tm
}

// SetRetryPolicy replaces the retry schedule. Call before StartRetryTimer and
// before any transaction is added.
func (tm *TransactionManager) SetRetryPolicy(p retryPolicy) {
	tm.retry = p
}

// SetKeepRequestBody says whether AddTransaction stores the signed request for
// resending. Call before any transaction is added.
func (tm *TransactionManager) SetKeepRequestBody(keep bool) {
	tm.keepRequestBody = keep
}

func (tm *TransactionManager) retryTimerWorker(normalStart bool) {
	sweep := tm.retry.sweepInterval()
	if normalStart {
		tm.txnRetryManager.timer.Reset(sweep)
	} else {
		tm.txnRetryManager.timer.Reset(10 * time.Millisecond)
	}
	defer close(tm.txnRetryManager.timerDoneCh)
	for {
		select {
		case <-tm.txnRetryManager.timer.C:
			tm.sendTxsForRetry()
			tm.txnRetryManager.timer.Reset(sweep)
		case <-tm.txnRetryManager.timerStopCh:
			return
		}
	}
}

func (tm *TransactionManager) StartRetryTimer(normalStart bool) {
	if !tm.keepRequestBody {
		// Nothing to resend: AddTransaction did not keep the bodies.
		tm.log.Error("retry timer not started: request bodies are not kept (SetKeepRequestBody(false))")
		return
	}
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

// AddTransaction registers a batch just before it is sent. Latency is measured
// from now (the send), and createdAt (when the batch left the signer pipeline)
// is kept so the client-side wait before sending is reported separately.
// start timestamp till commit is 7ms but node side measure is 3ms so either from leader batching or due to individual commit tps messages
func (tm *TransactionManager) AddTransaction(batch []core.ClientMsgSignature, createdAt time.Time) {
	timeNow := time.Now()
	for _, msgSig := range batch {
		details := &transactionDetails{
			createdTimestamp: createdAt.UnixNano(), // created -> send: pacer slot wait (+ pacer lock wait if retries are sending)
			startTimestamp:   timeNow.UnixNano(),   // taken just before send: latency includes send/stream wait, network, consensus and reply
			done:             false,
			retryCount:       0,
			nextRetryTime:    timeNow.Add(tm.retry.first()),
		}
		if tm.keepRequestBody {
			body := msgSig
			details.clientMsgSig = &body
		}
		s := tm.getShard(msgSig.Data.Id)
		s.mu.Lock()
		s.txns[msgSig.Data.Id] = details
		s.mu.Unlock()
	}
	if len(batch) > 0 {
		tm.latencyMu.Lock()
		tm.queueSamples = append(tm.queueSamples, timeNow.Sub(createdAt).Nanoseconds())
		tm.latencyMu.Unlock()
	}
}

// ForgetUnsent stops tracking a batch that AddTransaction registered but that was
// dropped before it was sent. With a retry path the entries stay, so the retry
// sweep resends them; without one they could never commit, so keeping them would
// only grow the client for the rest of the run. Returns how many were removed;
// they are counted in droppedUnsent instead of uncommitted.
func (tm *TransactionManager) ForgetUnsent(batch []core.ClientMsgSignature) int {
	if tm.keepRequestBody {
		return 0
	}
	removed := 0
	for _, msgSig := range batch {
		s := tm.getShard(msgSig.Data.Id)
		s.mu.Lock()
		if txn, ok := s.txns[msgSig.Data.Id]; ok {
			txn.mu.Lock()
			if !txn.committed {
				delete(s.txns, msgSig.Data.Id)
				removed++
			}
			txn.mu.Unlock()
		}
		s.mu.Unlock()
	}
	tm.droppedUnsent.Add(int64(removed))
	return removed
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
			if !txn.committed && txn.clientMsgSig != nil && now.After(txn.nextRetryTime) {
				candidates = append(candidates, *txn.clientMsgSig)
				txn.retryCount++
				if txn.retryCount > 1 {
					candidatesWithMultipleRetries++
				}
				delay := tm.retry.delay(txn.retryCount)
				txn.nextRetryTime = now.Add(delay)
			}
			txn.mu.Unlock()
		}
		s.mu.RUnlock()
	}
	if len(candidates) > 0 {
		tm.retriesSent.Add(int64(len(candidates)))
		tm.log.Info("Iterated through %d txns, found %d candidates for retry, %d of which have been retried multiple times\n", txnsIterated, len(candidates), candidatesWithMultipleRetries)

		timestart := time.Now()
		tm.client.sendTransactions(candidates)
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

func (tm *TransactionManager) CommitTps(reply core.CommitTps) bool {

	s := tm.getShard(reply.ClientMsg.Id)

	// Short shard lock just to grab the txn pointer
	s.mu.RLock()
	txn, ok := s.txns[reply.ClientMsg.Id]
	s.mu.RUnlock()
	if !ok {
		return false
	}

	// Per-txn lock for longer operations - doesn't block other txns in shard
	// even if delete from map but someone else has a pointer to the txn, it can still access it. go gc will not delete it until all references are gone
	txn.mu.Lock()
	if txn.committed {
		txn.mu.Unlock()
		return false
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
	// latency runs from the first send, so a retried request carries its retry wait
	if retried == 0 || tm.retry.includeRetried {
		tm.latencyMu.Lock()
		tm.latencySamples = append(tm.latencySamples, latency)
		if retried > 0 {
			tm.retriedLatencySamples = append(tm.retriedLatencySamples, latency)
		}
		tm.latencyMu.Unlock()
	}

	return true
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
	// LatencyMeasuredFrom marks what the latency fields measure: "send" means from
	// the request batch being handed to the network until the first CommitTps.
	LatencyMeasuredFrom string    `json:"latency_measured_from"`
	Count               int       `json:"count"`
	AvgMs               float64   `json:"avg_ms"`
	P50Ms               float64   `json:"p50_ms"`
	P95Ms               float64   `json:"p95_ms"`
	P99Ms               float64   `json:"p99_ms"`
	P999Ms              float64   `json:"p99_9_ms"`
	LatencySamplesMs    []float64 `json:"latency_samples_ms"`
	// Retry: whether retried commits are in the samples above, how many of the
	// samples were retried and their latency, and total retry transmissions.
	RetryEnabled bool    `json:"retry_enabled"`
	RetryMode    string  `json:"retry_mode,omitempty"`
	RetriedCount int     `json:"retried_count"`
	RetriedAvgMs float64 `json:"retried_avg_ms"`
	RetriedP50Ms float64 `json:"retried_p50_ms"`
	RetriedP99Ms float64 `json:"retried_p99_ms"`
	RetriesSent  int64   `json:"retries_sent"`
	// Uncommitted counts requests still tracked and never committed by the time of
	// the summary.
	Uncommitted int `json:"uncommitted"`
	// DroppedUnsent counts requests dropped before sending that were removed from
	// tracking because no retry path could resend them (TransactionManager.
	// ForgetUnsent). uncommitted + dropped_unsent is every request that never
	// committed; with retry on it is 0 and dropped requests stay in uncommitted.
	DroppedUnsent int64 `json:"dropped_unsent"`
	// Requests dropped on a full per-node send queue, and discarded from the old
	// leader's queue on a leader change (sendqueue.go). Discarded ones stay
	// registered; dropped ones too unless counted in dropped_unsent.
	SendQueueDropped   int64 `json:"send_queue_dropped"`
	SendQueueDiscarded int64 `json:"send_queue_discarded"`
	// Client-side wait between a batch leaving the signer pipeline and being
	// sent (pacer slot wait), one sample per batch. End-to-end ~= latency + queue.
	QueueBatches int     `json:"queue_batches"`
	QueueAvgMs   float64 `json:"queue_avg_ms"`
	QueueP50Ms   float64 `json:"queue_p50_ms"`
	QueueP95Ms   float64 `json:"queue_p95_ms"`
	QueueP99Ms   float64 `json:"queue_p99_ms"`
}

func (tm *TransactionManager) LatencySummary(path string) error {
	tm.latencyMu.Lock()
	samples := append([]int64(nil), tm.latencySamples...)
	retriedSamples := append([]int64(nil), tm.retriedLatencySamples...)
	queue := append([]int64(nil), tm.queueSamples...)
	tm.latencyMu.Unlock()

	sampleCount := len(samples)
	if sampleCount > 10 {
		sampleCount = 10
	}
	latencySamplesMs := make([]float64, sampleCount)
	for i := 0; i < sampleCount; i++ {
		latencySamplesMs[i] = nanosToMs(samples[i])
	}

	result := LatencySummaryResult{
		LatencyMeasuredFrom: "send",
		LatencySamplesMs:    latencySamplesMs,
		RetryEnabled:        tm.retry.includeRetried,
		RetriesSent:         tm.retriesSent.Load(),
		Uncommitted:         tm.uncommittedCount(),
		DroppedUnsent:       tm.droppedUnsent.Load(),
	}
	result.SendQueueDropped, result.SendQueueDiscarded = tm.client.sendQueueStats()
	if tm.retry.includeRetried {
		result.RetryMode = config.ClientRetryBackoff
		if tm.retry.fixed {
			result.RetryMode = config.ClientRetryFixed
		}
	}
	if len(retriedSamples) > 0 {
		sortedRetried := append([]int64(nil), retriedSamples...)
		sort.Slice(sortedRetried, func(i, j int) bool { return sortedRetried[i] < sortedRetried[j] })
		var retriedTotal float64
		for _, l := range retriedSamples {
			retriedTotal += float64(l)
		}
		result.RetriedCount = len(retriedSamples)
		result.RetriedAvgMs = nanosToMs(int64(retriedTotal / float64(len(retriedSamples))))
		result.RetriedP50Ms = nanosToMs(percentileLatency(sortedRetried, 0.50))
		result.RetriedP99Ms = nanosToMs(percentileLatency(sortedRetried, 0.99))
	}
	if len(queue) > 0 {
		sortedQueue := append([]int64(nil), queue...)
		sort.Slice(sortedQueue, func(i, j int) bool { return sortedQueue[i] < sortedQueue[j] })
		var queueTotal float64
		for _, q := range queue {
			queueTotal += float64(q)
		}
		result.QueueBatches = len(queue)
		result.QueueAvgMs = nanosToMs(int64(queueTotal / float64(len(queue))))
		result.QueueP50Ms = nanosToMs(percentileLatency(sortedQueue, 0.50))
		result.QueueP95Ms = nanosToMs(percentileLatency(sortedQueue, 0.95))
		result.QueueP99Ms = nanosToMs(percentileLatency(sortedQueue, 0.99))
	}

	if len(samples) == 0 {
		data, err := json.MarshalIndent(result, "", "  ")
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

	result.Count = len(samples)
	result.AvgMs = nanosToMs(int64(total / float64(len(samples))))
	result.P50Ms = nanosToMs(percentileLatency(sortedSamples, 0.50))
	result.P95Ms = nanosToMs(percentileLatency(sortedSamples, 0.95))
	result.P99Ms = nanosToMs(percentileLatency(sortedSamples, 0.99))
	result.P999Ms = nanosToMs(percentileLatency(sortedSamples, 0.999))

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// uncommittedCount returns how many sent requests have not committed. Committed
// requests are deleted from the shards in CommitTps, so this is what is left.
func (tm *TransactionManager) uncommittedCount() int {
	count := 0
	for i := range tm.shards {
		s := &tm.shards[i]
		s.mu.RLock()
		for _, txn := range s.txns {
			txn.mu.Lock()
			if !txn.committed {
				count++
			}
			txn.mu.Unlock()
		}
		s.mu.RUnlock()
	}
	return count
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
