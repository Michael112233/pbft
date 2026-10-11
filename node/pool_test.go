package node

import (
	"testing"

	"github.com/michael112233/pbft/core"
)

func TestPoolGCUpTo(t *testing.T) {
	p := NewPool(nil)
	d := func(b byte) [32]byte { return [32]byte{b} }
	view := core.ViewID{Generation: 1, Counter: 1}
	req := core.ClientMsgSignature{}
	p.AddBatch([]core.ClientMsgSignature{req, req}, [][32]byte{d(1), d(2)}, 5, view)
	p.AddBatch([]core.ClientMsgSignature{req}, [][32]byte{d(3)}, 250, view)
	p.AddBatch([]core.ClientMsgSignature{req}, [][32]byte{d(4)}, 251, view)
	// digest re-added at a higher seq keeps the higher seq
	p.AddBatch([]core.ClientMsgSignature{req}, [][32]byte{d(1)}, 300, view)

	if got := p.GCUpTo(250); got != 2 { // d(2), d(3)
		t.Fatalf("removed %d, want 2", got)
	}
	if _, ok := p.GetBatch([][32]byte{d(1), d(4)}); !ok {
		t.Fatal("entries above stable seq must survive")
	}
	if _, ok := p.GetBatch([][32]byte{d(2)}); ok {
		t.Fatal("entry below stable seq must be gone")
	}
}

func TestPruneViewChangeState(t *testing.T) {
	v := func(g, c uint64) core.ViewID { return core.ViewID{Generation: g, Counter: c} }
	vc := []*core.ViewChangeMsgSig{{}}
	n := &Node{
		viewChangeMsgsLog: map[core.ViewID][]*core.ViewChangeMsgSig{
			v(1, 2): vc, v(1, 3): vc, v(1, 4): vc, v(2, 1): vc,
		},
		leaderIdForView: map[core.ViewID]int{v(1, 1): 1, v(1, 2): 2, v(1, 3): 3},
	}
	n.pruneViewChangeState(v(1, 3))
	if len(n.viewChangeMsgsLog) != 2 || n.viewChangeMsgsLog[v(1, 4)] == nil || n.viewChangeMsgsLog[v(2, 1)] == nil {
		t.Fatalf("only views above the installed one may remain, got %v", n.viewChangeMsgsLog)
	}
	if len(n.leaderIdForView) != 1 || n.leaderIdForView[v(1, 3)] != 3 {
		t.Fatalf("only the installed view's leader may remain, got %v", n.leaderIdForView)
	}
}

// Without an install (a cascade), moving forView must still drop everything below it,
// in the ViewChange log and in the election maps, and keep forView and views above it.
func TestPruneBelowForView(t *testing.T) {
	v := func(g, c uint64) core.ViewID { return core.ViewID{Generation: g, Counter: c} }
	vc := []*core.ViewChangeMsgSig{{}}
	views := []core.ViewID{v(1, 2), v(1, 3), v(1, 9), v(2, 1)}
	n := &Node{
		viewChangeMsgsLog: map[core.ViewID][]*core.ViewChangeMsgSig{},
		electionManager:   NewElectionManager(),
	}
	em := n.electionManager
	for _, view := range views {
		n.viewChangeMsgsLog[view] = vc
		em.votedFor[view] = 1
		em.collectVotes[view] = map[int]struct{}{1: {}}
		em.reqVoteBuffer[view] = []core.RequestVoteMsg{{}}
	}

	n.pruneBelowForView(v(1, 9))

	for _, view := range views {
		keep := !view.LessThan(v(1, 9))
		_, inLog := n.viewChangeMsgsLog[view]
		_, voted := em.votedFor[view]
		_, collected := em.collectVotes[view]
		_, buffered := em.reqVoteBuffer[view]
		for name, present := range map[string]bool{"viewChangeMsgsLog": inLog, "votedFor": voted, "collectVotes": collected, "reqVoteBuffer": buffered} {
			if present != keep {
				t.Errorf("%s: view %v present=%v, want %v", name, view, present, keep)
			}
		}
	}
}
