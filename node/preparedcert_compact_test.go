package node

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/logger"
	"github.com/michael112233/pbft/transportpb"
	"google.golang.org/protobuf/proto"
)

// compactCertNode is a 7-node (f=2) replica with real Ed25519 keys for nodes 1..7.
func compactCertNode(t *testing.T) (*Node, map[int]ed25519.PrivateKey) {
	t.Helper()
	pubs := make(map[int]ed25519.PublicKey)
	privs := make(map[int]ed25519.PrivateKey)
	for id := 1; id <= 7; id++ {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		pubs[id], privs[id] = pub, priv
	}
	n := &Node{
		NodeID:             2,
		cfg:                &config.Config{},
		fNodes:             2,
		log:                logger.NewLogger(2, "node"),
		encryptionKeyStore: &KeyStore{publicKeys: pubs},
		consensusMsgChan:   make(chan ConsensusMsg, 1),
	}
	return n, privs
}

func signedPrepare(t *testing.T, priv ed25519.PrivateKey, msg core.PrepareMsg) core.PrepareMsgSig {
	t.Helper()
	payload, err := marshalDeterministic(transportpb.PrepareToPB(msg))
	if err != nil {
		t.Fatal(err)
	}
	return core.PrepareMsgSig{PrepareMsg: msg, Signature: ed25519.Sign(priv, payload)}
}

func TestBuildPreparedCertFiltersAndCapsAt2f(t *testing.T) {
	n, privs := compactCertNode(t)
	view := core.ViewID{Generation: 1, Counter: 4}
	mini := &core.PreprepareMsgMini{View: view, SeqNum: 77, DigestClientMsg: [32]byte{9}}
	good := func(from int) core.PrepareMsg {
		return core.PrepareMsg{View: view, SeqNum: 77, Digest: mini.DigestClientMsg, From: from}
	}
	wrongDigest := good(3)
	wrongDigest.Digest = [32]byte{1}
	wrongView := good(4)
	wrongView.View = core.ViewID{Generation: 1, Counter: 3}
	slot := &LogEntry{
		preprepare: mini,
		prepares: map[int]core.PrepareMsgSig{
			3: signedPrepare(t, privs[3], wrongDigest),
			4: signedPrepare(t, privs[4], wrongView),
			5: signedPrepare(t, privs[5], good(6)), // stored under the wrong key
			6: signedPrepare(t, privs[6], good(6)),
			7: signedPrepare(t, privs[7], good(7)),
			2: signedPrepare(t, privs[2], good(2)),
			1: signedPrepare(t, privs[1], good(1)),
		},
	}

	cert := n.buildPreparedCert(slot)

	if got := len(cert.PrepareLog); got != 2*n.fNodes {
		t.Fatalf("cert has %d prepares, want 2f=%d: %v", got, 2*n.fNodes, cert.PrepareLog)
	}
	for _, from := range []int{1, 2, 6, 7} { // matching ones, ascending
		if _, ok := cert.PrepareLog[from]; !ok {
			t.Fatalf("cert is missing matching prepare from %d: %v", from, cert.PrepareLog)
		}
	}
}

func TestBuildPreparedCertKeepsFewerThan2fUntouched(t *testing.T) {
	n, privs := compactCertNode(t)
	view := core.ViewID{Generation: 1, Counter: 1}
	mini := &core.PreprepareMsgMini{View: view, SeqNum: 5, DigestClientMsg: [32]byte{4}}
	slot := &LogEntry{preprepare: mini, prepares: map[int]core.PrepareMsgSig{
		3: signedPrepare(t, privs[3], core.PrepareMsg{View: view, SeqNum: 5, Digest: [32]byte{4}, From: 3}),
	}}
	if got := len(n.buildPreparedCert(slot).PrepareLog); got != 1 {
		t.Fatalf("cert has %d prepares, want 1", got)
	}
}

// The compact wire form drops each prepare's view, seq and digest and rebuilds them
// from the pre-prepare; the rebuilt messages must still verify against the original
// signatures.
func TestCompactPreparedCertStillVerifies(t *testing.T) {
	n, privs := compactCertNode(t)
	view := core.ViewID{Generation: 2, Counter: 9}
	mini := &core.PreprepareMsgMini{
		View: view, SeqNum: 301, DigestClientMsg: [32]byte{7, 7},
		DigestIndividualClientMsgs: [][32]byte{{1}, {2}},
	}
	slot := &LogEntry{preprepare: mini, preprepareSignature: []byte{1, 2, 3}, prepares: map[int]core.PrepareMsgSig{}}
	for from := 2; from <= 7; from++ {
		slot.prepares[from] = signedPrepare(t, privs[from], core.PrepareMsg{View: view, SeqNum: 301, Digest: mini.DigestClientMsg, From: from})
	}
	cert := n.buildPreparedCert(slot)

	wire, err := proto.Marshal(transportpb.PreparedCertToPB(cert))
	if err != nil {
		t.Fatal(err)
	}
	var pb transportpb.PreparedCert
	if err := proto.Unmarshal(wire, &pb); err != nil {
		t.Fatal(err)
	}
	got, err := transportpb.PreparedCertFromPB(&pb)
	if err != nil {
		t.Fatal(err)
	}
	if !n.verifyPrepareLog(got.PrepareLog, view, 301, mini.DigestClientMsg) {
		t.Fatal("compact cert failed verification after the wire round trip")
	}

	// A signature over a different digest must still be rejected once rebuilt.
	forged := got.PrepareLog[2]
	forged.Signature = signedPrepare(t, privs[2], core.PrepareMsg{View: view, SeqNum: 301, Digest: [32]byte{8}, From: 2}).Signature
	got.PrepareLog[2] = forged
	if n.verifyPrepareLog(got.PrepareLog, view, 301, mini.DigestClientMsg) {
		t.Fatal("cert with a prepare signed over another digest was accepted")
	}
}

func TestDeliverRejectsVoteSenderMismatch(t *testing.T) {
	n, privs := compactCertNode(t)
	hub := &NodeMessageHub{node_ref: n, log: n.log}
	view := core.ViewID{Generation: 1, Counter: 2}

	// Node 1 signs a vote claiming to be from node 3.
	prepare := transportpb.PrepareToPB(core.PrepareMsg{View: view, SeqNum: 10, Digest: [32]byte{1}, From: 3})
	commit := transportpb.CommitToPB(core.CommitMsg{View: view, SeqNum: 10, Digest: [32]byte{1}, From: 3})
	envs := map[string]*transportpb.Envelope{
		"prepare sender mismatch": {MsgType: core.MsgPrepareMessage, From: 1, Body: &transportpb.Envelope_Prepare{Prepare: prepare}},
		"commit sender mismatch":  {MsgType: core.MsgCommitMessage, From: 1, Body: &transportpb.Envelope_Commit{Commit: commit}},
	}
	for want, env := range envs {
		var payload proto.Message = env.GetPrepare()
		if env.GetCommit() != nil {
			payload = env.GetCommit()
		}
		b, err := marshalDeterministic(payload)
		if err != nil {
			t.Fatal(err)
		}
		env.Signature = ed25519.Sign(privs[1], b)

		ack, err := hub.Deliver(context.Background(), env)
		if err != nil {
			t.Fatal(err)
		}
		if ack.Ok || ack.Error != want {
			t.Fatalf("ack = %#v, want %q", ack, want)
		}
	}
	if len(n.consensusMsgChan) != 0 {
		t.Fatal("a vote with a mismatched sender reached the event loop")
	}
}
