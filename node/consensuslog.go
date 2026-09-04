package node

import "github.com/michael112233/pbft/core"

type slotKey struct {
	seqNum int64
}
type LogEntry struct {
	// ---- durable consensus facts ----
	// These survive a view change and are cleared ONLY by GCLog once a stable
	// checkpoint passes this sequence number. The prepared certificate is what a
	// replica must be able to re-advertise in every subsequent ViewChange P-set
	// (Castro-Liskov 2.3.2).
	preparedProof *core.PreparedCert // highest-view (pre-prepare + 2f prepares) cert ever assembled for this seq
	preparedView  int64              // view of preparedProof; 0 = never prepared
	executed      bool               // diagnostic only; n.lastExecuted is the real double-execution guard

	// ---- per-view working state ----
	// Reset on every new-view install (ResetPerViewState / RemoveLogEntriesAboveSeq).
	//
	// committed lives here deliberately. It is committed-LOCAL: a predicate over the
	// current view's log, not a latch. A committed seq always has a prepared cert
	// (tryExecute requires commitSent, which tryAdvancePrepare only sets right after
	// recording the cert), that cert is carried in every later P-set, so maxS always
	// covers it and the new primary always re-proposes it in the O-set -- where it
	// re-prepares and re-commits. Keeping it per-view means the commit decision and
	// the pre-prepare that execution reads the batch from are always from the same
	// view, so they cannot disagree.
	view                int64
	preprepare          *core.PreprepareMsgMini
	preprepareSignature []byte
	prepares            map[int]core.PrepareMsgSig
	commits             map[int][32]byte

	prepareSent bool
	commitSent  bool
	committed   bool
}

// hasDurableState reports whether the slot carries a consensus fact that must not be
// lost across a view change. committed is not checked: committed implies a prepared
// certificate, so preparedProof already covers it.
func (e *LogEntry) hasDurableState() bool {
	return e.preparedProof != nil
}

// recordPreparedIfHigher records cert as the slot's prepared certificate when it was
// prepared in a strictly higher view than any cert already stored. Monotonic: a view
// change never lowers or clears it.
func (e *LogEntry) recordPreparedIfHigher(view int64, cert *core.PreparedCert) {
	if cert == nil || view <= e.preparedView {
		return
	}
	e.preparedProof = cert
	e.preparedView = view
}

// resetPerViewState clears everything tied to a particular view, leaving the durable
// facts (preparedProof/preparedView/executed) intact.
func (e *LogEntry) resetPerViewState(newView int64) {
	e.view = newView
	e.preprepare = nil
	e.preprepareSignature = nil
	e.prepares = make(map[int]core.PrepareMsgSig)
	e.commits = make(map[int][32]byte)
	e.prepareSent = false
	e.commitSent = false
	e.committed = false
}

type Log struct {
	low       int64
	high      int64
	maxSeqNum int64
	log       map[slotKey]*LogEntry
}

func NewLog() *Log {
	return &Log{
		low:  1,
		high: 2 * CHECKPOINT_INTERVAL,
		log:  make(map[slotKey]*LogEntry),
	}
}

func (l *Log) GetorCreateEntry(seqNum, view int64) (*LogEntry, bool) {
	// doesnt set view
	slotkey := slotKey{seqNum: seqNum}
	if slot, exists := l.log[slotkey]; exists {
		return slot, true
	}
	slot := &LogEntry{
		prepares: make(map[int]core.PrepareMsgSig),
		commits:  make(map[int][32]byte),
	}
	if seqNum > l.maxSeqNum {
		l.maxSeqNum = seqNum
	}
	l.log[slotkey] = slot
	return slot, false

}

// if something is already there, it will overwrite it, so be careful
func (l *Log) CreateEntry(seqNum int64) *LogEntry {
	slotkey := slotKey{seqNum: seqNum}
	slot := &LogEntry{

		prepares: make(map[int]core.PrepareMsgSig),
		commits:  make(map[int][32]byte),
	}
	if seqNum > l.maxSeqNum {
		l.maxSeqNum = seqNum
	}
	l.log[slotkey] = slot
	return slot
}

