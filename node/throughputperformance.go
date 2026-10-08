package node

import (
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
)

type ThroughputPerf struct {
	// viewThroughputs holds each view's throughput record (slots/s), written by
	// observeExecutedSlotForViewThroughput and read by maxRecentViewThroughput.
	viewThroughputs map[core.ViewID]float64
	// maxCounterByGeneration tracks, for each generation seen so far, the
	// highest Counter recorded in viewThroughputs. Kept up to date on every
	// write so looking up "where did the previous generation leave off" is
	// O(1) instead of a scan over viewThroughputs.
	maxCounterByGeneration map[uint64]uint64

	// Perf window. It opens at the first executed slot at or past
	// timedIntervalStartSeq (maxSeq + PerfWindowDelaySlots() of the NewView).
	// The perf timer measures the average since timedIntervalStart once a second
	// and raises timedTargetThroughput (the bar) while the leader beats it.
	timedIntervalStart      time.Time
	timedIntervalStartSeq   int64
	timedTargetThroughput   float64
	timedObservationStarted bool

	// record measures the current view's throughput record.
	record viewRecord
}

// viewRecord is the measurement state for one view's throughput record: back-to-back
// intervals of PerfIntervalSlots executed slots, the first starting at the anchor.
type viewRecord struct {
	view             core.ViewID
	anchored         bool
	anchorSeq        int64
	anchorTime       time.Time
	lastBoundarySeq  int64
	lastBoundaryTime time.Time
	intervals        int
	fastest          float64 // fastest complete interval, slots/s
}

// observeExecutedSlotForTimedThroughput only opens the measurement window for the
// timed trigger: the first executed slot at or above the post-new-view delay seq
// pins the interval start and arms the perf timer. Everything else (measuring,
// raising the bar, triggering the view change) happens in handlePerfTimerTimeout.
func (n *Node) observeExecutedSlotForTimedThroughput(seq int64, now time.Time) {
	if seq >= n.throughputPerf.timedIntervalStartSeq && !n.throughputPerf.timedObservationStarted {
		n.log.Info("Timed trigger: interval start seq %d reached at seq %d, starting timing and perf timer", n.throughputPerf.timedIntervalStartSeq, seq)
		n.throughputPerf.timedIntervalStart = now
		n.throughputPerf.timedIntervalStartSeq = seq
		n.throughputPerf.timedObservationStarted = true
		n.resetPerfTimer()
	}
}

// resetViewRecord starts measuring a new view. Called on every NewView install;
// the genesis view gets its state from the constructor.
func (n *Node) resetViewRecord(view core.ViewID) {
	n.throughputPerf.record = viewRecord{view: view}
}

// observeExecutedSlotForViewThroughput measures the current view's throughput
// record, Aardvark-style. The anchor is the first slot executed PerfGrace() after
// the perf window opened, so the burst right after the view change is not
// measured. From the anchor, every PerfIntervalSlots() executed slots close an
// interval, and each closed interval updates the view's record:
//   - max:  the fastest complete interval so far;
//   - mean: total slots over total time of the complete intervals.
//
// An unfinished interval at the end of the view never counts, and a view with no
// complete interval has no record. Nothing executes during a view change, so a
// record only ever covers time inside its own view.
func (n *Node) observeExecutedSlotForViewThroughput(seq int64, now time.Time) {
	if !n.throughputPerf.timedObservationStarted {
		return
	}
	r := &n.throughputPerf.record
	if !r.anchored {
		if now.Sub(n.throughputPerf.timedIntervalStart) < n.cfg.PerfGrace() {
			return
		}
		r.anchored = true
		r.anchorSeq, r.anchorTime = seq, now
		r.lastBoundarySeq, r.lastBoundaryTime = seq, now
		return
	}

	slots := seq - r.lastBoundarySeq
	elapsed := now.Sub(r.lastBoundaryTime).Seconds()
	if slots < n.cfg.PerfIntervalSlots() || elapsed <= 0 {
		return
	}
	rate := float64(slots) / elapsed
	r.intervals++
	r.lastBoundarySeq, r.lastBoundaryTime = seq, now
	if rate > r.fastest {
		r.fastest = rate
	}

	record := r.fastest
	if n.cfg.PerfViewStrategy() == config.PerfViewStrategyMean {
		record = float64(seq-r.anchorSeq) / now.Sub(r.anchorTime).Seconds()
	}
	n.throughputPerf.viewThroughputs[r.view] = record
	if r.view.Counter > n.throughputPerf.maxCounterByGeneration[r.view.Generation] {
		n.throughputPerf.maxCounterByGeneration[r.view.Generation] = r.view.Counter
	}
	n.log.Info("PERF RECORD: view (%d,%d) interval %d: %d slots in %.3f s = %.2f slots/s; view record %.2f (%s of %d intervals)",
		r.view.Generation, r.view.Counter, r.intervals, slots, elapsed, rate, record, n.cfg.PerfViewStrategy(), r.intervals)
}

// in new view first wait for roughly 3 seq number then window open , anchor done 1s after so grace is 3 slot and 1s
//
//   - The 3 slots are counted from maxSeq, not from the install. The window opens at
//     the first executed slot with seq >= maxSeq + 3 (performance.window_delay_slots).
//     Slots <= maxSeq (re-proposed from the old view) that are still unexecuted
//     don't count toward the 3.
//   - The 1 s is what does the work. In time, the 3 slots took about 0.17 s after
//     install in the pilot. Most of that is waiting for the first new slot to
//     execute, since the slots themselves run in a few ms. The burst (~20 extra
//     slots) arrives in the first ~100 ms after the window opens, so the 3 slots
//     skip none of it; the 1 s skips all of it.
//   - So from install, measuring starts after about 1.17 s, at the first slot
//     executed 1 s after the window opens.
// these 3 seq are above maxseq so slots rexecuted ignored, so these three can take 0.17s (includes below max seq)
