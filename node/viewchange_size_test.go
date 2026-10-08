package node

import (
	"crypto/ed25519"
	"crypto/rand"
	mrand "math/rand"
	"testing"
	"time"

	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/crypto"
	"github.com/michael112233/pbft/transportpb"
	"google.golang.org/protobuf/proto"
)

// Wire size and CPU cost of the view-change messages at the steady-state P-set bound
// (~290 certs = CHECKPOINT_INTERVAL + max_inflight_seq, docs/prepared-cert-bound.md),
// batch 50, carry_state off, for n=4 and n=7. Run with -v to see the numbers:
//
//	go test ./node -run 'TestViewChangeWireSize|TestNewViewWireSize' -v

const (
	sizeTestCerts     = 290
	sizeTestBatchSize = 50
	sizeTestFirstSeq  = 1001
)

// buildSizeTestCerts builds prepared certs for a view with 2f prepares each. Prepare
// signatures are real (signed with priv) so they can be verified; the pre-prepare
// signature is a size-equivalent fake.
func buildSizeTestCerts(t *testing.T, rng *mrand.Rand, priv ed25519.PrivateKey, view core.ViewID, f int, realSigs bool) map[int64]*core.PreparedCert {
	t.Helper()
	certs := make(map[int64]*core.PreparedCert, sizeTestCerts)
	for seq := int64(sizeTestFirstSeq); seq < sizeTestFirstSeq+sizeTestCerts; seq++ {
		mini := core.PreprepareMsgMini{View: view, SeqNum: seq, DigestClientMsg: fakeDigest(rng)}
		for i := 0; i < sizeTestBatchSize; i++ {
			mini.DigestIndividualClientMsgs = append(mini.DigestIndividualClientMsgs, fakeDigest(rng))
		}
		prepareLog := make(map[int]core.PrepareMsgSig, 2*f)
		for from := 2; from < 2+2*f; from++ {
			pm := core.PrepareMsg{View: view, SeqNum: seq, Digest: mini.DigestClientMsg, From: from}
			sig := fakeSig(rng)
			if realSigs {
				b, err := marshalDeterministic(transportpb.PrepareToPB(pm))
				if err != nil {
					t.Fatal(err)
				}
				sig = crypto.SignMessageEd25519(b, priv)
			}
			prepareLog[from] = core.PrepareMsgSig{PrepareMsg: pm, Signature: sig}
		}
		certs[seq] = &core.PreparedCert{
			PreprepareMsg: core.PreprepareMsgSig{PreprepareMsgMini: mini, Signature: fakeSig(rng)},
			PrepareLog:    prepareLog,
		}
	}
	return certs
}

func TestViewChangeWireSize(t *testing.T) {
	if testing.Short() {
		t.Skip("signs and verifies ~1000 prepares per node count")
	}
	for _, nodes := range []int{4, 7} {
		f := (nodes - 1) / 3
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		rng := mrand.New(mrand.NewSource(1))
		view := core.ViewID{Generation: 1, Counter: 5}
		vc := core.ViewChangeMsg{
			ViewNumber:          core.ViewID{Generation: 1, Counter: 6},
			CheckpointSeqNumber: sizeTestFirstSeq - 1,
			From:                1,
			PreparedCerts:       buildSizeTestCerts(t, rng, priv, view, f, true),
		}

		// sender, on the event loop in VC()
		start := time.Now()
		signBytes, err := marshalDeterministic(transportpb.ViewChangeToPB(vc))
		if err != nil {
			t.Fatal(err)
		}
		sig := crypto.SignMessageEd25519(signBytes, priv)
		send := time.Since(start)

		env := &transportpb.Envelope{MsgType: core.MsgViewChangeMessage, From: 1, Signature: sig,
			Body: &transportpb.Envelope_ViewChange{ViewChange: transportpb.ViewChangeToPB(vc)}}
		wire, err := proto.Marshal(env)
		if err != nil {
			t.Fatal(err)
		}

		// receiver: Deliver (decode, re-marshal, outer signature, FromPB)
		start = time.Now()
		var got transportpb.Envelope
		if err := proto.Unmarshal(wire, &got); err != nil {
			t.Fatal(err)
		}
		recvBytes, err := marshalDeterministic(got.GetViewChange())
		if err != nil {
			t.Fatal(err)
		}
		if !crypto.VerifySignatureEd25519(recvBytes, got.Signature, pub) {
			t.Fatal("outer ViewChange signature failed after the wire round trip")
		}
		decoded, err := transportpb.ViewChangeFromPB(got.GetViewChange())
		if err != nil {
			t.Fatal(err)
		}
		recv := time.Since(start)

		// receiver: cert prepares, serially (verifyVC fans this out across CPUs)
		start = time.Now()
		verified := 0
		for _, cert := range decoded.PreparedCerts {
			for _, p := range cert.PrepareLog {
				b, err := marshalDeterministic(transportpb.PrepareToPB(p.PrepareMsg))
				if err != nil {
					t.Fatal(err)
				}
				if !ed25519.Verify(pub, b, p.Signature) {
					t.Fatalf("cert prepare from %d seq %d failed verification", p.PrepareMsg.From, p.PrepareMsg.SeqNum)
				}
				verified++
			}
		}
		certVerify := time.Since(start)

		t.Logf("n=%d ViewChange: wire=%.1f KiB sender(toPB+marshal+sign)=%v receiver(decode+outer verify)=%v certVerify(%d sigs, serial)=%v",
			nodes, float64(len(wire))/1024, send, recv, verified, certVerify)
	}
}