// ResetPerViewState returns the slot for seqNum (creating it if absent) with only its
// per-view working state cleared and view set to newView. Durable facts
// (preparedProof/preparedView/executed) are preserved. This replaces CreateEntry on the
// new-view install path, which used to blank-overwrite the slot and so destroyed the
// prepared certificate a later view change needs.
func (l *Log) ResetPerViewState(seqNum, newView int64) *LogEntry {
	slotkey := slotKey{seqNum: seqNum}
	slot, exists := l.log[slotkey]
	if !exists {
		slot = &LogEntry{}
		l.log[slotkey] = slot
	}
	slot.resetPerViewState(newView)
	if seqNum > l.maxSeqNum {
		l.maxSeqNum = seqNum
	}
	return slot
}

func (n *Node) slotPreprepare(slot *LogEntry, preprepareMsgMini *core.PreprepareMsgMini, preprepareSignature []byte, prepareSent bool) {
	slot.preprepare = preprepareMsgMini
	slot.preprepareSignature = preprepareSignature
	slot.prepareSent = prepareSent

}

func (l *Log) GetLogEntry(seqNum int64) (*LogEntry, bool) {
	slotkey := slotKey{seqNum: seqNum}
	if slot, exists := l.log[slotkey]; exists {
		return slot, true
	}
	return nil, false
}

func (l *Log) GetLogLen() int {
	return len(l.log)
}

func (l *Log) GCLog(stableCheckpointSeq int64) {
	for seq := l.low; seq <= stableCheckpointSeq; seq++ {
		slotkey := slotKey{seqNum: seq}
		delete(l.log, slotkey)
		// here can delete entries from pool referencing this seq number as well if needed
	}
	l.low = stableCheckpointSeq + 1
	l.high = l.low + 2*CHECKPOINT_INTERVAL - 1
}

// RemoveLogEntriesAboveSeq drops the tail of the log above seqNum during a new-view
// install. A slot that still carries a prepared certificate is KEPT so it is
// re-advertised in the next view change's P-set; only its per-view working state is
// reset to newView (so the new primary can later re-propose that sequence number
// without a stale-view rejection). Every other slot above seqNum is deleted.
// maxSeqNum is recomputed to the highest slot still present.
//
// Returns the retained sequence numbers, and separately any that were committed-local
// above seqNum. The latter should always be empty: a committed request is prepared at
// f+1 honest replicas, so any 2f+1 ViewChange messages carry its prepared cert and
// maxS covers it. A non-empty list means the P-sets going into createO were wrong.
func (l *Log) RemoveLogEntriesAboveSeq(seqNum, newView int64) (retained []int64, committedAbove []int64) {
	newMax := seqNum
	for seq := seqNum + 1; seq <= l.maxSeqNum; seq++ {
		slotkey := slotKey{seqNum: seq}
		slot, exists := l.log[slotkey]
		if !exists {
			continue
		}
		if slot.hasDurableState() {
			// prepared and committed state above maxSeqNum of O is kept
			// there shouldnt be any committed above maxSeqNum, but if there is, we return it so the test can catch it
			// there can be prepared one if our view change didnt make it into the O-set,
			if slot.committed {
				committedAbove = append(committedAbove, seq)
			}
			// keep the prepared certificate, drop the deposed primary's working state
			slot.resetPerViewState(newView)
			if seq > newMax {
				newMax = seq
			}
			retained = append(retained, seq)
			continue
		}
		delete(l.log, slotkey)
	}
	l.maxSeqNum = newMax
	return retained, committedAbove
}

func (n *Node) GCLog(stableCheckpointSeq int64) {
	n.consensusLog.GCLog(stableCheckpointSeq)
}
