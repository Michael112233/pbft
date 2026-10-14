package node

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"testing"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/logger"
	"github.com/michael112233/pbft/transportpb"
)

// refsTestNode is a 7-node (f=2) replica in view (1,1) with just the state the
// NewView-by-digest code touches.
func refsTestNode() *Node {
	return &Node{
		NodeID:            2,
		cfg:               &config.Config{NodeNum: 7},
		fNodes:            2,
		log:               logger.NewLogger(2, "node"),
		viewID:            core.ViewID{Generation: 1, Counter: 1},
		forViewID:         core.ViewID{Generation: 1, Counter: 1},
		viewChangeMsgsLog: make(map[core.ViewID][]*core.ViewChangeMsgSig),
		viewChangeAlt:     make(map[core.ViewID]map[[32]byte]*core.ViewChangeMsgSig),
		electionManager:   NewElectionManager(),
	}
}

func refDigest(from int, variant byte) [32]byte {
	return [32]byte{byte(from), variant, 0xEE}
}

func logViewChange(n *Node, view core.ViewID, from int, variant byte) *core.ViewChangeMsgSig {
	vc := &core.ViewChangeMsgSig{
		ViewChangeMsg: core.ViewChangeMsg{ViewNumber: view, From: from},
		Digest:        refDigest(from, variant),
	}
	n.viewChangeMsgsLog[view] = append(n.viewChangeMsgsLog[view], vc)
	return vc
}

func refsFor(froms ...int) []core.ViewChangeRef {
	refs := make([]core.ViewChangeRef, 0, len(froms))
	for _, from := range froms {
		refs = append(refs, core.ViewChangeRef{From: from, Digest: refDigest(from, 0)})
	}
	return refs
}

func TestResolveViewChangeRefs(t *testing.T) {
	view := core.ViewID{Generation: 1, Counter: 3}
	n := refsTestNode()
	byFrom := map[int]*core.ViewChangeMsgSig{}
	for from := 1; from <= 5; from++ {
		byFrom[from] = logViewChange(n, view, from, 0)
	}

	t.Run("all present, in ref order", func(t *testing.T) {
		resolved, missing, err := n.resolveViewChangeRefs(core.NewViewMsg{NewViewNumber: view, ViewChangeRefs: refsFor(3, 1, 5, 2, 4)})
		if err != nil || len(missing) != 0 {
			t.Fatalf("err=%v missing=%v", err, missing)
		}
		for i, from := range []int{3, 1, 5, 2, 4} {
			if resolved[i] != byFrom[from] {
				t.Fatalf("resolved[%d] is from %d, want %d", i, resolved[i].ViewChangeMsg.From, from)
			}
		}
	})
	t.Run("unknown sender and digest mismatch are missing", func(t *testing.T) {
		refs := refsFor(1, 2, 3, 4, 6)
		refs[0].Digest = refDigest(1, 9) // node 1's VC exists, but not this one
		_, missing, err := n.resolveViewChangeRefs(core.NewViewMsg{NewViewNumber: view, ViewChangeRefs: refs})
		if err != nil {
			t.Fatal(err)
		}
		if len(missing) != 2 || missing[0] != 1 || missing[1] != 6 {
			t.Fatalf("missing = %v, want [1 6]", missing)
		}
	})
	t.Run("alternate copy resolves", func(t *testing.T) {
		alt := &core.ViewChangeMsgSig{ViewChangeMsg: core.ViewChangeMsg{ViewNumber: view, From: 1}, Digest: refDigest(1, 9)}
		n.storeAltViewChange(alt)
		defer delete(n.viewChangeAlt, view)
		refs := refsFor(1, 2, 3, 4, 5)
		refs[0].Digest = refDigest(1, 9)
		resolved, missing, err := n.resolveViewChangeRefs(core.NewViewMsg{NewViewNumber: view, ViewChangeRefs: refs})
		if err != nil || len(missing) != 0 || resolved[0] != alt {
			t.Fatalf("err=%v missing=%v resolved[0]=%p want %p", err, missing, resolved[0], alt)
		}
	})

	malformed := map[string][]core.ViewChangeRef{
		"fewer than 2f+1":  refsFor(1, 2, 3, 4),
		"duplicate sender": append(refsFor(1, 2, 3, 4), core.ViewChangeRef{From: 4, Digest: refDigest(4, 0)}),
		"zero digest":      append(refsFor(1, 2, 3, 4), core.ViewChangeRef{From: 5}),
		"unknown node":     append(refsFor(1, 2, 3, 4), core.ViewChangeRef{From: 8, Digest: refDigest(8, 0)}),
	}
	for name, refs := range malformed {
		t.Run(name, func(t *testing.T) {
			if _, _, err := n.resolveViewChangeRefs(core.NewViewMsg{NewViewNumber: view, ViewChangeRefs: refs}); err == nil {
				t.Fatal("malformed refs were accepted")
			}
		})
	}
}

