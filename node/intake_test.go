package node

import (
	"testing"

	"github.com/michael112233/pbft/core"
)

func req(id int64) core.ClientMsg {
	return core.ClientMsg{Id: id, ClientName: "client"}
}

func TestIDTrackerWatermarkAdvancesOverGaps(t *testing.T) {
	tr := newIDTracker()
	for _, id := range []int64{0, 1, 3, 4} {
		if tr.add(id) {
			t.Fatalf("id %d reported as already executed", id)
		}
	}
	if tr.watermark != 2 || len(tr.above) != 2 {
		t.Fatalf("watermark=%d above=%v, want 2 and {3,4}", tr.watermark, tr.above)
	}
	if tr.contains(2) {
		t.Fatal("id 2 never executed but contains says so")
	}
	if tr.add(2); tr.watermark != 5 || len(tr.above) != 0 {
		t.Fatalf("after filling the gap watermark=%d above=%v, want 5 and empty", tr.watermark, tr.above)
	}
	if !tr.add(3) {
		t.Fatal("re-adding id 3 not reported as duplicate")
	}
}

func TestIntakeDropsQueuedAndExecutedRetries(t *testing.T) {
	f := newIntakeFilter(true)
	if !f.admit(req(7)) {
		t.Fatal("first arrival rejected")
	}
	if f.admit(req(7)) {
		t.Fatal("retry of a queued request admitted")
	}
	f.markExecuted(req(7))
	if f.admit(req(7)) {
		t.Fatal("retry of an executed request admitted")
	}
	if dropped, dups := f.takeCounters(); dropped != 2 || dups != 0 {
		t.Fatalf("dropped=%d dups=%d, want 2 and 0", dropped, dups)
	}
}

// A request whose slot a view change discarded must be admitted again on retry.
func TestIntakeAdmitsRetryAfterNewView(t *testing.T) {
	f := newIntakeFilter(true)
	f.admit(req(9))
	f.resetQueued()
	if !f.admit(req(9)) {
		t.Fatal("retry after a new-view reset was dropped")
	}
}

func TestIntakeUnqueueAfterFailedEnqueue(t *testing.T) {
	f := newIntakeFilter(true)
	f.admit(req(4))
	f.unqueue(req(4))
	if !f.admit(req(4)) {
		t.Fatal("retry of a request lost at enqueue was dropped")
	}
}

// The O-set a new primary re-proposes counts as queued, so its retries are dropped.
func TestIntakeOSetMarkedQueued(t *testing.T) {
	f := newIntakeFilter(true)
	f.markQueued([]core.ClientMsgSignature{{Data: req(11)}})
	if f.admit(req(11)) {
		t.Fatal("retry of an O-set request admitted")
	}
}

func TestIntakeCountsDuplicateExecution(t *testing.T) {
	f := newIntakeFilter(true)
	f.markExecuted(req(5))
	f.markExecuted(req(5))
	if _, dups := f.takeCounters(); dups != 1 {
		t.Fatalf("dups=%d, want 1", dups)
	}
}

// Without a retry path no executed id is recorded, even after a gap that would
// freeze the watermark, while requests queued in this view are still deduplicated.
func TestIntakeWithoutRetryKeepsNoExecutedIDs(t *testing.T) {
	f := newIntakeFilter(false)
	msg := func(id int64) core.ClientMsg { return core.ClientMsg{ClientName: "c", Id: id} }

	if !f.admit(msg(5)) || f.admit(msg(5)) {
		t.Fatal("queued request 5: first admit should pass, the second should be dropped")
	}
	// id 0 never executes: with tracking this gap would keep every later id in above
	for id := int64(1); id <= 1000; id++ {
		f.markExecuted(msg(id))
	}
	if n := len(f.executed); n != 0 {
		t.Fatalf("executed trackers = %d, want 0 without a retry path", n)
	}
	if _, ok := f.queued[requestKey{client: "c", id: 5}]; ok {
		t.Fatal("request 5 still queued after it executed")
	}
	f.markExecuted(msg(7))
	if _, dups := f.takeCounters(); dups != 0 {
		t.Fatalf("duplicates = %d, want 0 (not tracked without a retry path)", dups)
	}
}
