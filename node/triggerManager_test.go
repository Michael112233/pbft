package node

import (
	"testing"
	"time"

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
		other.SwitchTriggerMode(test.mode)
		if other.GetProgressTimeout() != want || other.GetNewViewTimeout() != want {
			t.Fatalf("switch to mode %d: got timeouts %v/%v, want %v", test.mode, other.GetProgressTimeout(), other.GetNewViewTimeout(), want)
		}
	}
}
