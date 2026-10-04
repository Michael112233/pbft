package node

// Standalone cost measurement for the per-consensus-message receive path, used to
// check the fixed-vs-per-message CPU split inferred from throughput runs.
//
// What Deliver does for every Prepare/Commit it takes off a stream
// (nodeMessageHub.go verifySignature + the FromPB conversion):
//
//	marshalDeterministic(payload)  -> re-serialise to get the signed bytes
//	ed25519.Verify                 -> verify the sender's signature
//	<Type>FromPB                   -> convert to the core struct
//
// Run with:
//
//	go test ./node -run XXX -bench PerMessage -benchtime 2s
//
// Not a node test: it builds no Node and touches no node state.

import (
	"crypto/ed25519"
	"testing"

	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/crypto"
	"github.com/michael112233/pbft/transportpb"
)

func benchPrepare() (core.PrepareMsg, []byte, ed25519.PublicKey, []byte) {
	pub, priv, err := crypto.GenerateEd25519Keypair()
	if err != nil {
		panic(err)
	}
	msg := core.PrepareMsg{
		View:   core.ViewID{Generation: 3, Counter: 7},
		SeqNum: 123456,
		Digest: [32]byte{1, 2, 3, 4, 5, 6, 7, 8, 9},
		From:   2,
	}
	payload, err := marshalDeterministic(transportpb.PrepareToPB(msg))
	if err != nil {
		panic(err)
	}
	return msg, payload, pub, crypto.SignMessageEd25519(payload, priv)
}

// BenchmarkPerMessageVerifyPath is the whole per-message receive cost:
// re-marshal + verify + FromPB, exactly what verifySignature and the
// MsgPrepareMessage case in Deliver do.
func BenchmarkPerMessageVerifyPath(b *testing.B) {
	msg, _, pub, sig := benchPrepare()
	pb := transportpb.PrepareToPB(msg)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		payloadBytes, err := marshalDeterministic(pb)
		if err != nil {
			b.Fatal(err)
		}
		if !crypto.VerifySignatureEd25519(payloadBytes, sig, pub) {
			b.Fatal("verify failed")
		}
		if _, err := transportpb.PrepareFromPB(pb); err != nil {
			b.Fatal(err)
		}
	}
}

// The three components separately, so the dominant term is visible.
func BenchmarkPerMessageMarshalOnly(b *testing.B) {
	msg, _, _, _ := benchPrepare()
	pb := transportpb.PrepareToPB(msg)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := marshalDeterministic(pb); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPerMessageVerifyOnly(b *testing.B) {
	_, payload, pub, sig := benchPrepare()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !crypto.VerifySignatureEd25519(payload, sig, pub) {
			b.Fatal("verify failed")
		}
	}
}

// BenchmarkClientTxVerify is the per-transaction cost the leader pays on the
// other side of the ledger: one signature check per client request in a batch.
// With max_batch_size = 50 this runs 50x per consensus round, which is what the
// throughput model lumps into "fixed" (n-independent) work.
func BenchmarkClientTxVerify(b *testing.B) {
	pub, priv, err := crypto.GenerateEd25519Keypair()
	if err != nil {
		b.Fatal(err)
	}
	// A client request payload is larger than a Prepare; approximate with the
	// padding-free default (client_msg_padding_bytes = 0 in the configs used).
	payload := make([]byte, 256)
	for i := range payload {
		payload[i] = byte(i)
	}
	sig := crypto.SignMessageEd25519(payload, priv)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !crypto.VerifySignatureEd25519(payload, sig, pub) {
			b.Fatal("verify failed")
		}
	}
}
