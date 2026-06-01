package client

import (
	"crypto/ed25519"
	"strings"
	"testing"

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
