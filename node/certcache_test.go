package node

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/logger"
)

// certCacheFixture is a 7-node (f=2) replica, node 2, plus a ViewChange from node 3
// whose 4 certs each carry 2f=4 real prepares from nodes 4..7, prepared in view (1,4).
type certCacheFixture struct {
	n     *Node
	privs map[int]ed25519.PrivateKey
	pubs  map[int]ed25519.PublicKey
	view  core.ViewID
	vc    core.ViewChangeMsg
}

func newCertCacheFixture(t *testing.T, parallel bool) *certCacheFixture {
	t.Helper()
	f := &certCacheFixture{privs: map[int]ed25519.PrivateKey{}, pubs: map[int]ed25519.PublicKey{}, view: core.ViewID{Generation: 1, Counter: 4}}
	for id := 1; id <= 7; id++ {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		f.pubs[id], f.privs[id] = pub, priv
	}
	f.n = &Node{
		NodeID:       2,
		cfg:          &config.Config{ParallelWorkers: parallel},
		fNodes:       2,
		log:          logger.NewLogger(2, "node"),
		consensusLog: NewLog(),
		// no public keys: any prepare this node had to signature-check would fail
		encryptionKeyStore: &KeyStore{publicKeys: map[int]ed25519.PublicKey{}},
	}
	certs := map[int64]*core.PreparedCert{}
	for seq := int64(11); seq <= 14; seq++ {
		digest := [32]byte{byte(seq)}
		cert := &core.PreparedCert{
			PreprepareMsg: core.PreprepareMsgSig{PreprepareMsgMini: core.PreprepareMsgMini{View: f.view, SeqNum: seq, DigestClientMsg: digest}},
			PrepareLog:    map[int]core.PrepareMsgSig{},
		}
		for from := 4; from <= 7; from++ {
			cert.PrepareLog[from] = signedPrepare(t, f.privs[from], core.PrepareMsg{View: f.view, SeqNum: seq, Digest: digest, From: from})
		}
		certs[seq] = cert
	}
	f.vc = core.ViewChangeMsg{ViewNumber: core.ViewID{Generation: 1, Counter: 5}, From: 3, PreparedCerts: certs}
	return f
}

// holdPrepares gives node 2 the cert prepares it received in view, except skip.
func (f *certCacheFixture) holdPrepares(skip map[[2]int64]bool) {
	for seq, cert := range f.vc.PreparedCerts {
		slot, _ := f.n.consensusLog.GetorCreateEntry(seq)
		slot.view = f.view
		for from, p := range cert.PrepareLog {
			if !skip[[2]int64{seq, int64(from)}] {
				slot.prepares[from] = p
			}
		}
	}
}

func TestVerifyVCSkipsPreparesAlreadyVerified(t *testing.T) {
	for _, parallel := range []bool{false, true} {
		f := newCertCacheFixture(t, parallel)

		if f.n.verifyVC(f.vc) {
			t.Fatalf("parallel=%v: nothing held and no keys, yet the view change verified", parallel)
		}

		f.holdPrepares(nil)
		if !f.n.verifyVC(f.vc) {
			t.Fatalf("parallel=%v: every prepare held, yet the view change failed (it re-checked a signature)", parallel)
		}

		// One prepare of seq 12 never arrived here: it must be checked, and needs a key.
		f = newCertCacheFixture(t, parallel)
		f.holdPrepares(map[[2]int64]bool{{12, 5}: true})
		if f.n.verifyVC(f.vc) {
			t.Fatalf("parallel=%v: an unheld prepare passed without its sender's key", parallel)
		}
		f.n.encryptionKeyStore.publicKeys = f.pubs
		if !f.n.verifyVC(f.vc) {
			t.Fatalf("parallel=%v: an unheld but valid prepare failed", parallel)
		}

		// The unheld prepare is forged: the cert is one valid prepare short.
		forged := f.vc.PreparedCerts[12].PrepareLog[5]
		forged.Signature = signedPrepare(t, f.privs[5], core.PrepareMsg{View: f.view, SeqNum: 12, Digest: [32]byte{99}, From: 5}).Signature
		f.vc.PreparedCerts[12].PrepareLog[5] = forged
		if f.n.verifyVC(f.vc) {
			t.Fatalf("parallel=%v: a forged unheld prepare was accepted", parallel)
		}
	}
}

func TestPrepareAlreadyVerified(t *testing.T) {
	f := newCertCacheFixture(t, false)
	f.holdPrepares(nil)
	p := f.vc.PreparedCerts[13].PrepareLog[6]

	if !f.n.prepareAlreadyVerified(p) {
		t.Fatal("held prepare not recognised")
	}

	other := p
	other.Signature = append([]byte(nil), p.Signature...)
	other.Signature[0] ^= 0xFF
	if f.n.prepareAlreadyVerified(other) {
		t.Fatal("prepare with different signature bytes counted as verified")
	}

	digest := p
	digest.PrepareMsg.Digest = [32]byte{1, 2, 3}
	if f.n.prepareAlreadyVerified(digest) {
		t.Fatal("prepare with a different digest counted as verified")
	}

	// A new view resets the slot's working state; the prepared cert still holds it.
	slot, _ := f.n.consensusLog.GetLogEntry(13)
	slot.recordPreparedIfHigher(f.view, f.vc.PreparedCerts[13])
	slot.resetPerViewState(core.ViewID{Generation: 1, Counter: 5})
	if !f.n.prepareAlreadyVerified(p) {
		t.Fatal("prepare held in the slot's prepared cert not recognised after a view reset")
	}
	slot.preparedProof = nil
	if f.n.prepareAlreadyVerified(p) {
		t.Fatal("prepare counted as verified with neither current prepares nor a cert holding it")
	}

	unknown := p
	unknown.PrepareMsg.SeqNum = 99
	if f.n.prepareAlreadyVerified(unknown) {
		t.Fatal("prepare for a seq with no slot counted as verified")
	}
}