// Refs (from, 32-byte digest) do not exist on the wire yet; until they do they are
// stood in by CheckpointMsg{From, Digest}, which has the same wire shape.
func TestNewViewWireSize(t *testing.T) {
	if testing.Short() {
		t.Skip("builds multi-MiB messages")
	}
	for _, nodes := range []int{4, 7} {
		f := (nodes - 1) / 3
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		rng := mrand.New(mrand.NewSource(1))
		view := core.ViewID{Generation: 1, Counter: 5}
		newView := core.ViewID{Generation: 1, Counter: 6}
		certs := buildSizeTestCerts(t, rng, priv, view, f, false)
		vc := core.ViewChangeMsg{ViewNumber: newView, CheckpointSeqNumber: sizeTestFirstSeq - 1, From: 1, PreparedCerts: certs}
		vcLog := make([]*core.ViewChangeMsgSig, 0, 2*f+1)
		for i := 0; i < 2*f+1; i++ {
			c := vc
			c.From = i + 1
			vcLog = append(vcLog, &core.ViewChangeMsgSig{ViewChangeMsg: c, Signature: fakeSig(rng)})
		}
		buildO := func(withLists bool) []core.PreprepareMsgSig {
			O := make([]core.PreprepareMsgSig, 0, sizeTestCerts)
			for seq := int64(sizeTestFirstSeq); seq < sizeTestFirstSeq+sizeTestCerts; seq++ {
				m := certs[seq].PreprepareMsg.PreprepareMsgMini
				m.View = newView
				if !withLists {
					m.DigestIndividualClientMsgs = nil
				}
				O = append(O, core.PreprepareMsgSig{PreprepareMsgMini: m, Signature: fakeSig(rng)})
			}
			return O
		}
		refBytes := 0
		for i := 0; i < 2*f+1; i++ {
			d := fakeDigest(rng)
			refBytes += proto.Size(&transportpb.CheckpointMsg{From: int32(i + 1), Digest: d[:]}) + 2 // + tag and length
		}

		cases := []struct {
			name  string
			O     []core.PreprepareMsgSig
			vcLog []*core.ViewChangeMsgSig
			refs  int
		}{
			{"current: O with lists + full 2f+1 VCs", buildO(true), vcLog, 0},
			{"VC refs (stand-in), O with lists", buildO(true), nil, refBytes},
			{"VC refs (stand-in), O without lists", buildO(false), nil, refBytes},
		}
		for _, c := range cases {
			nv := core.NewViewMsg{PreprepareLog: c.O, ViewChangeLog: c.vcLog, NewViewNumber: newView, From: 2}
			const iters = 10
			var send, recv time.Duration
			var wireLen int
			for i := 0; i < iters; i++ {
				start := time.Now()
				pb := transportpb.NewViewToPB(nv)
				signBytes, err := marshalDeterministic(pb)
				if err != nil {
					t.Fatal(err)
				}
				sig := crypto.SignMessageEd25519(signBytes, priv)
				env := &transportpb.Envelope{MsgType: core.MsgNewViewMessage, From: 2, Signature: sig,
					Body: &transportpb.Envelope_NewView{NewView: pb}}
				wire, err := proto.Marshal(env)
				if err != nil {
					t.Fatal(err)
				}
				send += time.Since(start)
				wireLen = len(wire) + c.refs

				start = time.Now()
				var got transportpb.Envelope
				if err := proto.Unmarshal(wire, &got); err != nil {
					t.Fatal(err)
				}
				recvBytes, err := marshalDeterministic(got.GetNewView())
				if err != nil {
					t.Fatal(err)
				}
				if !ed25519.Verify(pub, recvBytes, got.Signature) {
					t.Fatal("NewView signature failed after the wire round trip")
				}
				if _, err := transportpb.NewViewFromPB(got.GetNewView()); err != nil {
					t.Fatal(err)
				}
				recv += time.Since(start)
			}
			t.Logf("n=%d NewView %-40s wire=%8.1f KiB sender(toPB+marshal+sign+marshal)=%v receiver(decode+verify+fromPB)=%v",
				nodes, c.name, float64(wireLen)/1024, send/iters, recv/iters)
		}
	}
}
