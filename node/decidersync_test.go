package node

import (
	"reflect"
	"testing"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/logger"
)

func TestUnsentGenerations(t *testing.T) {
	tests := []struct {
		name          string
		sent, leaving uint64
		want          []uint64
	}{
		{"normal path: leaving the generation just sent", 4, 4, nil},
		{"caught up before the aggregate", 3, 4, []uint64{4}},
		{"two generations missing", 2, 4, []uint64{3, 4}},
		{"nothing sent yet, leaving generation 1", 0, 1, []uint64{1}},
		{"already sent further than leaving", 5, 4, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := unsentGenerations(tt.sent, tt.leaving); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("unsentGenerations(%d, %d) = %v, want %v", tt.sent, tt.leaving, got, tt.want)
			}
		})
	}
}

// A node whose decider is the oracle, so every send comes back as a decision on
// learningAgentDecisionCh and the test can see exactly which generations went out.
func newDeciderTestNode(t *testing.T) *Node {
	t.Chdir(t.TempDir())
	return &Node{
		log: logger.NewLogger(1, "node"),
		cfg: &config.Config{
			OracleMode:            true,
			OracleDecisionDelayMs: 1,
			OracleActionsEnum:     []core.Action{core.FixedRoundRobin},
		},
		learningAgentDecisionCh: make(chan core.LearningAgentDecision, 8),
		eventLoopStopCh:         make(chan struct{}),
	}
}

func decidedGenerations(t *testing.T, n *Node, want int) []uint64 {
	t.Helper()
	var gens []uint64
	for len(gens) < want {
		select {
		case d := <-n.learningAgentDecisionCh:
			gens = append(gens, d.Generation)
		case <-time.After(2 * time.Second):
			t.Fatalf("got decisions for %v, waited for %d", gens, want)
		}
	}
	select { // nothing extra may follow
	case d := <-n.learningAgentDecisionCh:
		t.Fatalf("unexpected extra decision for generation %d after %v", d.Generation, gens)
	case <-time.After(50 * time.Millisecond):
	}
	return gens
}

// Normal path: the aggregate's data went out, so leaving the generation sends nothing.
func TestFillDeciderGapNormalPath(t *testing.T) {
	n := newDeciderTestNode(t)
	n.sendEpochToDecider(4, core.FixedRoundRobin, core.EpochData{})
	n.fillDeciderGap(4, core.FixedRoundRobin)
	if got := decidedGenerations(t, n, 1); !reflect.DeepEqual(got, []uint64{4}) {
		t.Fatalf("decided %v, want [4]", got)
	}
}

// Node 1 in the old run: caught up into generation 5 before gen 4's aggregate
// arrived. Leaving 4 must send it, once, and the next aggregate continues at 5.
// Each step waits for its decision, as consecutive sends are an epoch apart in a run
// (each send is its own goroutine, so back-to-back sends have no fixed order).
func TestFillDeciderGapCatchUpBeforeAggregate(t *testing.T) {
	n := newDeciderTestNode(t)
	n.sendEpochToDecider(3, core.FixedRoundRobin, core.EpochData{})
	n.fillDeciderGap(3, core.FixedRoundRobin) // normal switch out of 3: nothing more
	if got := decidedGenerations(t, n, 1); !reflect.DeepEqual(got, []uint64{3}) {
		t.Fatalf("decided %v, want [3]", got)
	}
	n.fillDeciderGap(4, core.FixedRoundRobin) // caught up out of 4, no aggregate
	if got := decidedGenerations(t, n, 1); !reflect.DeepEqual(got, []uint64{4}) {
		t.Fatalf("decided %v, want [4]", got)
	}
	if n.deciderSentGen != 4 {
		t.Fatalf("deciderSentGen = %d, want 4", n.deciderSentGen)
	}
	n.fillDeciderGap(4, core.FixedRoundRobin) // a repeat must not send 4 again
	n.sendEpochToDecider(5, core.FixedRoundRobin, core.EpochData{})
	if got := decidedGenerations(t, n, 1); !reflect.DeepEqual(got, []uint64{5}) {
		t.Fatalf("decided %v, want [5]", got)
	}
}

// Several missing generations go out in order, on one goroutine.
func TestFillDeciderGapSendsInOrder(t *testing.T) {
	n := newDeciderTestNode(t)
	n.fillDeciderGap(3, core.FixedRoundRobin)
	if got := decidedGenerations(t, n, 3); !reflect.DeepEqual(got, []uint64{1, 2, 3}) {
		t.Fatalf("decided %v, want [1 2 3]", got)
	}
}

// The epoch manager hands both the aggregator's own aggregate and a received one
// to the decider through sendEpochToDecider, so deciderSentGen sees every send.
func TestEpochManagerHandsAggregateToDecider(t *testing.T) {
	stub := &epochNodeStub{nodeID: 2, quorum: 3, nodeCount: 4, forView: core.ViewID{Generation: 7, Counter: 1}}
	t.Chdir(t.TempDir())
	em := NewEpochManager(logger.NewLogger(2, "node"), stub)
	em.HandleEpochAggregateMsg(core.EpochAggregateMsg{EpochGeneration: 7, From: 4, CurrentAction: stub.currAction}, nil)
	// a stale aggregate (node already moved on) is not handed over
	em.HandleEpochAggregateMsg(core.EpochAggregateMsg{EpochGeneration: 6, From: 4, CurrentAction: stub.currAction}, nil)
	if !reflect.DeepEqual(stub.deciderGens, []uint64{7}) {
		t.Fatalf("decider got generations %v, want [7]", stub.deciderGens)
	}
}
