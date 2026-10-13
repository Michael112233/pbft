package client

import (
	"testing"

	"github.com/michael112233/pbft/config"
)

// The single client keeps every legacy path; with several clients each writes its
// own copy, which scripts/merge_client_reports.py combines.
func TestOutPathPerClient(t *testing.T) {
	single := &Client{id: 1, config: &config.Config{NodeNum: 7}}
	multi := &Client{id: 3, config: &config.Config{NodeNum: 7, ClientNum: 7}}

	tests := []struct {
		legacy, single, multi string
	}{
		{"report.json", "logs/report.json", "logs/client3_report.json"},
		{"latencyreport.json", "logs/latencyreport.json", "logs/client3_latencyreport.json"},
		{"client_mem.log", "logs/client_mem.log", "logs/client3_mem.log"},
		{"client_request_send_rate.csv", "logs/client_request_send_rate.csv", "logs/client3_request_send_rate.csv"},
		{"leader_timeline.jsonl", "logs/leader_timeline.jsonl", "logs/client3_leader_timeline.jsonl"},
		{"throttle_manager.jsonl", "logs/throttle_manager.jsonl", "logs/client3_throttle_manager.jsonl"},
	}
	for _, tc := range tests {
		if got := single.outPath(tc.legacy); got != tc.single {
			t.Fatalf("single outPath(%q) = %q, want %q", tc.legacy, got, tc.single)
		}
		if got := multi.outPath(tc.legacy); got != tc.multi {
			t.Fatalf("multi outPath(%q) = %q, want %q", tc.legacy, got, tc.multi)
		}
	}
}

func TestLatencyHistogramBuckets(t *testing.T) {
	ms := func(v float64) int64 { return int64(v * 1e6) }
	sorted := []int64{ms(10.62), ms(10.68), ms(10.71), ms(150.4), ms(150.9), ms(2500)}
	got := latencyHistogram(sorted)
	want := [][3]int64{
		{10_600, 100, 2},
		{10_700, 100, 1},
		{150_000, 1_000, 2},
		{2_500_000, 10_000, 1},
	}
	if len(got) != len(want) {
		t.Fatalf("histogram = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("histogram = %v, want %v", got, want)
		}
	}
}
