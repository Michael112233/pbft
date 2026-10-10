package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/logger"
)

type transactionManagerTestClient struct{}

func (*transactionManagerTestClient) sendTransactions([]core.ClientMsgSignature) {}

func (*transactionManagerTestClient) TotalTxnsToInject() int64 {
	return 100
}

func (*transactionManagerTestClient) sendQueueStats() (int64, int64) { return 0, 0 }

func newTestTransactionManager() *TransactionManager {
	return NewTransactionManager(&transactionManagerTestClient{}, &logger.Logger{})
}

func TestTransactionManagerExportTPSSeries(t *testing.T) {
	tm := newTestTransactionManager()
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
	tm := newTestTransactionManager()
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

func TestTransactionManagerCommitTpsDeletesCommittedEntry(t *testing.T) {
	tm := newTestTransactionManager()
	addTestTransactions(tm, 9999, 10000, 10001, 30000)

	tm.CommitTps(core.CommitTps{
		ClientMsg: core.ClientMsgReply{Id: 30000},
	})

	if transactionExists(tm, 30000) {
		t.Fatal("committed transaction 30000 still exists")
	}
	for _, id := range []int64{9999, 10000, 10001} {
		if !transactionExists(tm, id) {
			t.Fatalf("uncommitted transaction %d was deleted by CommitTps", id)
		}
	}
	if committed := tm.txnCommited.Load(); committed != 1 {
		t.Fatalf("committed count = %d, want 1", committed)
	}
}

func TestTransactionManagerAddTransactionStoresMetadata(t *testing.T) {
	tm := newTestTransactionManager()
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

// Without a retry path the signed body is not kept; with one it is, and only
// entries that have a body are resent.
func TestAddTransactionKeepsBodyOnlyForRetry(t *testing.T) {
	body := core.ClientMsgSignature{Data: core.ClientMsg{Id: 7, ClientName: "c"}, Signature: []byte{1, 2, 3}}

	sent := &recordingTestClient{}
	tm := NewTransactionManager(sent, &logger.Logger{})
	tm.SetKeepRequestBody(false)
	tm.AddTransaction([]core.ClientMsgSignature{body}, time.Now())
	if txn := tm.getShard(7).txns[7]; txn == nil || txn.clientMsgSig != nil {
		t.Fatalf("keep=false: entry %+v, want an entry without a body", txn)
	}
	tm.getShard(7).txns[7].nextRetryTime = time.Time{} // due
	tm.sendTxsForRetry()
	if len(sent.sent) != 0 {
		t.Fatalf("keep=false: retry resent %d requests without a body", len(sent.sent))
	}

	tm = NewTransactionManager(sent, &logger.Logger{})
	tm.AddTransaction([]core.ClientMsgSignature{body}, time.Now())
	txn := tm.getShard(7).txns[7]
	if txn.clientMsgSig == nil || txn.clientMsgSig.Data.ClientName != "c" || len(txn.clientMsgSig.Signature) != 3 {
		t.Fatalf("keep=true (default): body %+v, want the signed request", txn.clientMsgSig)
	}
	txn.nextRetryTime = time.Time{}
	tm.sendTxsForRetry()
	if len(sent.sent) != 1 || sent.sent[0].Data.Id != 7 {
		t.Fatalf("keep=true: retry sent %+v, want request 7", sent.sent)
	}
}

// A batch dropped before sending is forgotten and counted when nothing can resend
// it, kept when a retry path can, and the report adds it up separately.
func TestForgetUnsentOnlyWithoutRetry(t *testing.T) {
	tm := newTestTransactionManager()
	tm.SetKeepRequestBody(false)
	addTestTransactions(tm, 1, 2, 3)
	if !tm.CommitTps(core.CommitTps{ClientMsg: core.ClientMsgReply{Id: 3}}) {
		t.Fatal("commit of 3 failed")
	}
	batch := []core.ClientMsgSignature{{Data: core.ClientMsg{Id: 1}}, {Data: core.ClientMsg{Id: 2}}, {Data: core.ClientMsg{Id: 3}}}
	if n := tm.ForgetUnsent(batch); n != 2 {
		t.Fatalf("ForgetUnsent removed %d, want 2 (3 had already committed)", n)
	}
	for _, id := range []int64{1, 2} {
		if transactionExists(tm, id) {
			t.Fatalf("request %d still tracked after ForgetUnsent", id)
		}
	}
	path := filepath.Join(t.TempDir(), "latency.json")
	if err := tm.LatencySummary(path); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	var got LatencySummaryResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.DroppedUnsent != 2 || got.Uncommitted != 0 {
		t.Fatalf("report dropped_unsent=%d uncommitted=%d, want 2 and 0", got.DroppedUnsent, got.Uncommitted)
	}

	retry := newTestTransactionManager() // keeps bodies: a retry path will resend
	addTestTransactions(retry, 1, 2)
	if n := retry.ForgetUnsent(batch[:2]); n != 0 || !transactionExists(retry, 1) || !transactionExists(retry, 2) {
		t.Fatalf("with retry: ForgetUnsent removed %d, want 0 and both still tracked", n)
	}
}

type recordingTestClient struct {
	transactionManagerTestClient
	sent []core.ClientMsgSignature
}

func (r *recordingTestClient) sendTransactions(txs []core.ClientMsgSignature) {
	r.sent = append(r.sent, txs...)
}

func addTestTransactions(tm *TransactionManager, ids ...int64) {
	batch := make([]core.ClientMsgSignature, 0, len(ids))
	for _, id := range ids {
		batch = append(batch, core.ClientMsgSignature{
			Data: core.ClientMsg{Id: id},
		})
	}
	tm.AddTransaction(batch, time.Now())
}

func transactionExists(tm *TransactionManager, id int64) bool {
	s := tm.getShard(id)
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, exists := s.txns[id]
	return exists
}

func TestAddTransactionMeasuresLatencyFromSendAndQueueSeparately(t *testing.T) {
	tm := newTestTransactionManager()
	createdAt := time.Now().Add(-24 * time.Millisecond) // batch waited a pacer slot before send
	batch := []core.ClientMsgSignature{{Data: core.ClientMsg{Id: 1}}, {Data: core.ClientMsg{Id: 2}}}
	tm.AddTransaction(batch, createdAt)

	for _, id := range []int64{1, 2} {
		s := tm.getShard(id)
		txn := s.txns[id]
		if txn.createdTimestamp != createdAt.UnixNano() {
			t.Fatalf("txn %d created = %d, want %d", id, txn.createdTimestamp, createdAt.UnixNano())
		}
		if wait := time.Duration(txn.startTimestamp - txn.createdTimestamp); wait < 24*time.Millisecond {
			t.Fatalf("txn %d start must be the send time, got only %s after creation", id, wait)
		}
	}
	if len(tm.queueSamples) != 1 || time.Duration(tm.queueSamples[0]) < 24*time.Millisecond {
		t.Fatalf("want one queue sample per batch of >= 24ms, got %v", tm.queueSamples)
	}

	if !tm.CommitTps(core.CommitTps{ClientMsg: core.ClientMsgReply{Id: 1}}) {
		t.Fatal("CommitTps did not commit txn 1")
	}
	path := filepath.Join(t.TempDir(), "latency.json")
	if err := tm.LatencySummary(path); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got LatencySummaryResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.LatencyMeasuredFrom != "send" || got.Count != 1 || got.QueueBatches != 1 {
		t.Fatalf("summary = %+v", got)
	}
	if got.QueueP50Ms < 24 || got.P50Ms >= got.QueueP50Ms {
		t.Fatalf("latency %.2fms must exclude the %.2fms queue wait", got.P50Ms, got.QueueP50Ms)
	}
}
