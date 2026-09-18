package node

import (
	"testing"

	"github.com/michael112233/pbft/core"
)

// makePreparedCert builds a minimal prepared certificate for seq/view with a distinctive
// digest so a test can tell a real re-proposal from a null one.
func testView(counter uint64) core.ViewID {
	return core.ViewID{Generation: 1, Counter: counter}
}

func makePreparedCert(view core.ViewID, seq int64, digest [32]byte) *core.PreparedCert {
	return &core.PreparedCert{
		PreprepareMsg: core.PreprepareMsgSig{
			PreprepareMsgMini: core.PreprepareMsgMini{View: view, SeqNum: seq, DigestClientMsg: digest},
		},
		PrepareLog: map[int]core.PrepareMsgSig{
			2: {PrepareMsg: core.PrepareMsg{View: view, SeqNum: seq, Digest: digest, From: 2}},
			3: {PrepareMsg: core.PrepareMsg{View: view, SeqNum: seq, Digest: digest, From: 3}},
		},
	}
}

// vcPSet mirrors what createVCContent extracts from the log: the prepared certificate for
// every seq above the stable checkpoint that ever prepared.
func vcPSet(l *Log, stableCheckpointSeq int64) map[int64]*core.PreparedCert {
	out := make(map[int64]*core.PreparedCert)
	for seq := stableCheckpointSeq + 1; seq <= l.maxSeqNum; seq++ {
		slot, ok := l.GetLogEntry(seq)
		if !ok || slot.preparedProof == nil {
			continue
		}
		out[seq] = slot.preparedProof
	}
	return out
}

func digestFor(seq int64) [32]byte {
	var d [32]byte
	d[0] = byte(seq)
	d[1] = 0xAB
	return d
}

// TestPreparedCertSurvivesConsecutiveLocalNewViewInstalls reproduces the reported bug:
// a replica that was the intended primary for several failed views installs each new
// view locally and then fails before its NewView lands. Its prepared certificates for
// entries above the last stable checkpoint must still be present for the next view
// change's P-set.
func TestPreparedCertSurvivesConsecutiveLocalNewViewInstalls(t *testing.T) {
	l := NewLog()
	const stableCheckpoint = 0
	const suffixLen = 10

	// --- view 1: seqs 1..10 prepare; 1..7 also commit-local ---
	view1 := testView(1)
	for seq := int64(1); seq <= suffixLen; seq++ {
		slot, _ := l.GetorCreateEntry(seq)
		slot.view = view1
		slot.recordPreparedIfHigher(view1, makePreparedCert(view1, seq, digestFor(seq)))
		if seq <= 7 {
			slot.committed = true
		}
	}

	// --- views 2,3,4,5: this node is the intended primary each time, installs the
	// O-set suffix locally (ResetPerViewState + RemoveLogEntriesAboveSeq) and then
	// fails before anyone installs its NewView. maxSeq stays at the suffix length
	// because every collected VC message carries the same view-1 P-set. ---
	for counter := uint64(2); counter <= 5; counter++ {
		view := testView(counter)
		for seq := int64(1); seq <= suffixLen; seq++ {
			slot := l.ResetPerViewState(seq, view)
			slot.preprepare = &core.PreprepareMsgMini{View: view, SeqNum: seq, DigestClientMsg: digestFor(seq)}
			slot.view = view
		}
		retained, committedAbove := l.RemoveLogEntriesAboveSeq(suffixLen, view)
		if len(retained) != 0 {
			t.Fatalf("view %v: unexpected retained slots above maxSeq: %v", view, retained)
		}
		if len(committedAbove) != 0 {
			t.Fatalf("view %v: committed slots above maxSeq: %v", view, committedAbove)
		}
	}

	// --- next view change: the P-set must still carry every seq that prepared in
	// view 1, with its real digest and original prepared view. ---
	pset := vcPSet(l, stableCheckpoint)
	if len(pset) != suffixLen {
		t.Fatalf("P-set carries %d certs, want %d", len(pset), suffixLen)
	}
	for seq := int64(1); seq <= suffixLen; seq++ {
		cert, ok := pset[seq]
		if !ok {
			t.Fatalf("seq %d missing from P-set after 4 local new-view installs", seq)
		}
		if got := cert.PreprepareMsg.PreprepareMsgMini.DigestClientMsg; got != digestFor(seq) {
			t.Fatalf("seq %d P-set digest = %x, want %x (null/overwritten cert)", seq, got, digestFor(seq))
		}
		slot, _ := l.GetLogEntry(seq)
		if slot.preparedView != view1 {
			t.Fatalf("seq %d preparedView = %v, want %v", seq, slot.preparedView, view1)
		}
		// committed is per-view and is deliberately cleared on install: the seq is in
		// the new view's O-set (maxS covers it precisely because the cert above is
		// carried) and re-commits there, against the same view's pre-prepare that
		// execution reads its batch from.
		if slot.committed {
			t.Fatalf("seq %d kept a stale committed flag from a previous view", seq)
		}
	}
}

// TestCommittedImpliesPreparedProof pins the invariant that lets committed be per-view
// state: a slot can never be committed-local without a durable prepared certificate, so
// clearing committed on a new-view install loses nothing that the P-set does not carry.
func TestCommittedImpliesPreparedProof(t *testing.T) {
	l := NewLog()
	view1 := testView(1)
	slot, _ := l.GetorCreateEntry(4)
	slot.view = view1

	// mirror tryAdvancePrepare: the prepared cert is recorded before commitSent, and
	// tryExecute refuses to set committed without commitSent.
	slot.recordPreparedIfHigher(view1, makePreparedCert(view1, 4, digestFor(4)))
	slot.commitSent = true
	slot.committed = true

	if !slot.hasDurableState() {
		t.Fatal("a committed slot must carry a durable prepared certificate")
	}

	l.ResetPerViewState(4, testView(2))

	if slot.committed {
		t.Fatal("committed should be cleared on a new-view install")
	}
	if slot.preparedProof == nil || slot.preparedView != view1 {
		t.Fatal("the prepared certificate must survive so the seq lands in the next O-set")
	}
}

