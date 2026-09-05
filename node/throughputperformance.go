package node

import "time"

type ThroughputPerf struct {
	throughputIntervalStart      time.Time
	throughputIntervalStartSeq   int64
	targetThroughput             float64
	throughputObservationStarted bool
	viewThroughputs              map[int64]float64
}

// this will tput for seq number so full batch

func (n *Node) observeExecutedSlotForThroughput(seq int64, now time.Time, view int64, leaderId int) bool {
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
			n.emitThroughputMeasurement(throughputMeasurement{
				MeasurementTime: now,
				View:            view,
				LeaderID:        leaderId,
				Seq:             seq,
				ExecutedSlots:   executedSlots,
				ElapsedSeconds:  elapsedSeconds,
				Throughput:      throughput,
			})
		}
		if throughput < 100 {
			n.log.Warn(" Grace Period as throughput less than 100 for view %d and seq %d is %.2f with elapsed time %.2f seconds, executed slots %d", view, seq, throughput, elapsedSeconds, executedSlots)
			// return false
		}
	} else { // grace period
		n.log.Warn("In grace period as elapsed time is zero for view %d and seq %d, executed slots %d", view, seq, executedSlots)
		return false

	}

	belowTarget := false
	if elapsedSeconds > 2 && seq%CHECKPOINT_INTERVAL == 0 {
		belowTarget = throughput <= n.throughputPerf.targetThroughput
		if belowTarget {
			n.log.Info("Elapsed secs greater than 2 and Throughput %.2f is below target %.2f for view %d and seq %d, elapsed time %.2f seconds, executed slots %d", throughput, n.throughputPerf.targetThroughput, view, seq, elapsedSeconds, executedSlots)
		} else {
			n.log.Info("Elapsed secs greater than 2 and Throughput %.2f is above target %.2f for view %d and seq %d, elapsed time %.2f seconds, executed slots %d", throughput, n.throughputPerf.targetThroughput, view, seq, elapsedSeconds, executedSlots)
			oldtput := n.throughputPerf.targetThroughput
			n.throughputPerf.targetThroughput *= 1.01
			n.log.Info("Increasing target throughput from %.2f to %.2f for view %d as observed throughput %.2f is above target", oldtput, n.throughputPerf.targetThroughput, view, throughput)
		}

	} else if elapsedSeconds <= 2 {
		n.log.Info("Elapsed secs less than 2 doing nothing, the measured throughput is %.2f for view %d and seq %d, elapsed time %.2f seconds, executed slots %d", throughput, view, seq, elapsedSeconds, executedSlots)
	}
	n.throughputPerf.viewThroughputs[view] = throughput
	return belowTarget
}
