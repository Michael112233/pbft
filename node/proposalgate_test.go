package node

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/logger"
)

func TestProposalGateBlocks(t *testing.T) {
	now := time.Now()
	for _, tt := range []struct {
		name     string
		on       bool
		interval time.Duration
		last     time.Time
		want     bool
	}{
		{name: "gate off never blocks", on: false, interval: 10 * time.Millisecond, last: now, want: false},
		{name: "zero interval never blocks", on: true, interval: 0, last: now, want: false},
		{name: "no prior proposal never blocks", on: true, interval: 10 * time.Millisecond, last: time.Time{}, want: false},
		{name: "recent proposal blocks", on: true, interval: time.Second, last: now, want: true},
		{name: "old proposal does not block", on: true, interval: 10 * time.Millisecond, last: now.Add(-time.Second), want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			n := &Node{proposalGateOn: tt.on, proposalMinInterval: tt.interval, lastProposalAt: tt.last}
			got := n.proposalGateBlocks()
			if got != tt.want {
				t.Fatalf("proposalGateBlocks() = %v, want %v", got, tt.want)
			}
			// When it blocks it must have armed the retry timer; when it does not,
			// it must not have.
			if tt.want && n.proposalGateTimerCh == nil {
				t.Fatal("blocked but retry timer not armed")
			}
			if !tt.want && n.proposalGateTimerCh != nil {
				t.Fatal("did not block but retry timer armed")
			}
			n.stopProposalGateTimer()
		})
	}
}

func TestProposalGateTimerRearmIsSafe(t *testing.T) {
	n := &Node{proposalGateOn: true, proposalMinInterval: time.Hour, lastProposalAt: time.Now()}
	if !n.proposalGateBlocks() {
		t.Fatal("expected a block")
	}
	// A second block re-arms the same timer without leaking or panicking.
	if !n.proposalGateBlocks() {
		t.Fatal("expected a second block")
	}
	n.stopProposalGateTimer()
	if n.proposalGateTimerCh != nil {
		t.Fatal("stop did not clear the channel")
	}
}

// A command the handler gave up on (deadline expired after it was queued) must
// not be applied by the loop: the controller has recorded it as failed, so
// applying it would gate a node no slot is tracking.
func TestThrottleGateCancelledCmdIsNotApplied(t *testing.T) {
	n := &Node{log: logger.NewLogger(0, "gatetest")}
	cmd := throttleGateCmd{enable: true, applied: make(chan error, 1), claimed: &atomic.Bool{}}

	if !cmd.claim() { // stand in for the handler's ctx.Done() branch
		t.Fatal("handler could not claim an unclaimed command")
	}
	n.handleThrottleGateCmd(cmd)

	if n.proposalGateOn {
		t.Fatal("loop applied a command the handler had cancelled")
	}
	select {
	case <-cmd.applied:
		t.Fatal("a verdict was sent for a cancelled command")
	default:
	}
}

// The normal path: the loop wins the claim, acks before doing any further work,
// and applies the flag.
func TestThrottleGateAppliedCmdAcksEarly(t *testing.T) {
	n := &Node{log: logger.NewLogger(0, "gatetest")}
	cmd := throttleGateCmd{enable: true, applied: make(chan error, 1), claimed: &atomic.Bool{}}

	n.handleThrottleGateCmd(cmd)

	if !n.proposalGateOn {
		t.Fatal("gate not enabled")
	}
	select {
	case err := <-cmd.applied:
		if err != nil {
			t.Fatalf("verdict %v, want nil", err)
		}
	default:
		t.Fatal("no verdict sent, so the handler would block until its deadline")
	}
	// The handler can no longer cancel a command the loop has claimed.
	if cmd.claim() {
		t.Fatal("handler claimed a command the loop had already applied")
	}
}

func TestThrottleGateEnableFor(t *testing.T) {
	if enable, ok := throttleGateEnableFor("ThrottleGateOn"); !ok || !enable {
		t.Fatalf("ThrottleGateOn => (%v,%v)", enable, ok)
	}
	if enable, ok := throttleGateEnableFor("ThrottleGateOff"); !ok || enable {
		t.Fatalf("ThrottleGateOff => (%v,%v)", enable, ok)
	}
	if _, ok := throttleGateEnableFor("LeaderStall"); ok {
		t.Fatal("LeaderStall should not be a gate command")
	}
}

// In scenario mode the gate may only be enabled in a Throttle generation: an
// enable elsewhere is refused (and the client retries), a disable always applies.
func TestThrottleGateScenarioRule(t *testing.T) {
	tests := []struct {
		name       string
		scenario   bool
		applied    bool
		curr       core.Scenario
		enable     bool
		gateBefore bool
		wantErr    bool
		wantGateOn bool
	}{
		{"static run: enable allowed", false, false, core.ScenarioHealthy, true, false, false, true},
		{"Throttle generation: enable allowed", true, true, core.ScenarioThrottle, true, false, false, true},
		{"Healthy generation: enable refused", true, true, core.ScenarioHealthy, true, false, true, false},
		{"ProposalDelay generation: enable refused", true, true, core.ScenarioProposalDelay, true, false, true, false},
		{"before the first scenario is applied: enable refused", true, false, core.ScenarioThrottle, true, false, true, false},
		{"Healthy generation: disable is never refused", true, true, core.ScenarioHealthy, false, false, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := &Node{log: logger.NewLogger(0, "gatetest"), scenarioMode: tt.scenario,
				scenarioApplied: tt.applied, currScenario: tt.curr, proposalGateOn: tt.gateBefore}
			cmd := throttleGateCmd{enable: tt.enable, applied: make(chan error, 1), claimed: &atomic.Bool{}}
			n.handleThrottleGateCmd(cmd)
			var err error
			select {
			case err = <-cmd.applied:
			default:
				t.Fatal("no verdict sent")
			}
			if (err != nil) != tt.wantErr || n.proposalGateOn != tt.wantGateOn {
				t.Fatalf("verdict %v gate %v, want error %v gate %v", err, n.proposalGateOn, tt.wantErr, tt.wantGateOn)
			}
		})
	}
}

// Leaving the Throttle scenario turns the gate off on the node itself; staying
// in it (or entering it) leaves the gate alone.
func TestClearThrottleGateOnScenarioSwitch(t *testing.T) {
	for _, tt := range []struct {
		next core.Scenario
		want bool
	}{
		{core.ScenarioHealthy, false},
		{core.ScenarioProposalDelay, false},
		{core.ScenarioNetworkDelayFCrash, false},
		{core.ScenarioThrottle, true},
	} {
		n := &Node{log: logger.NewLogger(0, "gatetest"), proposalGateOn: true}
		n.clearThrottleGateOnScenarioSwitch(tt.next)
		if n.proposalGateOn != tt.want {
			t.Fatalf("switch to %s: gate %v, want %v", core.ScenarioToString(tt.next), n.proposalGateOn, tt.want)
		}
	}
}