func TestViewChangeIntakeDedupAndEquivocation(t *testing.T) {
	// (1,3) is more than one ahead of forView (1,1): stored, but no quorum or f+1
	// action, so only the intake logic runs.
	view := core.ViewID{Generation: 1, Counter: 3}
	n := refsTestNode()
	vc := core.ViewChangeMsg{ViewNumber: view, From: 4}
	first, second := refDigest(4, 0), refDigest(4, 1)

	n.HandleViewChangeRoundRobin(vc, nil, first)
	n.HandleViewChangeRoundRobin(vc, nil, first)
	if got := n.uniqueViewChangeCount(view); got != 1 {
		t.Fatalf("count after a repeat = %d, want 1", got)
	}

	// A different ViewChange from the same sender, with nothing pending: dropped.
	n.HandleViewChangeRoundRobin(vc, nil, second)
	if n.findViewChange(view, 4, second) != nil || n.uniqueViewChangeCount(view) != 1 {
		t.Fatal("an unreferenced equivocating view change was kept")
	}

	// Once a pending NewView names it, it is kept, but still not counted.
	refs := refsFor(1, 2, 3, 5)
	refs = append(refs, core.ViewChangeRef{From: 4, Digest: second})
	n.pendingNewView = &pendingNewView{msg: core.NewViewMsg{NewViewNumber: view, From: 3, ViewChangeRefs: refs}}
	defer n.clearPendingNewView()
	n.HandleViewChangeRoundRobin(vc, nil, second)
	if n.findViewChange(view, 4, second) == nil {
		t.Fatal("the view change the pending NewView names was not kept")
	}
	if got := n.uniqueViewChangeCount(view); got != 1 {
		t.Fatalf("the referenced copy was counted: count = %d, want 1", got)
	}
}

func TestPendingNewViewKeepsHighestView(t *testing.T) {
	n := refsTestNode()
	low := core.NewViewMsg{NewViewNumber: core.ViewID{Generation: 1, Counter: 3}}
	high := core.NewViewMsg{NewViewNumber: core.ViewID{Generation: 1, Counter: 4}}
	defer n.clearPendingNewView()

	n.setPendingNewView(high, []int{1})
	if n.vcFetchTimerCh == nil {
		t.Fatal("pending NewView did not arm the fetch timer")
	}
	n.pendingNewView.attempts = 1
	n.setPendingNewView(low, []int{1, 2})
	if !n.pendingNewView.msg.NewViewNumber.Equal(high.NewViewNumber) {
		t.Fatal("a lower-view NewView replaced the pending one")
	}
	n.setPendingNewView(high, []int{5})
	if n.pendingNewView.attempts != 1 || len(n.pendingNewView.missing) != 1 || n.pendingNewView.missing[0] != 5 {
		t.Fatalf("re-pending the same view should keep the fetch schedule and update missing: %+v", n.pendingNewView)
	}

	n.pruneBelowForView(core.ViewID{Generation: 1, Counter: 5})
	if n.pendingNewView != nil || n.vcFetchTimerCh != nil {
		t.Fatal("pending NewView below forView survived the prune")
	}
}

func TestViewChangesToServe(t *testing.T) {
	view := core.ViewID{Generation: 1, Counter: 3}
	n := refsTestNode()
	var vcs []*core.ViewChangeMsgSig
	for from := 1; from <= 5; from++ {
		vcs = append(vcs, logViewChange(n, view, from, 0))
	}
	refs := n.recordNewViewSent(view, vcs)
	if len(refs) != 5 || refs[2].From != 3 || refs[2].Digest != refDigest(3, 0) {
		t.Fatalf("refs = %+v", refs)
	}

	req := core.ViewChangeFetchMsg{ViewNumber: view, From: 6, Wanted: []int{2, 7, 4}}
	served := n.viewChangesToServe(req)
	if len(served) != 2 || served[0].ViewChangeMsg.From != 2 || served[1].ViewChangeMsg.From != 4 {
		t.Fatalf("served %v, want the referenced ones from 2 and 4", served)
	}
	if n.viewChangesToServe(req) == nil {
		t.Fatal("second request was refused")
	}
	if n.viewChangesToServe(req) != nil {
		t.Fatal("third request from the same node was answered")
	}
	if n.viewChangesToServe(core.ViewChangeFetchMsg{ViewNumber: core.ViewID{Generation: 1, Counter: 2}, From: 5, Wanted: []int{1}}) != nil {
		t.Fatal("request for another view was answered")
	}
}

