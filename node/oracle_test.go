package node

import (
	"testing"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
)

func TestOracleActionRoundRobinCyclesThroughActions(t *testing.T) {
	n := &Node{cfg: &config.Config{
		OracleActionsEnum: []core.Action{core.FixedRoundRobin, core.PeriodicRoundRobin, core.PeriodicElection},
	}}

	got := []core.Action{
		n.oracleAction(1),
		n.oracleAction(2),
		n.oracleAction(3),
		n.oracleAction(4),
	}
	// epoch=1 skips index 0 (FixedRoundRobin) since gen 1 already starts
	// there by default; it cycles back once epoch is a multiple of len.
	want := []core.Action{core.PeriodicRoundRobin, core.PeriodicElection, core.FixedRoundRobin, core.PeriodicRoundRobin}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("oracleAction(%d) = %v, want %v", i+1, got[i], want[i])
		}
	}
}

func TestOracleActionRandomIsDeterministicAcrossNodes(t *testing.T) {
	actions := []core.Action{core.FixedRoundRobin, core.PeriodicRoundRobin, core.PeriodicElection}

	n1 := &Node{cfg: &config.Config{OracleRandom: true, OracleSeed: 42, OracleActionsEnum: actions}}
	n2 := &Node{cfg: &config.Config{OracleRandom: true, OracleSeed: 42, OracleActionsEnum: actions}}

	for epoch := uint64(1); epoch <= 10; epoch++ {
		a1 := n1.oracleAction(epoch)
		a2 := n2.oracleAction(epoch)
		if a1 != a2 {
			t.Fatalf("oracleAction(%d) diverged across nodes with same seed: %v vs %v", epoch, a1, a2)
		}
	}
}

func TestOracleActionDefaultsToFixedRoundRobinWhenNoActionsConfigured(t *testing.T) {
	n := &Node{cfg: &config.Config{}}
	if got := n.oracleAction(1); got != core.FixedRoundRobin {
		t.Fatalf("oracleAction() = %v, want %v", got, core.FixedRoundRobin)
	}
}