// TestRemoveLogEntriesAboveSeqKeepsDurableDropsTransient checks the truncation split:
// slots above maxSeq with a prepared/committed fact are kept (per-view state cleared,
// view rewound so the new primary can re-propose), everything else is deleted.
func TestRemoveLogEntriesAboveSeqKeepsDurableDropsTransient(t *testing.T) {
	l := NewLog()
	view1 := testView(1)

	// seq 5: prepared in view 1 -> durable, must be kept.
	prepared, _ := l.GetorCreateEntry(5)
	prepared.view = view1
	prepared.prepareSent = true
	prepared.commitSent = true
	prepared.recordPreparedIfHigher(view1, makePreparedCert(view1, 5, digestFor(5)))

	// seq 6: only a stray pre-prepare from the deposed primary -> no durable fact.
	transient, _ := l.GetorCreateEntry(6)
	transient.view = view1
	transient.preprepare = &core.PreprepareMsgMini{View: view1, SeqNum: 6}
	transient.prepareSent = true

	view7 := testView(7)
	retained, committedAbove := l.RemoveLogEntriesAboveSeq(4, view7)

	if len(retained) != 1 || retained[0] != 5 {
		t.Fatalf("retained = %v, want [5]", retained)
	}
	if len(committedAbove) != 0 {
		t.Fatalf("committedAbove = %v, want none", committedAbove)
	}
	if _, ok := l.GetLogEntry(6); ok {
		t.Fatal("seq 6 (no durable fact) should have been deleted")
	}
	slot, ok := l.GetLogEntry(5)
	if !ok {
		t.Fatal("seq 5 (durable) should have been kept")
	}
	if slot.preparedProof == nil || slot.preparedView != view1 {
		t.Fatal("seq 5 lost its prepared certificate")
	}
	if slot.commitSent || slot.prepareSent || slot.preprepare != nil {
		t.Fatal("seq 5 per-view working state was not cleared")
	}
	if slot.view != view7 {
		t.Fatalf("seq 5 view = %v, want %v (rewound for the new primary)", slot.view, view7)
	}
	if l.maxSeqNum != 5 {
		t.Fatalf("maxSeqNum = %d, want 5 (highest retained slot)", l.maxSeqNum)
	}
}

func TestRecordPreparedIfHigherIsMonotonic(t *testing.T) {
	e := &LogEntry{}

	view2 := testView(2)
	e.recordPreparedIfHigher(view2, makePreparedCert(view2, 9, digestFor(9)))
	if e.preparedView != view2 {
		t.Fatalf("preparedView = %v, want %v", e.preparedView, view2)
	}

	// a lower-view cert must not overwrite
	view1 := testView(1)
	e.recordPreparedIfHigher(view1, makePreparedCert(view1, 9, digestFor(1)))
	if e.preparedView != view2 || e.preparedProof.PreprepareMsg.PreprepareMsgMini.View != view2 {
		t.Fatal("lower-view prepared cert overwrote a higher-view one")
	}

	// a higher-view cert upgrades
	view4 := testView(4)
	e.recordPreparedIfHigher(view4, makePreparedCert(view4, 9, digestFor(4)))
	if e.preparedView != view4 {
		t.Fatalf("preparedView = %v, want %v", e.preparedView, view4)
	}
}

func TestResetPerViewStatePreservesDurableFacts(t *testing.T) {
	l := NewLog()
	view1 := testView(1)
	slot, _ := l.GetorCreateEntry(3)
	slot.view = view1
	slot.committed = true
	slot.executed = true
	slot.commitSent = true
	slot.prepares[2] = core.PrepareMsgSig{}
	slot.recordPreparedIfHigher(view1, makePreparedCert(view1, 3, digestFor(3)))

	view6 := testView(6)
	got := l.ResetPerViewState(3, view6)

	if got.preparedProof == nil || got.preparedView != view1 || !got.executed {
		t.Fatal("ResetPerViewState dropped a durable fact")
	}
	if got.view != view6 || got.commitSent || got.committed || got.preprepare != nil || len(got.prepares) != 0 {
		t.Fatal("ResetPerViewState did not clear per-view working state")
	}
}

// TestCommittedAboveMaxSeqIsReported checks the invariant alarm: a committed slot above
// maxSeq means the P-sets that produced maxS were incomplete.
func TestCommittedAboveMaxSeqIsReported(t *testing.T) {
	l := NewLog()
	view1 := testView(1)
	slot, _ := l.GetorCreateEntry(9)
	slot.view = view1
	slot.recordPreparedIfHigher(view1, makePreparedCert(view1, 9, digestFor(9)))
	slot.commitSent = true
	slot.committed = true

	retained, committedAbove := l.RemoveLogEntriesAboveSeq(4, testView(5))

	if len(retained) != 1 || retained[0] != 9 {
		t.Fatalf("retained = %v, want [9]", retained)
	}
	if len(committedAbove) != 1 || committedAbove[0] != 9 {
		t.Fatalf("committedAbove = %v, want [9]", committedAbove)
	}
}
