package node

import (
	"sync"
	"time"
)

type LatencyData struct {
	startTime time.Time
	endTime   time.Time
	duration  time.Duration
}
type LatencyMonitor struct {
	latencyMu      sync.RWMutex
	msgs           map[[32]byte]LatencyData
	latencySamples []time.Duration
}

func NewLatencyMonitor() *LatencyMonitor {
	return &LatencyMonitor{
		msgs:           nil,
		latencySamples: nil,
	}
}

func (lm *LatencyMonitor) StartMonitoring() {
	lm.latencyMu.Lock()
	defer lm.latencyMu.Unlock()
	lm.msgs = make(map[[32]byte]LatencyData)
	lm.latencySamples = make([]time.Duration, 0)
}

func (lm *LatencyMonitor) StopMonitoring() []time.Duration {
	lm.latencyMu.Lock()
	defer lm.latencyMu.Unlock()
	samples := lm.latencySamples
	lm.msgs = nil
	lm.latencySamples = nil
	return samples
}

func (lm *LatencyMonitor) RecordStartTime(digest [32]byte, startTime time.Time) {
	lm.latencyMu.Lock()
	defer lm.latencyMu.Unlock()
	if lm.msgs == nil {
		return
	}
	if _, exists := lm.msgs[digest]; !exists {
		lm.msgs[digest] = LatencyData{
			startTime: time.Now(),
		}
	}
}

func (lm *LatencyMonitor) RecordEndTime(digest [32]byte, endTime time.Time) {
	lm.latencyMu.Lock()
	defer lm.latencyMu.Unlock()
	if lm.msgs == nil {
		return
	}
	if data, exists := lm.msgs[digest]; exists {
		data.endTime = time.Now()
		duration := time.Since(data.startTime)
		lm.msgs[digest] = data
		lm.latencySamples = append(lm.latencySamples, duration)
		delete(lm.msgs, digest)
	}

}
