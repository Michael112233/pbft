package node

import (
	"math/big"
	"slices"
	"testing"
	"time"

	"github.com/michael112233/pbft/core"
)

func digestTestBatch() []core.ClientMsgSignature {
	return []core.ClientMsgSignature{
		{
			Data: core.ClientMsg{
				Id:         1,
				Timestamp:  time.Unix(100, 0).UTC(),
				ClientName: "client-a",
				Txn:        core.NewTransaction("alice", "bob", big.NewInt(10)),
			},
			Signature: []byte{1, 2, 3},
		},
		{
			Data: core.ClientMsg{
				Id:         2,
				Timestamp:  time.Unix(101, 0).UTC(),
				ClientName: "client-b",
				Txn:        core.NewTransaction("carol", "dave", big.NewInt(20)),
			},
			Signature: []byte{4, 5, 6},
		},
	}
}

func TestComputeBatchDigestExcludesSignatures(t *testing.T) {
	batch := digestTestBatch()
	want, wantRequestDigests, err := ComputeBatchDigest(batch)
	if err != nil {
		t.Fatalf("ComputeBatchDigest returned error: %v", err)
	}

	batch[0].Signature = []byte("different signature")
	batch[1].Signature = nil
	got, gotRequestDigests, err := ComputeBatchDigest(batch)
	if err != nil {
		t.Fatalf("ComputeBatchDigest returned error: %v", err)
	}
	if got != want {
		t.Fatalf("digest changed when only signatures changed: got %x want %x", got, want)
	}
	if !slices.Equal(gotRequestDigests, wantRequestDigests) {
		t.Fatalf("request digests changed when only signatures changed: got %x want %x", gotRequestDigests, wantRequestDigests)
	}
}

func TestComputeBatchDigestCommitsToOrderAndMessageData(t *testing.T) {
	batch := digestTestBatch()
	original, originalRequestDigests, err := ComputeBatchDigest(batch)
	if err != nil {
		t.Fatalf("ComputeBatchDigest returned error: %v", err)
	}

	reordered := []core.ClientMsgSignature{batch[1], batch[0]}
	reorderedDigest, reorderedRequestDigests, err := ComputeBatchDigest(reordered)
	if err != nil {
		t.Fatalf("ComputeBatchDigest returned error: %v", err)
	}
	if reorderedDigest == original {
		t.Fatal("digest did not change when batch order changed")
	}
	if reorderedRequestDigests[0] != originalRequestDigests[1] || reorderedRequestDigests[1] != originalRequestDigests[0] {
		t.Fatal("request digests did not preserve batch order")
	}

	batch[0].Data.ClientName = "changed-client"
	changedDigest, changedRequestDigests, err := ComputeBatchDigest(batch)
	if err != nil {
		t.Fatalf("ComputeBatchDigest returned error: %v", err)
	}
	if changedDigest == original {
		t.Fatal("digest did not change when client-message data changed")
	}
	if changedRequestDigests[0] == originalRequestDigests[0] {
		t.Fatal("request digest did not change when client-message data changed")
	}
	if changedRequestDigests[1] != originalRequestDigests[1] {
		t.Fatal("unmodified request digest changed")
	}
}

func TestComputeBatchDigestRejectsEmptyBatch(t *testing.T) {
	if _, _, err := ComputeBatchDigest(nil); err == nil {
		t.Fatal("ComputeBatchDigest returned nil error for an empty batch")
	}
}
