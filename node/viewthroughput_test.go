package node

import (
	"math"
	"testing"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/logger"
)

// newViewThroughputTestNode returns a node whose perf window for view opened at t0.
func newViewThroughputTestNode(perf config.PerformanceConfig, view core.ViewID, t0 time.Time) *Node {
	perf.Enabled = true
	return &Node{
		cfg: &config.Config{Performance: perf},
		log: logger.NewLogger(0, "viewthroughputtest"),
		throughputPerf: ThroughputPerf{
			viewThroughputs:         make(map[core.ViewID]float64),
			maxCounterByGeneration:  make(map[uint64]uint64),
			timedIntervalStart:      t0,
			timedObservationStarted: true,
			record:                  viewRecord{view: view},
		},
	}
}

// execute feeds count slots, starting at seq from, evenly spaced at rate slots/s
// from start. It returns the next seq and the time of the last slot.
func execute(n *Node, from, count int64, start time.Time, rate float64) (int64, time.Time) {
	gap := time.Duration(float64(time.Second) / rate)
	at := start
	for i := int64(0); i < count; i++ {
		at = start.Add(time.Duration(i) * gap)
		n.observeExecutedSlotForViewThroughput(from+i, at)
	}
	return from + count, at
}

func near(a, b float64) bool { return math.Abs(a-b) < 0.5 }

// The burst right after the view change lands inside the grace and is not measured;
// the record is the rate of the first complete interval after the anchor.
func TestViewRecordSkipsGraceAndBurst(t *testing.T) {
	view := core.ViewID{Generation: 1, Counter: 5}
	t0 := time.Unix(100, 0)
	n := newViewThroughputTestNode(config.PerformanceConfig{}, view, t0)

	seq, _ := execute(n, 1000, 40, t0, 800)                          // burst, all inside the 1 s grace
	seq, _ = execute(n, seq, 120, t0.Add(100*time.Millisecond), 160) // rest of the grace
	if _, ok := n.throughputPerf.viewThroughputs[view]; ok {
		t.Fatal("record written during the grace")
	}
	execute(n, seq, 251, t0.Add(time.Second), 160) // anchor + one full interval

	got, ok := n.throughputPerf.viewThroughputs[view]
	if !ok || !near(got, 160) {
		t.Fatalf("record = %.2f (ok=%t), want 160 (burst excluded)", got, ok)
	}
}

// A view whose measurable stretch is shorter than one interval has no record, and
// an unfinished interval at the end of a view never counts.
func TestViewRecordNeedsCompleteInterval(t *testing.T) {
	view := core.ViewID{Generation: 1, Counter: 6}
	t0 := time.Unix(100, 0)
	n := newViewThroughputTestNode(config.PerformanceConfig{}, view, t0)

	execute(n, 2000, 200, t0.Add(time.Second), 160) // anchor + 199 slots
	if _, ok := n.throughputPerf.viewThroughputs[view]; ok {
		t.Fatal("record written without a complete interval")
	}
}

// Two fast intervals, then one that the throttle slows down but that still
// completes. max keeps the fastest interval; mean is total slots over total time.
func TestViewRecordStrategies(t *testing.T) {
	run := func(strategy string) float64 {
		view := core.ViewID{Generation: 1, Counter: 7}
		t0 := time.Unix(100, 0)
		n := newViewThroughputTestNode(config.PerformanceConfig{ViewStrategy: strategy}, view, t0)
		seq, last := execute(n, 3000, 501, t0.Add(time.Second), 160) // anchor + 2 intervals at 160
		// third interval: 240 slots at 160, then 10 slots at 10/s (throttled)
		seq, last = execute(n, seq, 240, last.Add(time.Second/160), 160)
		execute(n, seq, 10, last.Add(time.Second/10), 10)
		return n.throughputPerf.viewThroughputs[view]
	}

	if got := run(config.PerfViewStrategyMax); !near(got, 160) {
		t.Fatalf("max: record = %.2f, want 160 (throttled interval ignored)", got)
	}
	// 750 slots: 500/160 + 240/160 + 10/10 = 5.625 s
	if got, want := run(config.PerfViewStrategyMean), 750/5.625; !near(got, want) {
		t.Fatalf("mean: record = %.2f, want %.2f", got, want)
	}
	if got := run(""); !near(got, 160) {
		t.Fatalf("default strategy: record = %.2f, want max (160)", got)
	}
}

func TestViewRecordConfigurableIntervalAndGrace(t *testing.T) {
	view := core.ViewID{Generation: 1, Counter: 8}
	t0 := time.Unix(100, 0)
	n := newViewThroughputTestNode(config.PerformanceConfig{IntervalSlots: 50, GraceMs: -1}, view, t0)

	execute(n, 10, 51, t0, 100) // no grace: anchor at the window open, then 50 slots
	if got := n.throughputPerf.viewThroughputs[view]; !near(got, 100) {
		t.Fatalf("record = %.2f, want 100", got)
	}
}

func TestMaxRecentViewThroughputReportsDefault(t *testing.T) {
	n := newViewThroughputTestNode(config.PerformanceConfig{DefaultMaxThroughput: 150}, core.ViewID{}, time.Time{})
	n.fNodes = 2
	current := core.ViewID{Generation: 1, Counter: 20}

	got, fromDefault := n.maxRecentViewThroughput(current)
	if !fromDefault || got != 150 {
		t.Fatalf("empty window: got (%.2f, %t), want (150, true)", got, fromDefault)
	}

	n.throughputPerf.viewThroughputs[core.ViewID{Generation: 1, Counter: 15}] = 161
	n.throughputPerf.maxCounterByGeneration[1] = 15
	got, fromDefault = n.maxRecentViewThroughput(current)
	if fromDefault || got != 161 {
		t.Fatalf("one record in window: got (%.2f, %t), want (161, false)", got, fromDefault)
	}
}
