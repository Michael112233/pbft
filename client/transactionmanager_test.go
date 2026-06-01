package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/michael112233/pbft/core"
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

func TestTransactionManagerGCTxnsDeletesEntriesBelowCutoff(t *testing.T) {
	tm := NewTransactionManager()
	addTestTransactions(tm, 1, 63, 64, 19999, 20000, 20001)

	tm.GCTxns(20000)

	for _, id := range []int64{1, 63, 64, 19999} {
		if transactionExists(tm, id) {
			t.Fatalf("transaction %d still exists after GC cutoff 20000", id)
		}
	}
	for _, id := range []int64{20000, 20001} {
		if !transactionExists(tm, id) {
			t.Fatalf("transaction %d was deleted by GC cutoff 20000", id)
		}
	}
}

func TestTransactionManagerCommitTpsTriggersShardGC(t *testing.T) {
	tm := NewTransactionManager()
	addTestTransactions(tm, 9999, 10000, 10001, 30000)

	tm.CommitTps(core.CommitTps{
		ClientMsg: core.ClientMsg{Id: 30000},
	})

	if transactionExists(tm, 9999) {
		t.Fatal("transaction 9999 still exists after CommitTps GC cutoff 10000")
	}
	for _, id := range []int64{10000, 10001, 30000} {
		if !transactionExists(tm, id) {
			t.Fatalf("transaction %d was deleted by CommitTps GC", id)
		}
	}
	if committed := tm.txnCommited.Load(); committed != 1 {
		t.Fatalf("committed count = %d, want 1", committed)
	}
}

func TestTransactionManagerAddTransactionStoresMetadata(t *testing.T) {
	tm := NewTransactionManager()
	addTestTransactions(tm, 42)

	s := tm.getShard(42)
	s.mu.RLock()
	txn, exists := s.txns[42]
	s.mu.RUnlock()
	if !exists {
		t.Fatal("transaction 42 was not recorded")
	}
	if txn.startTimestamp == 0 {
		t.Fatal("startTimestamp = 0, want recorded timestamp")
	}
	if txn.done {
		t.Fatal("done = true, want false")
	}
	if txn.committed {
		t.Fatal("committed = true, want false")
	}
}

func addTestTransactions(tm *TransactionManager, ids ...int64) {
	batch := make([]core.ClientMsgSignature, 0, len(ids))
	for _, id := range ids {
		batch = append(batch, core.ClientMsgSignature{
			Data: core.ClientMsg{Id: id},
		})
	}
	tm.AddTransaction(batch)
}

func transactionExists(tm *TransactionManager, id int64) bool {
	s := tm.getShard(id)
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, exists := s.txns[id]
	return exists
}
