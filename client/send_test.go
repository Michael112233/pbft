package client

import (
	"crypto/ed25519"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/michael112233/pbft/core"
	pbftcrypto "github.com/michael112233/pbft/crypto"
	"github.com/michael112233/pbft/transportpb"
	"google.golang.org/protobuf/proto"
)

func TestSignedTxPipelineGeneratesUniqueVerifiableTransactions(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey returned error: %v", err)
	}

	c := &Client{
		name:       "pipeline-client",
		privateKey: privateKey,
	}

	const totalTxs = 25
	padding := strings.Repeat("x", 16)
	signedTxs := c.startSignedTxPipeline(totalTxs, padding, 4, 3)
	seen := make(map[int64]bool, totalTxs)

	for msgSig := range signedTxs {
		id := msgSig.Data.Id
		if id < 0 || id >= totalTxs {
			t.Fatalf("generated id = %d, want in [0, %d)", id, totalTxs)
		}
		if seen[id] {
			t.Fatalf("duplicate generated id %d", id)
		}
		seen[id] = true
		if msgSig.Data.ClientName != "pipeline-client" {
			t.Fatalf("ClientName = %q, want %q", msgSig.Data.ClientName, "pipeline-client")
		}
		if msgSig.Data.Padding != padding {
			t.Fatalf("Padding length = %d, want %d", len(msgSig.Data.Padding), len(padding))
		}

		clientMsgBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(transportpb.ClientMsgToPB(msgSig.Data))
		if err != nil {
			t.Fatalf("Marshal returned error: %v", err)
		}
		if !pbftcrypto.VerifySignatureEd25519(clientMsgBytes, msgSig.Signature, publicKey) {
			t.Fatalf("signature for id %d did not verify", id)
		}
	}

	if len(seen) != totalTxs {
		t.Fatalf("generated %d transactions, want %d", len(seen), totalTxs)
	}
	for id := int64(0); id < totalTxs; id++ {
		if !seen[id] {
			t.Fatalf("missing generated id %d", id)
		}
	}
}

func TestCollectSignedBatchReturnsFullAndPartialBatches(t *testing.T) {
	ch := make(chan core.ClientMsgSignature, 5)
	for id := int64(0); id < 5; id++ {
		ch <- core.ClientMsgSignature{Data: core.ClientMsg{Id: id}}
	}
	close(ch)

	fullBatch, ok := collectSignedBatch(ch, 3)
	if !ok {
		t.Fatal("collectSignedBatch returned ok=false for full batch")
	}
	if len(fullBatch) != 3 {
		t.Fatalf("len(fullBatch) = %d, want 3", len(fullBatch))
	}

	partialBatch, ok := collectSignedBatch(ch, 3)
	if !ok {
		t.Fatal("collectSignedBatch returned ok=false for partial batch")
	}
	if len(partialBatch) != 2 {
		t.Fatalf("len(partialBatch) = %d, want 2", len(partialBatch))
	}

	emptyBatch, ok := collectSignedBatch(ch, 3)
	if ok {
		t.Fatalf("collectSignedBatch returned ok=true for closed empty channel with %d messages", len(emptyBatch))
	}
}

func TestSignedTxQueueCapacityUsesOneSecondBuffer(t *testing.T) {
	if got := signedTxQueueCapacity(100); got != 2000 {
		t.Fatalf("signedTxQueueCapacity(100) = %d, want 2000", got)
	}
	if got := signedTxQueueCapacity(0); got != 1 {
		t.Fatalf("signedTxQueueCapacity(0) = %d, want 1", got)
	}
}

