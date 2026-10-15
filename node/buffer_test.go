package node

import (
	"testing"

	"github.com/michael112233/pbft/core"
)

// A Prepare or Commit for a future view is kept whether or not the node is in a view
// change, one below forView is dropped on arrival, and entering a higher forView
// prunes what can no longer be installed.
func TestBufferFutureViewMessages(t *testing.T) {
	n := refsTestNode() // installed (1,1), not in a view change
	v2 := core.ViewID{Generation: 1, Counter: 2}
	v3 := core.ViewID{Generation: 1, Counter: 3}

	// Before this change both were dropped: the node had not entered the view change.
	n.HandlePrepare(core.PrepareMsg{View: v2, SeqNum: 5, From: 3}, []byte("sig"))
	n.HandleCommit(core.CommitMsg{View: v2, SeqNum: 5, From: 3})
	if len(n.bufferedMsgs) != 2 {
		t.Fatalf("buffered %d messages outside a view change, want 2", len(n.bufferedMsgs))
	}
	for _, msg := range n.bufferedMsgs {
		if !msg.outsideViewChange {
			t.Fatalf("message kind %d not marked as buffered outside a view change", msg.kind)
		}
	}

	// The node moves on to (1,3): (1,2) can no longer be installed here.
	n.viewChangeRunning = true
	n.forViewID = v3
	n.pruneBelowForView(v3)
	if len(n.bufferedMsgs) != 0 {
		t.Fatalf("%d messages below forView left after prune", len(n.bufferedMsgs))
	}

	n.HandlePrepare(core.PrepareMsg{View: v2, SeqNum: 6, From: 3}, []byte("sig"))
	if len(n.bufferedMsgs) != 0 {
		t.Fatal("Prepare below forView was buffered")
	}
	n.HandlePrepare(core.PrepareMsg{View: v3, SeqNum: 6, From: 3}, []byte("sig"))
	if len(n.bufferedMsgs) != 1 || n.bufferedMsgs[0].outsideViewChange {
		t.Fatalf("Prepare for forView not buffered as in a view change: %+v", n.bufferedMsgs)
	}

	if got := n.drainBufferedMessagesForView(v3); len(got) != 1 || got[0].prepare.SeqNum != 6 {
		t.Fatalf("drained %+v for (1,3)", got)
	}
	if len(n.bufferedMsgs) != 0 {
		t.Fatalf("%d messages left after draining", len(n.bufferedMsgs))
	}
}
