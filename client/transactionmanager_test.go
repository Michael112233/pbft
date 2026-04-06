package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTransactionManagerExportTPSSeries(t *testing.T) {
	tm := NewTransactionManager()
	tm.Start()
	defer tm.stopTPSSampler()

	tm.txnCommited.Store(10)
	time.Sleep(10 * time.Millisecond)
	tm.captureTPSSample(time.Now())

	tps, elapsed, committed := tm.GetThroughput()
	if committed != 10 {
		t.Fatalf("committed = %d, want 10", committed)
	}
	if elapsed <= 0 {
		t.Fatalf("elapsed = %f, want > 0", elapsed)
	}
	if tps <= 0 {
		t.Fatalf("tps = %f, want > 0", tps)
	}

	path := filepath.Join(t.TempDir(), "tps_series.json")
	if err := tm.ExportTPSSeries(path); err != nil {
		t.Fatalf("ExportTPSSeries returned error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}

	var points []TPSPoint
	if err := json.Unmarshal(data, &points); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if len(points) == 0 {
		t.Fatal("expected at least one exported TPS point")
	}
	if points[len(points)-1].CommittedTotal != 10 {
		t.Fatalf("last committed total = %d, want 10", points[len(points)-1].CommittedTotal)
	}
}
