package node

import (
	"testing"
	"time"
)

// The worked example in docs/epoch-grid.md: 75 s epochs anchored at seq 1 in
// generation 1, decisions applied δ = 1.4 s after each timer fires.
func TestEpochGridDelay(t *testing.T) {
	const period = 75 * time.Second
	anchor := time.Unix(1_000_000, 0)
	at := func(sec float64) time.Time { return anchor.Add(time.Duration(sec * float64(time.Second))) }

	tests := []struct {
		name      string
		gen       uint64
		now       time.Time
		wantDelay time.Duration
		wantLate  time.Duration
	}{
		{"gen 1 armed at seq 1", 1, at(0), 75 * time.Second, 0},
		// decision for gen 1 applied at 76.4 s: gen 2 fires at 150 s, not 151.4 s
		{"gen 2 armed after a 1.4 s decision", 2, at(76.4), 73600 * time.Millisecond, 0},
		{"gen 3 armed after a 1.4 s decision", 3, at(151.4), 73600 * time.Millisecond, 0},
		// the scenario switch into gen 101 sits on the grid whatever δ is
		{"gen 101", 101, at(7500 + 1.4), 73600 * time.Millisecond, 0},
		// a node pulled forward by f+1 amplification lands on the same deadline
		{"catch-up into gen 6", 6, at(376), 74 * time.Second, 0},
		{"two-generation jump into gen 7", 7, at(376), 149 * time.Second, 0},
		// a decision that took longer than a whole epoch: fire now, report lateness
		{"deadline already past", 10, at(755), 0, 5 * time.Second},
		{"exactly on the deadline", 2, at(150), 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			delay, late := epochGridDelay(anchor, 1, tt.gen, period, tt.now)
			if delay != tt.wantDelay || late != tt.wantLate {
				t.Fatalf("gen %d at %v: got delay %v late %v, want delay %v late %v",
					tt.gen, tt.now.Sub(anchor), delay, late, tt.wantDelay, tt.wantLate)
			}
		})
	}
}

// The grid is relative to the generation the node was in at seq 1, so a node
// that executed seq 1 in generation 3 fires generation 3 one period later.
func TestEpochGridDelayAnchorGeneration(t *testing.T) {
	const period = 75 * time.Second
	anchor := time.Unix(1_000_000, 0)
	if delay, _ := epochGridDelay(anchor, 3, 3, period, anchor); delay != period {
		t.Fatalf("anchor generation: delay %v, want %v", delay, period)
	}
	if delay, _ := epochGridDelay(anchor, 3, 5, period, anchor); delay != 3*period {
		t.Fatalf("two generations past the anchor: delay %v, want %v", delay, 3*period)
	}
	// generations never decrease, but a lower one must not wrap around
	if delay, _ := epochGridDelay(anchor, 3, 2, period, anchor); delay != period {
		t.Fatalf("generation below the anchor: delay %v, want %v", delay, period)
	}
}
