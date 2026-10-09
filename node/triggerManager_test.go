package node

import (
	"testing"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
)

func TestTriggerManagerTimeoutsFollowMode(t *testing.T) {
	// deliberately not the defaults, so a leftover hardcoded value would show
	const periodic, fixed = 7 * time.Second, 90 * time.Millisecond
	tests := []struct {
		mode core.TriggerMode
		want time.Duration
	}{
		{core.FixedTrigger, fixed},
		{core.PerfTrigger, fixed},
		{core.PeriodicTrigger, periodic},
	}
	for _, test := range tests {
		want := test.want

		tm := NewTriggerManager(nil, test.mode, nil, periodic, fixed)
		if tm.GetTriggerMode() != test.mode || tm.GetProgressTimeout() != want || tm.GetNewViewTimeout() != want {
			t.Fatalf("init mode %d: got timeouts %v/%v, want %v", test.mode, tm.GetProgressTimeout(), tm.GetNewViewTimeout(), want)
		}

		// switching from the opposite mode must not leave a stale timeout behind
		start := core.PeriodicTrigger
		if test.mode == core.PeriodicTrigger {
			start = core.FixedTrigger
		}
		other := NewTriggerManager(nil, start, nil, periodic, fixed)
		other.SwitchTriggerMode(test.mode, fixed)
		if other.GetProgressTimeout() != want || other.GetNewViewTimeout() != want {
			t.Fatalf("switch to mode %d: got timeouts %v/%v, want %v", test.mode, other.GetProgressTimeout(), other.GetNewViewTimeout(), want)
		}
	}
}

// A switch carries the new generation's Fixed/Perf floor; Periodic ignores it.
func TestTriggerManagerSwitchSetsFixedFloor(t *testing.T) {
	const periodic, strict, relaxed = 10 * time.Second, 150 * time.Millisecond, 350 * time.Millisecond
	tm := NewTriggerManager(nil, core.FixedTrigger, nil, periodic, strict)
	tm.SwitchTriggerMode(core.PerfTrigger, relaxed)
	if tm.GetProgressTimeout() != relaxed || tm.GetNewViewTimeout() != relaxed {
		t.Fatalf("Perf with relaxed floor: got %v/%v, want %v", tm.GetProgressTimeout(), tm.GetNewViewTimeout(), relaxed)
	}
	tm.SwitchTriggerMode(core.PeriodicTrigger, strict)
	if tm.GetProgressTimeout() != periodic {
		t.Fatalf("Periodic: got %v, want %v", tm.GetProgressTimeout(), periodic)
	}
	tm.SwitchTriggerMode(core.FixedTrigger, strict)
	if tm.GetProgressTimeout() != strict {
		t.Fatalf("Fixed back to strict floor: got %v, want %v", tm.GetProgressTimeout(), strict)
	}
}

// In scenario mode the floor follows the generation's scenario: strict in the
// network-delay scenarios, relaxed elsewhere; outside scenario mode always strict.
func TestFixedFloorForGeneration(t *testing.T) {
	scenarios := []core.Scenario{core.ScenarioProposalDelay, core.ScenarioNetworkDelay,
		core.ScenarioNetworkDelayFCrash, core.ScenarioThrottle, core.ScenarioHealthy}
	cfg := &config.Config{ScenarioMode: true, ScenariosEnum: scenarios, ScenarioGenerations: 100,
		Timer: config.TimerConfig{FixedTriggerTimeoutMs: 150, RelaxedFixedTriggerTimeoutMs: 350}}
	n := &Node{cfg: cfg}
	for _, tt := range []struct {
		gen  uint64
		want time.Duration
	}{
		{1, 350 * time.Millisecond},   // ProposalDelay
		{100, 350 * time.Millisecond}, // last ProposalDelay generation
		{101, 150 * time.Millisecond}, // NetworkDelay
		{250, 150 * time.Millisecond}, // NetworkDelayFCrash
		{301, 350 * time.Millisecond}, // Throttle
		{499, 350 * time.Millisecond}, // Healthy
		{601, 150 * time.Millisecond}, // cycle: NetworkDelay again
	} {
		if got := n.fixedFloorForGeneration(tt.gen); got != tt.want {
			t.Errorf("gen %d (%s): floor %v, want %v", tt.gen,
				core.ScenarioToString(scenarioForGeneration(tt.gen, scenarios, 100)), got, tt.want)
		}
	}
	n.cfg = &config.Config{Timer: config.TimerConfig{FixedTriggerTimeoutMs: 150, RelaxedFixedTriggerTimeoutMs: 350}}
	if got := n.fixedFloorForGeneration(101); got != 150*time.Millisecond {
		t.Errorf("no scenario mode: floor %v, want 150ms", got)
	}
}
