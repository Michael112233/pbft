package node

import (
	"testing"
	"time"

	"github.com/michael112233/pbft/core"
)

func TestTriggerManagerTimeoutsFollowMode(t *testing.T) {
	// deliberately not the defaults, and progress != new-view, so a leftover
	// hardcoded or shared value would show
	const periodic, progress, newView = 7 * time.Second, 90 * time.Millisecond, 130 * time.Millisecond
	tests := []struct {
		mode          core.TriggerMode
		wantP, wantNV time.Duration
	}{
		{core.FixedTrigger, progress, newView},
		{core.PerfTrigger, progress, newView},
		{core.PeriodicTrigger, periodic, periodic},
	}
	for _, test := range tests {
		tm := NewTriggerManager(nil, test.mode, nil, periodic, progress, newView)
		if tm.GetTriggerMode() != test.mode || tm.GetProgressTimeout() != test.wantP || tm.GetNewViewTimeout() != test.wantNV {
			t.Fatalf("init mode %d: got timeouts %v/%v, want %v/%v", test.mode,
				tm.GetProgressTimeout(), tm.GetNewViewTimeout(), test.wantP, test.wantNV)
		}

		// switching from the opposite mode must not leave a stale timeout behind
		start := core.PeriodicTrigger
		if test.mode == core.PeriodicTrigger {
			start = core.FixedTrigger
		}
		other := NewTriggerManager(nil, start, nil, periodic, progress, newView)
		other.SwitchTriggerMode(test.mode)
		if other.GetProgressTimeout() != test.wantP || other.GetNewViewTimeout() != test.wantNV {
			t.Fatalf("switch to mode %d: got timeouts %v/%v, want %v/%v", test.mode,
				other.GetProgressTimeout(), other.GetNewViewTimeout(), test.wantP, test.wantNV)
		}
	}
}
