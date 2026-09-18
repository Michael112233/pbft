package node

import (
	"testing"

	"github.com/michael112233/pbft/core"
)

func TestTriggerManagerTimeoutsFollowMode(t *testing.T) {
	tests := []struct {
		mode core.TriggerMode
		want string
	}{
		{core.FixedTrigger, "fixed"},
		{core.PerfTrigger, "fixed"},
		{core.PeriodicTrigger, "periodic"},
	}
	for _, test := range tests {
		want := FixedTriggerTimeout
		if test.want == "periodic" {
			want = PeriodicTriggerTimeout
		}

		tm := NewTriggerManager(nil, test.mode, nil)
		if tm.GetTriggerMode() != test.mode || tm.GetProgressTimeout() != want || tm.GetNewViewTimeout() != want {
			t.Fatalf("init mode %d: got timeouts %v/%v, want %v", test.mode, tm.GetProgressTimeout(), tm.GetNewViewTimeout(), want)
		}

		// switching from the opposite mode must not leave a stale timeout behind
		start := core.PeriodicTrigger
		if test.mode == core.PeriodicTrigger {
			start = core.FixedTrigger
		}
		other := NewTriggerManager(nil, start, nil)
		other.SwitchTriggerMode(test.mode)
		if other.GetProgressTimeout() != want || other.GetNewViewTimeout() != want {
			t.Fatalf("switch to mode %d: got timeouts %v/%v, want %v", test.mode, other.GetProgressTimeout(), other.GetNewViewTimeout(), want)
		}
	}
}
