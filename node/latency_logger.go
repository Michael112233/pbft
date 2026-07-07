package node

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
)

const latencyLoggerWriteInterval = 100

type nodeLatencySummaryResult struct {
	Count            int       `json:"count"`
	AvgMs            float64   `json:"avg_ms"`
	P50Ms            float64   `json:"p50_ms"`
	P95Ms            float64   `json:"p95_ms"`
	P99Ms            float64   `json:"p99_ms"`
	LatencySamplesMs []float64 `json:"latency_samples_ms"`
}

func (n *Node) latencyLogger() {
	defer close(n.latencyLoggerDone)

	if n.latencySamples == nil {
		n.latencySamples = make([]int64, 0)
	}

	for {
		select {
		case latency := <-n.latencyChan:
			n.latencySamples = append(n.latencySamples, latency)
			if len(n.latencySamples)%latencyLoggerWriteInterval == 0 {
				n.writeLatencySummaryJSON()
			}
		case <-n.latencyLoggerStop:
			n.drainLatencySamples()
			n.writeLatencySummaryJSON()
			return
		}
	}
}

func (n *Node) drainLatencySamples() {
	for {
		select {
		case latency := <-n.latencyChan:
			n.latencySamples = append(n.latencySamples, latency)
		default:
			return
		}
	}
}

func (n *Node) writeLatencySummaryJSON() {
	if err := os.MkdirAll("logs", 0755); err != nil {
		if n.log != nil {
			n.log.Error("Failed to create logs directory for latency JSON: %v", err)
		}
		return
	}

	path := filepath.Join("logs", "node_"+strconv.Itoa(n.NodeID)+"_latency.json")
	data, err := json.MarshalIndent(nodeLatencySummary(n.latencySamples), "", "  ")
	if err != nil {
		if n.log != nil {
			n.log.Error("Failed to marshal latency JSON: %v", err)
		}
		return
	}

	if err := os.WriteFile(path, data, 0666); err != nil {
		if n.log != nil {
			n.log.Error("Failed to write latency JSON %s: %v", path, err)
		}
	}
}

func nodeLatencySummary(samples []int64) nodeLatencySummaryResult {
	sampleCount := len(samples)
	if sampleCount > 10 {
		sampleCount = 10
	}
	latencySamplesMs := make([]float64, sampleCount)
	for i := 0; i < sampleCount; i++ {
		latencySamplesMs[i] = nodeNanosToMs(samples[i])
	}

	result := nodeLatencySummaryResult{
		Count:            len(samples),
		LatencySamplesMs: latencySamplesMs,
	}
	if len(samples) == 0 {
		return result
	}

	sortedSamples := append([]int64(nil), samples...)
	sort.Slice(sortedSamples, func(i, j int) bool {
		return sortedSamples[i] < sortedSamples[j]
	})

	var total float64
	for _, latency := range samples {
		total += float64(latency)
	}

	result.AvgMs = nodeNanosToMs(int64(total / float64(len(samples))))
	result.P50Ms = nodeNanosToMs(nodePercentileLatency(sortedSamples, 0.50))
	result.P95Ms = nodeNanosToMs(nodePercentileLatency(sortedSamples, 0.95))
	result.P99Ms = nodeNanosToMs(nodePercentileLatency(sortedSamples, 0.99))
	return result
}

func nodePercentileLatency(sorted []int64, p float64) int64 {
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

func nodeNanosToMs(ns int64) float64 {
	return float64(ns) / 1e6
}

func (n *Node) recordLatencySample(latency int64) {
	if n.cfg == nil || !n.cfg.Logging || n.latencyChan == nil {
		return
	}

	select {
	case n.latencyChan <- latency:
	default:
		if n.log != nil {
			n.log.Error("Dropped latency sample because latency channel is full")
		}
	}
}
