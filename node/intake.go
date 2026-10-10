package node

import (
	"time"

	"github.com/michael112233/pbft/core"
)

// Intake filter: keeps client retries of requests that are already queued,
// proposed or executed out of the pending queue, so duplicates do not take
// batch slots. Loop-owned, no locking.
//
// It only decides what this node proposes when it is leader, so it does not need
// to agree across replicas and is not part of the checkpointed state. After a node
// jumps to a checkpoint it did not execute (fastPathStablizeCheckpointviaVC), its
// executed record misses the skipped requests, and a late retry of one of them can
// still be proposed and executed twice. Execution stays at-least-once; exeLoop
// counts duplicate executions so the rate is visible.

type requestKey struct {
	client string
	id     int64
}

// idTracker records which request ids of one client have executed: every id below
// watermark, plus the sparse set of ids at or above it. Client ids are dense from 0
// and executed roughly in order, so the set only holds ids that ran ahead of an
// older request still waiting on a retry.
type idTracker struct {
	watermark int64
	above     map[int64]struct{}
}

func newIDTracker() *idTracker {
	return &idTracker{above: make(map[int64]struct{})}
}

func (t *idTracker) contains(id int64) bool {
	if id < t.watermark {
		return true
	}
	_, ok := t.above[id]
	return ok
}

// add records id as executed and reports whether it already was.
func (t *idTracker) add(id int64) bool {
	if t.contains(id) {
		return true
	}
	t.above[id] = struct{}{}
	for {
		if _, ok := t.above[t.watermark]; !ok {
			break
		}
		delete(t.above, t.watermark)
		t.watermark++
	}
	return false
}

type intakeFilter struct {
	// trackExecuted records executed ids so a client retry of an executed request
	// is not proposed again. Only a retry path (client_retry, complete_suite) ever
	// resends a request; without one no duplicate can arrive, and recording would
	// cost ~30 B per executed request on every node for the rest of the run: the
	// first request that never commits (dropped, discarded, lost in a view change)
	// leaves a gap that is never filled, so the watermark stops and every later id
	// stays in idTracker.above.
	//
	// TODO(retry on): bound idTracker.above when trackExecuted is set. A gap that
	// no retry fills within some window (e.g. above > 1M entries, or the oldest gap
	// older than the client's retry max) should be treated as lost: advance the
	// watermark past it so the set drains. Until then, long runs with client_retry
	// still grow by ~30 B per executed request per node after the first lost one.
	trackExecuted bool
	executed      map[string]*idTracker
	// queued holds requests enqueued or proposed by this node in the current view
	// plus the O-set it re-proposed as new primary. Cleared with the pending queue
	// on every new-view install, so a retry of a request whose slot the view change
	// discarded is accepted again. Entries leave on execution, where executed takes
	// over.
	queued map[requestKey]struct{}

	// counters since the last report
	droppedAtIntake   int64
	duplicateExecuted int64
}

// newIntakeFilter builds the filter; trackExecuted should be true only when the
// client can resend requests (see intakeFilter.trackExecuted).
func newIntakeFilter(trackExecuted bool) *intakeFilter {
	return &intakeFilter{
		trackExecuted: trackExecuted,
		executed:      make(map[string]*idTracker),
		queued:        make(map[requestKey]struct{}),
	}
}

func (f *intakeFilter) tracker(client string) *idTracker {
	t, ok := f.executed[client]
	if !ok {
		t = newIDTracker()
		f.executed[client] = t
	}
	return t
}

// admit reports whether a request arriving at the leader should be enqueued, and
// marks it queued if so. If the enqueue then fails, the caller must unqueue it.
func (f *intakeFilter) admit(msg core.ClientMsg) bool {
	key := requestKey{client: msg.ClientName, id: msg.Id}
	if _, ok := f.queued[key]; ok || (f.trackExecuted && f.tracker(msg.ClientName).contains(msg.Id)) {
		f.droppedAtIntake++
		return false
	}
	f.queued[key] = struct{}{}
	return true
}

// markQueued records requests this node re-proposes without taking them from the
// pending queue (the O-set of a new view it leads).
func (f *intakeFilter) markQueued(reqs []core.ClientMsgSignature) {
	for _, req := range reqs {
		f.queued[requestKey{client: req.Data.ClientName, id: req.Data.Id}] = struct{}{}
	}
}

func (f *intakeFilter) unqueue(msg core.ClientMsg) {
	delete(f.queued, requestKey{client: msg.ClientName, id: msg.Id})
}

func (f *intakeFilter) resetQueued() {
	clear(f.queued)
}

// markExecuted records an executed request and counts it if it had executed before.
// Without trackExecuted it only clears the request from the queued set.
func (f *intakeFilter) markExecuted(msg core.ClientMsg) {
	delete(f.queued, requestKey{client: msg.ClientName, id: msg.Id})
	if !f.trackExecuted {
		return
	}
	if f.tracker(msg.ClientName).add(msg.Id) {
		f.duplicateExecuted++
	}
}

// takeCounters returns the counters since the last call and resets them.
func (f *intakeFilter) takeCounters() (dropped, duplicates int64) {
	dropped, duplicates = f.droppedAtIntake, f.duplicateExecuted
	f.droppedAtIntake, f.duplicateExecuted = 0, 0
	return dropped, duplicates
}

// resetPendingForNewView drops the pending queue and the per-view queued set on a
// new-view install. O is the set this node re-proposes as the new primary (nil at a
// replica); its request bodies come from the O-set itself with carry_state, and
// from the pool otherwise (filled by HandlePrePrepare).
func (n *Node) resetPendingForNewView(O []core.PreprepareMsgSig) {
	n.pendingRequests.Reset()
	n.intake.resetQueued()
	// A fresh tenure proposes its first batch immediately even when gated, so the
	// gate cannot delay seq 1 past a replica's progress timer (node/proposalgate.go).
	n.lastProposalAt = time.Time{}
	n.stopProposalGateTimer()
	for _, pp := range O {
		if len(pp.PreprepareMsgMini.DigestIndividualClientMsgs) == 0 {
			continue // no-op slot
		}
		if len(pp.ActualMsg) > 0 {
			n.intake.markQueued(pp.ActualMsg)
			continue
		}
		if reqs, ok := n.pool.GetBatch(pp.PreprepareMsgMini.DigestIndividualClientMsgs); ok {
			n.intake.markQueued(reqs)
		}
	}
}
