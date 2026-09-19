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
