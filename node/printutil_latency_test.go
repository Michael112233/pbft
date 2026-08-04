package node

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestSummarizeLatenciesMedian(t *testing.T) {
	tests := []struct {
		name       string
		samples    []time.Duration
		wantMedian float64
	}{
		{
			name:       "odd sample count",
			samples:    []time.Duration{5 * time.Millisecond, time.Millisecond, 3 * time.Millisecond},
			wantMedian: 3,
		},
		{
			name:       "even sample count",
			samples:    []time.Duration{4 * time.Millisecond, time.Millisecond, 2 * time.Millisecond, 3 * time.Millisecond},
			wantMedian: 2.5,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := summarizeLatencies(test.samples)
			if result.MedianMs != test.wantMedian {
				t.Fatalf("median_ms = %v, want %v", result.MedianMs, test.wantMedian)
			}
		})
	}
}

func TestSummarizeLatenciesPercentilesRatiosAndDiagnosticSamples(t *testing.T) {
	samples := make([]time.Duration, 100)
	for i := range samples {
		samples[i] = time.Duration(100-i) * time.Millisecond
	}
	wantDiagnosticSamples := []float64{100, 99, 98, 97, 96, 95, 94, 93, 92, 91}

	result := summarizeLatencies(samples)

	if result.Count != 100 {
		t.Fatalf("count = %d, want 100", result.Count)
	}
	if result.MedianMs != 50.5 {
		t.Fatalf("median_ms = %v, want 50.5", result.MedianMs)
	}
	if result.P95Ms != 95 {
		t.Fatalf("p95_ms = %v, want 95", result.P95Ms)
	}
	if result.P99Ms != 99 {
		t.Fatalf("p99_ms = %v, want 99", result.P99Ms)
	}
	if math.Abs(result.P95MedianRatio-(95.0/50.5)) > 1e-12 {
		t.Fatalf("p95_median_ratio = %v, want %v", result.P95MedianRatio, 95.0/50.5)
	}
	if math.Abs(result.P99MedianRatio-(99.0/50.5)) > 1e-12 {
		t.Fatalf("p99_median_ratio = %v, want %v", result.P99MedianRatio, 99.0/50.5)
	}
	if !reflect.DeepEqual(result.LatencySamplesMs, wantDiagnosticSamples) {
		t.Fatalf("latency_samples_ms = %v, want %v", result.LatencySamplesMs, wantDiagnosticSamples)
	}
}

func TestSummarizeLatenciesEmptyAndZeroMedian(t *testing.T) {
	emptyResult := summarizeLatencies(nil)
	if emptyResult.Count != 0 || len(emptyResult.LatencySamplesMs) != 0 {
		t.Fatalf("empty result = %+v, want zero-valued summary with an empty sample array", emptyResult)
	}

	zeroMedianResult := summarizeLatencies([]time.Duration{0, 0, time.Millisecond})
	if zeroMedianResult.MedianMs != 0 {
		t.Fatalf("median_ms = %v, want 0", zeroMedianResult.MedianMs)
	}
	if zeroMedianResult.P95MedianRatio != 0 || zeroMedianResult.P99MedianRatio != 0 {
		t.Fatalf("ratios = (%v, %v), want (0, 0)", zeroMedianResult.P95MedianRatio, zeroMedianResult.P99MedianRatio)
	}
	if _, err := json.Marshal(zeroMedianResult); err != nil {
		t.Fatalf("marshal zero-median result: %v", err)
	}
}

func TestPrintLatencySummaryWritesAndOverwritesNodeFile(t *testing.T) {
	t.Chdir(t.TempDir())

	monitor := NewLatencyMonitor()
	n := &Node{
		NodeID: 7,
		epochManager: &EpochManager{
			latencyMonitor: monitor,
		},
	}

	recordLatencySamples(monitor, []time.Duration{time.Millisecond})
	n.PrintLatencySummary()

	path := filepath.Join("logs", "node_7_latencylog.json")
	firstData, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read first latency summary: %v", err)
	}
	var firstResult latencySummaryResult
	if err := json.Unmarshal(firstData, &firstResult); err != nil {
		t.Fatalf("unmarshal first latency summary: %v", err)
	}
	if firstResult.Count != 1 {
		t.Fatalf("first count = %d, want 1", firstResult.Count)
	}

	recordLatencySamples(monitor, []time.Duration{2 * time.Millisecond, 4 * time.Millisecond})
	n.PrintLatencySummary()

	secondData, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read second latency summary: %v", err)
	}
	var secondResult latencySummaryResult
	if err := json.Unmarshal(secondData, &secondResult); err != nil {
		t.Fatalf("unmarshal second latency summary: %v", err)
	}
	if secondResult.Count != 2 || secondResult.MedianMs != 3 {
		t.Fatalf("second result = %+v, want count 2 and median_ms 3", secondResult)
	}
	if reflect.DeepEqual(firstData, secondData) {
		t.Fatal("latency summary file was not overwritten")
	}
}

func recordLatencySamples(monitor *LatencyMonitor, durations []time.Duration) {
	monitor.StartMonitoring()
	start := time.Unix(1, 0)
	for i, duration := range durations {
		digest := [32]byte{byte(i + 1)}
		monitor.RecordStartTime(digest, start)
		monitor.RecordEndTime(digest, start.Add(duration))
	}
}
