package node

import (
	"sync/atomic"
	"testing"
	"time"

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
	cmd := throttleGateCmd{enable: true, applied: make(chan struct{}), claimed: &atomic.Bool{}}

	if !cmd.claim() { // stand in for the handler's ctx.Done() branch
		t.Fatal("handler could not claim an unclaimed command")
	}
	n.handleThrottleGateCmd(cmd)

	if n.proposalGateOn {
		t.Fatal("loop applied a command the handler had cancelled")
	}
	select {
	case <-cmd.applied:
		t.Fatal("applied was closed for a cancelled command")
	default:
	}
}

// The normal path: the loop wins the claim, acks before doing any further work,
// and applies the flag.
func TestThrottleGateAppliedCmdAcksEarly(t *testing.T) {
	n := &Node{log: logger.NewLogger(0, "gatetest")}
	cmd := throttleGateCmd{enable: true, applied: make(chan struct{}), claimed: &atomic.Bool{}}

	n.handleThrottleGateCmd(cmd)

	if !n.proposalGateOn {
		t.Fatal("gate not enabled")
	}
	select {
	case <-cmd.applied:
	default:
		t.Fatal("applied was not closed, so the handler would block until its deadline")
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
