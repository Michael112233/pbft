package node

import "testing"

func TestNodeLatencySummaryUsesAllSamplesAndKeepsFirstTenSamplePreview(t *testing.T) {
	samples := []int64{
		10_000_000,
		1_000_000,
		12_000_000,
		2_000_000,
		11_000_000,
		3_000_000,
		9_000_000,
		4_000_000,
		8_000_000,
		5_000_000,
		7_000_000,
		6_000_000,
	}

	result := nodeLatencySummary(samples)

	if result.Count != 12 {
		t.Fatalf("Count = %d, want 12", result.Count)
	}
	if result.AvgMs != 6.5 {
		t.Fatalf("AvgMs = %f, want 6.5", result.AvgMs)
	}
	if result.P50Ms != 6 {
		t.Fatalf("P50Ms = %f, want 6", result.P50Ms)
	}
	if result.P95Ms != 12 {
		t.Fatalf("P95Ms = %f, want 12", result.P95Ms)
	}
	if result.P99Ms != 12 {
		t.Fatalf("P99Ms = %f, want 12", result.P99Ms)
	}

	wantSamples := []float64{10, 1, 12, 2, 11, 3, 9, 4, 8, 5}
	if len(result.LatencySamplesMs) != len(wantSamples) {
		t.Fatalf("len(LatencySamplesMs) = %d, want %d", len(result.LatencySamplesMs), len(wantSamples))
	}
	for i := range wantSamples {
		if result.LatencySamplesMs[i] != wantSamples[i] {
			t.Fatalf("LatencySamplesMs[%d] = %f, want %f", i, result.LatencySamplesMs[i], wantSamples[i])
		}
	}
}

func TestNodeLatencySummaryKeepsAllSamplesWhenFewerThanTen(t *testing.T) {
	result := nodeLatencySummary([]int64{2_000_000, 4_000_000, 6_000_000})

	wantSamples := []float64{2, 4, 6}
	if len(result.LatencySamplesMs) != len(wantSamples) {
		t.Fatalf("len(LatencySamplesMs) = %d, want %d", len(result.LatencySamplesMs), len(wantSamples))
	}
	for i := range wantSamples {
		if result.LatencySamplesMs[i] != wantSamples[i] {
			t.Fatalf("LatencySamplesMs[%d] = %f, want %f", i, result.LatencySamplesMs[i], wantSamples[i])
		}
	}
}
