package node

import (
	"testing"
	"time"

	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/logger"
)

func newViewThroughputTestNode(startSeq int64) *Node {
	return &Node{
		log: logger.NewLogger(0, "viewthroughputtest"),
		throughputPerf: ThroughputPerf{
			throughputIntervalStartSeq: startSeq,
			viewThroughputs:            make(map[core.ViewID]float64),
			maxCounterByGeneration:     make(map[uint64]uint64),
		},
	}
}

// The (1,288) outlier from the 300 ms PerfRR run: window opens at 57245 and the
// 250-slot boundary at 57250 executes in the same burst, recording ~16 000 slots/s.
func TestViewThroughputSkipsBoundaryInsideGrace(t *testing.T) {
	view := core.ViewID{Generation: 1, Counter: 288}
	n := newViewThroughputTestNode(57245)
	t0 := time.Unix(100, 0)

	n.observeExecutedSlotForThroughput(57245, t0, view, 1)
	n.observeExecutedSlotForThroughput(57250, t0.Add(300*time.Microsecond), view, 1)

	if got, ok := n.throughputPerf.viewThroughputs[view]; ok {
		t.Fatalf("recorded %.1f slots/s for a 0.3 ms window; want no record", got)
	}
}

func TestViewThroughputRecordsPastGrace(t *testing.T) {
	view := core.ViewID{Generation: 1, Counter: 5}
	n := newViewThroughputTestNode(240)
	t0 := time.Unix(100, 0)

	n.observeExecutedSlotForThroughput(240, t0, view, 1)
	n.observeExecutedSlotForThroughput(500, t0.Add(1600*time.Millisecond), view, 1)

	got, ok := n.throughputPerf.viewThroughputs[view]
	if !ok {
		t.Fatal("no record for a 1.6 s window")
	}
	if want := 260 / 1.6; got != want {
		t.Fatalf("throughput = %.2f, want %.2f", got, want)
	}
}

func TestMaxRecentViewThroughputReportsDefault(t *testing.T) {
	n := newViewThroughputTestNode(0)
	n.fNodes = 2
	current := core.ViewID{Generation: 1, Counter: 20}

	got, fromDefault := n.maxRecentViewThroughput(current)
	if !fromDefault || got != defaultMaxRecentThroughput {
		t.Fatalf("empty window: got (%.2f, %t), want (%.2f, true)", got, fromDefault, defaultMaxRecentThroughput)
	}
	// The fallback bar must equal the healthy bar, not have the factor applied twice.
	if bar := targetThroughputMaxFactor * got; bar != defaultTargetThroughput {
		t.Fatalf("fallback bar = %.2f, want defaultTargetThroughput %.2f", bar, float64(defaultTargetThroughput))
	}

	n.throughputPerf.viewThroughputs[core.ViewID{Generation: 1, Counter: 15}] = 161
	n.throughputPerf.maxCounterByGeneration[1] = 15
	got, fromDefault = n.maxRecentViewThroughput(current)
	if fromDefault || got != 161 {
		t.Fatalf("one record in window: got (%.2f, %t), want (161, false)", got, fromDefault)
	}
}

// Execution is bursty, so the first slot seen can be past the planned start seq.
// The count must start from that slot, not from the planned one.
func TestViewThroughputCountsFromFirstObservedSeq(t *testing.T) {
	view := core.ViewID{Generation: 1, Counter: 7}
	n := newViewThroughputTestNode(240)
	t0 := time.Unix(100, 0)

	n.observeExecutedSlotForThroughput(245, t0, view, 1)
	n.observeExecutedSlotForThroughput(500, t0.Add(2*time.Second), view, 1)

	if want, got := 255/2.0, n.throughputPerf.viewThroughputs[view]; got != want {
		t.Fatalf("throughput = %.2f, want %.2f (counted from seq 245)", got, want)
	}
}