func TestVerifyOSetRequiresEveryEntry(t *testing.T) {
	view := core.ViewID{Generation: 1, Counter: 3}
	entry := func(seq int64) core.PreprepareMsgSig {
		return core.PreprepareMsgSig{PreprepareMsgMini: core.PreprepareMsgMini{View: view, SeqNum: seq, DigestClientMsg: [32]byte{byte(seq)}}}
	}
	created := map[int64]core.PreprepareMsgSig{11: entry(11), 12: entry(12), 13: entry(13)}

	if !verifyOSet(created, []core.PreprepareMsgSig{entry(11), entry(12), entry(13)}) {
		t.Fatal("matching O-set rejected")
	}
	if verifyOSet(created, []core.PreprepareMsgSig{entry(11), entry(13)}) {
		t.Fatal("O-set missing seq 12 accepted")
	}
	if verifyOSet(created, []core.PreprepareMsgSig{entry(11), entry(11), entry(13)}) {
		t.Fatal("O-set with seq 11 twice accepted")
	}
}

func TestDeliverViewChangeNewViewAndFetch(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	n := &Node{
		log:                logger.NewLogger(2, "node"),
		encryptionKeyStore: &KeyStore{publicKeys: map[int]ed25519.PublicKey{1: pub}},
		viewChangeMsgChan:  make(chan ViewChangeMsg, 2),
		newViewMsgChan:     make(chan NewViewMsg, 2),
	}
	hub := &NodeMessageHub{node_ref: n, log: n.log}
	view := core.ViewID{Generation: 1, Counter: 3}
	sign := func(b []byte) []byte { return ed25519.Sign(priv, b) }

	t.Run("view change carries the digest of its signed bytes", func(t *testing.T) {
		pb := transportpb.ViewChangeToPB(core.ViewChangeMsg{ViewNumber: view, From: 1, Action: core.FixedRoundRobin})
		signed, _ := marshalDeterministic(pb)
		ack, _ := hub.Deliver(context.Background(), &transportpb.Envelope{MsgType: core.MsgViewChangeMessage, From: 1, Signature: sign(signed), Body: &transportpb.Envelope_ViewChange{ViewChange: pb}})
		if !ack.Ok {
			t.Fatalf("ack = %+v", ack)
		}
		if got := <-n.viewChangeMsgChan; got.Digest != sha256.Sum256(signed) {
			t.Fatal("delivered digest is not sha256 of the signed bytes")
		}
	})
	t.Run("new view with a bad signature is rejected", func(t *testing.T) {
		pb := transportpb.NewViewToPB(core.NewViewMsg{NewViewNumber: view, From: 1, Action: core.FixedRoundRobin, ViewChangeRefs: refsFor(1, 2, 3)})
		ack, _ := hub.Deliver(context.Background(), &transportpb.Envelope{MsgType: core.MsgNewViewMessage, From: 1, Signature: sign([]byte("other")), Body: &transportpb.Envelope_NewView{NewView: pb}})
		if ack.Ok || len(n.newViewMsgChan) != 0 {
			t.Fatalf("ack = %+v", ack)
		}
	})
	t.Run("fetch from another sender is rejected, a valid one delivered", func(t *testing.T) {
		pb := transportpb.ViewChangeFetchToPB(core.ViewChangeFetchMsg{ViewNumber: view, From: 3, Wanted: []int{4}})
		signed, _ := marshalDeterministic(pb)
		ack, _ := hub.Deliver(context.Background(), &transportpb.Envelope{MsgType: core.MsgViewChangeFetchMessage, From: 1, Signature: sign(signed), Body: &transportpb.Envelope_ViewChangeFetch{ViewChangeFetch: pb}})
		if ack.Ok || ack.Error != "view-change-fetch sender mismatch" {
			t.Fatalf("ack = %+v", ack)
		}

		pb.From = 1
		signed, _ = marshalDeterministic(pb)
		ack, _ = hub.Deliver(context.Background(), &transportpb.Envelope{MsgType: core.MsgViewChangeFetchMessage, From: 1, Signature: sign(signed), Body: &transportpb.Envelope_ViewChangeFetch{ViewChangeFetch: pb}})
		if !ack.Ok {
			t.Fatalf("ack = %+v", ack)
		}
		got := <-n.viewChangeMsgChan
		if got.MsgType != core.MsgViewChangeFetchMessage || got.Msg.(core.ViewChangeFetchMsg).Wanted[0] != 4 {
			t.Fatalf("delivered %+v", got)
		}
	})
}
