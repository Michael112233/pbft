package node

import (
	"fmt"

	"github.com/michael112233/pbft/core"
)

// func (n *Node) perfVC() {
// 	if n.performanceTrigger {
// 		n.log.Info("Starting perf view change my current for view %d and my n.view %d and the next for view will be %d", n.forView, n.view, n.forView+1)
// 		if n.viewChangeRunning {
// 			n.log.Warn(" vc already running when perf vc called")
// 		}
// 		n.enterViewChange()
// 	}
// }

// maxCounterForGeneration returns the highest counter recorded for the given
// generation, or 0 if no view from that generation has recorded throughput.
// Backed by maxCounterByGeneration, which is kept up to date on every write
// to viewThroughputs, so this is O(1) rather than a scan over the map.
func (n *Node) maxCounterForGeneration(generation uint64) uint64 {
	return n.throughputPerf.maxCounterByGeneration[generation]
}

// for proposal delay in new view can make sure if less than 100 then send a default value
func (n *Node) maxRecentViewThroughput(currentView core.ViewID) float64 {
	window := int64(3*n.fNodes + 1)

	maxThroughput := 0.0
	found := false
	concatStr := ""

	generation := currentView.Generation
	counter := currentView.Counter
	for i := int64(0); i < window; i++ {
		counter--
		for counter < 1 {
			if generation <= 1 {
				break
			}
			generation--
			// A generation with no recorded throughput is skipped for free:
			// its unknown view count is not charged against window, so this
			// window can span further back in time than 3f+1 actual views.
			counter = n.maxCounterForGeneration(generation)
		}
		if counter < 1 {
			break
		}

		view := core.ViewID{Generation: generation, Counter: counter}
		if throughput, exists := n.throughputPerf.viewThroughputs[view]; exists {
			// for same generation for few counters throughput maynot exists
			if !found || throughput > maxThroughput {
				maxThroughput = throughput
				found = true
			}
			concatStr += fmt.Sprintf("view (%d,%d): final throughput %.2f, ", view.Generation, view.Counter, throughput)
		} else {
			concatStr += fmt.Sprintf("view (%d,%d): no throughput data, ", view.Generation, view.Counter)
		}
	}

	if !found {
		n.log.Debug(" No throughput data found for the %d views before (%d,%d), returning default target throughput %.2f", window, currentView.Generation, currentView.Counter, defaultTargetThroughput)
		return defaultTargetThroughput
	}
	n.log.Info("Recent view throughputs for the %d views before (%d,%d): %s", window, currentView.Generation, currentView.Counter, concatStr)

	return maxThroughput
}

func (n *Node) newviewUpdatePerf(maxSeq int64, view core.ViewID) float64 {
	maxRecentThroughput := 0.0
	if n.cfg.Performance {

		n.throughputPerf.throughputIntervalStartSeq = maxSeq + THROUGHPUTINTERVAL_DELAY
		n.log.Info("Throughput interval start seq set to %d for new view (%d,%d)", n.throughputPerf.throughputIntervalStartSeq, view.Generation, view.Counter)
		n.throughputPerf.throughputObservationStarted = false
		maxRecentThroughput = n.maxRecentViewThroughput(view)
		if maxRecentThroughput <= 100 {
			n.log.Error("Concerning how is tput <= 100")
		}
		n.throughputPerf.targetThroughput = targetThroughputMaxFactor * maxRecentThroughput

		n.log.Info("Max recent throughput for new view (%d,%d) is %.2f; target throughput set to %.2f", view.Generation, view.Counter, maxRecentThroughput, n.throughputPerf.targetThroughput)

		n.resetTimedPerfWindow(maxSeq, view, maxRecentThroughput)
	}
	return maxRecentThroughput

}

func (n *Node) handleNewViewUpdatePerf(maxSeq int64, view core.ViewID, throughput float64) {
	if n.cfg.Performance {
		n.throughputPerf.throughputIntervalStartSeq = maxSeq + THROUGHPUTINTERVAL_DELAY
		n.log.Info("Throughput interval start seq set to %d for new view (%d,%d)", n.throughputPerf.throughputIntervalStartSeq, view.Generation, view.Counter)
		n.throughputPerf.throughputObservationStarted = false
		maxRecentThroughput := throughput
		n.throughputPerf.targetThroughput = targetThroughputMaxFactor * maxRecentThroughput

		n.log.Info("Max recent throughput for new view (%d,%d) is %.2f; target throughput set to %.2f", view.Generation, view.Counter, maxRecentThroughput, n.throughputPerf.targetThroughput)

		n.resetTimedPerfWindow(maxSeq, view, maxRecentThroughput)
	}
}