func TestRequestSendSpacingIsProportionalToTransactionCount(t *testing.T) {
	tests := []struct {
		name        string
		txCount     int
		injectSpeed int
		want        time.Duration
	}{
		{name: "full normal batch", txCount: 100, injectSpeed: 100, want: clientSendInterval},
		{name: "partial retry batch", txCount: 25, injectSpeed: 100, want: 10 * time.Millisecond},
		{name: "single retry", txCount: 1, injectSpeed: 100, want: 400 * time.Microsecond},
		{name: "empty batch", txCount: 0, injectSpeed: 100, want: 0},
		{name: "invalid speed", txCount: 1, injectSpeed: 0, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := requestSendSpacing(tt.txCount, tt.injectSpeed, clientSendInterval); got != tt.want {
				t.Fatalf("requestSendSpacing(%d, %d, %s) = %s, want %s", tt.txCount, tt.injectSpeed, clientSendInterval, got, tt.want)
			}
		})
	}
}

func TestForEachTransactionBatchHonorsInjectSpeed(t *testing.T) {
	txs := make([]core.ClientMsgSignature, 10)
	for id := range txs {
		txs[id].Data.Id = int64(id)
	}

	var batchSizes []int
	var ids []int64
	forEachTransactionBatch(txs, 4, func(batch []core.ClientMsgSignature) {
		batchSizes = append(batchSizes, len(batch))
		for _, tx := range batch {
			ids = append(ids, tx.Data.Id)
		}
	})

	wantBatchSizes := []int{4, 4, 2}
	if len(batchSizes) != len(wantBatchSizes) {
		t.Fatalf("batch count = %d, want %d", len(batchSizes), len(wantBatchSizes))
	}
	for i, want := range wantBatchSizes {
		if batchSizes[i] != want {
			t.Fatalf("batch %d size = %d, want %d", i, batchSizes[i], want)
		}
	}
	for id, got := range ids {
		if got != int64(id) {
			t.Fatalf("transaction %d has ID %d, want %d", id, got, id)
		}
	}
}

func TestRequestPacerUsesOneTimelineForNormalAndRetryTraffic(t *testing.T) {
	currentTime := time.Unix(100, 0)
	pacer := requestPacer{
		now: func() time.Time {
			return currentTime
		},
		sleep: func(delay time.Duration) {
			currentTime = currentTime.Add(delay)
		},
	}

	var sendTimes []time.Time
	recordSend := func() {
		sendTimes = append(sendTimes, currentTime)
	}

	// A full normal batch consumes 40 ms, the half-sized retry consumes 20 ms,
	// and the following normal batch must use that same shared timeline.
	pacer.pace(100, 100, clientSendInterval, recordSend)
	pacer.pace(50, 100, clientSendInterval, recordSend)
	pacer.pace(100, 100, clientSendInterval, recordSend)

	if len(sendTimes) != 3 {
		t.Fatalf("recorded %d sends, want 3", len(sendTimes))
	}
	if got := sendTimes[1].Sub(sendTimes[0]); got != 40*time.Millisecond {
		t.Fatalf("retry send followed normal send after %s, want 40ms", got)
	}
	if got := sendTimes[2].Sub(sendTimes[1]); got != 20*time.Millisecond {
		t.Fatalf("normal send followed partial retry after %s, want 20ms", got)
	}
}

func TestRequestPacerSerializesConcurrentProducers(t *testing.T) {
	pacer := requestPacer{}
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondEntered := make(chan struct{})
	firstDone := make(chan struct{})
	secondDone := make(chan struct{})
	var active atomic.Int32
	var overlap atomic.Bool

	go func() {
		pacer.pace(1, 1, time.Nanosecond, func() {
			if active.Add(1) != 1 {
				overlap.Store(true)
			}
			close(firstEntered)
			<-releaseFirst
			active.Add(-1)
		})
		close(firstDone)
	}()

	<-firstEntered
	go func() {
		pacer.pace(1, 1, time.Nanosecond, func() {
			if active.Add(1) != 1 {
				overlap.Store(true)
			}
			close(secondEntered)
			active.Add(-1)
		})
		close(secondDone)
	}()

	select {
	case <-secondEntered:
		t.Fatal("second producer entered send callback while first producer was active")
	case <-time.After(10 * time.Millisecond):
	}

	close(releaseFirst)
	<-firstDone
	<-secondDone
	if overlap.Load() {
		t.Fatal("normal and retry send callbacks overlapped")
	}
}
