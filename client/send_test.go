package client

import (
	"crypto/ed25519"
	"math/rand"
	"strconv"
	"strings"
	"testing"

	"github.com/michael112233/pbft/config"
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

func TestGenerateDummyTxUsesUniformAccountPool(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	const accountCount = 100000

	for i := 0; i < 5000; i++ {
		tx := GenerateDummyTx(rng, accountCount)
		if tx == nil {
			t.Fatal("GenerateDummyTx returned nil")
		}

		senderIdx := mustAccountIndex(t, tx.Sender)
		receiverIdx := mustAccountIndex(t, tx.Receiver)
		if senderIdx < 0 || senderIdx >= accountCount {
			t.Fatalf("sender index = %d, want in [0, %d)", senderIdx, accountCount)
		}
		if receiverIdx < 0 || receiverIdx >= accountCount {
			t.Fatalf("receiver index = %d, want in [0, %d)", receiverIdx, accountCount)
		}
		if tx.Sender == tx.Receiver {
			t.Fatalf("sender and receiver are the same account %q", tx.Sender)
		}
		if tx.Amount == nil || tx.Amount.Sign() != 1 {
			t.Fatalf("Amount = %v, want positive amount", tx.Amount)
		}
	}
}

func TestGenerateDummyTxIsDeterministicWithFixedSeed(t *testing.T) {
	left := rand.New(rand.NewSource(42))
	right := rand.New(rand.NewSource(42))
	const accountCount = 100000

	for i := 0; i < 100; i++ {
		leftTx := GenerateDummyTx(left, accountCount)
		rightTx := GenerateDummyTx(right, accountCount)

		if leftTx.Sender != rightTx.Sender || leftTx.Receiver != rightTx.Receiver {
			t.Fatalf("fixed seed transaction %d mismatch: got %s->%s want %s->%s",
				i,
				leftTx.Sender,
				leftTx.Receiver,
				rightTx.Sender,
				rightTx.Receiver,
			)
		}
	}
}

func TestDummyAccountCountUsesDefaultFallback(t *testing.T) {
	if got := dummyAccountCount(nil); got != defaultDummyAccountCount {
		t.Fatalf("dummyAccountCount(nil) = %d, want %d", got, defaultDummyAccountCount)
	}
	if got := dummyAccountCount(&config.Config{DummyAccountCount: 1}); got != defaultDummyAccountCount {
		t.Fatalf("dummyAccountCount(invalid) = %d, want %d", got, defaultDummyAccountCount)
	}
	if got := dummyAccountCount(&config.Config{DummyAccountCount: 100000}); got != 100000 {
		t.Fatalf("dummyAccountCount(configured) = %d, want 100000", got)
	}
}

func TestDummyAccountNameFormatsWidthFromAccountCount(t *testing.T) {
	tests := []struct {
		name  string
		index int
		count int
		want  string
	}{
		{name: "default min width", index: 999, count: 1000, want: "acct-0999"},
		{name: "hundred thousand width", index: 99999, count: 100000, want: "acct-99999"},
		{name: "hundred thousand zero", index: 0, count: 100000, want: "acct-00000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dummyAccountName(tt.index, tt.count); got != tt.want {
				t.Fatalf("dummyAccountName(%d, %d) = %q, want %q", tt.index, tt.count, got, tt.want)
			}
		})
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

func mustAccountIndex(t *testing.T, account string) int {
	t.Helper()
	if !strings.HasPrefix(account, "acct-") {
		t.Fatalf("account %q does not have acct- prefix", account)
	}
	idx, err := strconv.Atoi(strings.TrimPrefix(account, "acct-"))
	if err != nil {
		t.Fatalf("account %q has invalid numeric suffix: %v", account, err)
	}
	return idx
}

func TestSignedTxQueueCapacityUsesOneSecondBuffer(t *testing.T) {
	if got := signedTxQueueCapacity(100); got != 2000 {
		t.Fatalf("signedTxQueueCapacity(100) = %d, want 2000", got)
	}
	if got := signedTxQueueCapacity(0); got != 1 {
		t.Fatalf("signedTxQueueCapacity(0) = %d, want 1", got)
	}
}
