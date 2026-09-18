package node

import (
	"time"

	"github.com/michael112233/pbft/core"
)

type ThroughputPerf struct {
	throughputIntervalStart      time.Time
	throughputIntervalStartSeq   int64
	targetThroughput             float64
	throughputObservationStarted bool
	viewThroughputs              map[core.ViewID]float64
	// maxCounterByGeneration tracks, for each generation seen so far, the
	// highest Counter recorded in viewThroughputs. Kept up to date on every
	// write so looking up "where did the previous generation leave off" is
	// O(1) instead of a scan over viewThroughputs.
	maxCounterByGeneration map[uint64]uint64

	// Timed trigger state. Mirrors the seq-driven fields above, but the
	// measurement is taken by the perf timer once a second instead of at
	// checkpoint boundaries, so it keeps its own target to raise.
	timedIntervalStart      time.Time
	timedIntervalStartSeq   int64
	timedTargetThroughput   float64
	timedObservationStarted bool
}

// observeExecutedSlotForTimedThroughput only opens the measurement window for the
// timed trigger: the first executed slot at or above the post-new-view delay seq
// pins the interval start and arms the perf timer. Everything else (measuring,
// raising the bar, triggering the view change) happens in handlePerfTimerTimeout.
func (n *Node) observeExecutedSlotForTimedThroughput(seq int64, now time.Time) {
	// if !n.performanceTimedTrigger || seq <= 0 {
	// 	return
	// }

	if seq >= n.throughputPerf.timedIntervalStartSeq && !n.throughputPerf.timedObservationStarted {
		n.log.Info("Timed trigger: interval start seq %d reached at seq %d, starting timing and perf timer", n.throughputPerf.timedIntervalStartSeq, seq)
		n.throughputPerf.timedIntervalStart = now
		n.throughputPerf.timedIntervalStartSeq = seq
		n.throughputPerf.timedObservationStarted = true
		n.resetPerfTimer()
	}
}

// this will tput for seq number so full batch

func (n *Node) observeExecutedSlotForThroughput(seq int64, now time.Time, view core.ViewID, leaderId int) bool {
	if seq <= 0 {
		return false
	}

	if seq >= n.throughputPerf.throughputIntervalStartSeq && !n.throughputPerf.throughputObservationStarted {
		n.log.Info("Throughput interval start seq %d is greater than or equal to current seq %d, starting timing", n.throughputPerf.throughputIntervalStartSeq, seq)
		n.throughputPerf.throughputIntervalStart = now
		n.throughputPerf.throughputObservationStarted = true
		return false
	}

	if seq%CHECKPOINT_INTERVAL != 0 || !n.throughputPerf.throughputObservationStarted {
		if seq%CHECKPOINT_INTERVAL == 0 && !n.throughputPerf.throughputObservationStarted {
			n.log.Info("Throughput observation not started yet, but seq %d is a checkpoint boundary, starting timing and n.throughputstartinterval is %d", seq, n.throughputPerf.throughputIntervalStartSeq)
		}
		return false
	}

	executedSlots := seq - n.throughputPerf.throughputIntervalStartSeq
	elapsedSeconds := now.Sub(n.throughputPerf.throughputIntervalStart).Seconds()
	throughput := 0.0
	if elapsedSeconds > 0 {
		throughput = float64(executedSlots) / elapsedSeconds
		if elapsedSeconds > 0 {
			// n.emitThroughputMeasurement(throughputMeasurement{
			// 	MeasurementTime: now,
			// 	View:            view,
			// 	LeaderID:        leaderId,
			// 	Seq:             seq,
			// 	ExecutedSlots:   executedSlots,
			// 	ElapsedSeconds:  elapsedSeconds,
			// 	Throughput:      throughput,
			// })
		}
		if throughput < 50 {
			// n.log.Warn(" Grace Period as throughput less than 50 for view %d and seq %d is %.2f with elapsed time %.2f seconds, executed slots %d", view, seq, throughput, elapsedSeconds, executedSlots)
			// return false
		}
	} else { // grace period
		n.log.Warn("In grace period as elapsed time is zero for view %d and seq %d, executed slots %d", view, seq, executedSlots)
		return false

	}

	belowTarget := false
	// at 250/s tput roughly 10 cp till go beyond threshold so 10s period
	if elapsedSeconds > 1 && seq%CHECKPOINT_INTERVAL == 0 {
		belowTarget = throughput <= n.throughputPerf.targetThroughput
		if belowTarget {
			n.log.Info("OLD PERF REQ: Elapsed secs greater than 1 and Throughput %.2f is below target %.2f for view %d and seq %d, elapsed time %.2f seconds, executed slots %d", throughput, n.throughputPerf.targetThroughput, view, seq, elapsedSeconds, executedSlots)
		} else {
			// n.log.Info("Elapsed secs greater than 1 and Throughput %.2f is above target %.2f for view %d and seq %d, elapsed time %.2f seconds, executed slots %d", throughput, n.throughputPerf.targetThroughput, view, seq, elapsedSeconds, executedSlots)
			_ = n.throughputPerf.targetThroughput
			n.throughputPerf.targetThroughput *= 1.01
			// n.log.Info("Increasing target throughput from %.2f to %.2f for view %d as observed throughput %.2f is above target", oldtput, n.throughputPerf.targetThroughput, view, throughput)
		}

	} else if elapsedSeconds <= 1 {
		// n.log.Info("Elapsed secs less than 1 doing nothing, the measured throughput is %.2f for view %d and seq %d, elapsed time %.2f seconds, executed slots %d", throughput, view, seq, elapsedSeconds, executedSlots)
	}
	n.throughputPerf.viewThroughputs[view] = throughput
	if view.Counter > n.throughputPerf.maxCounterByGeneration[view.Generation] {
		n.throughputPerf.maxCounterByGeneration[view.Generation] = view.Counter
	}
	return belowTarget
}
