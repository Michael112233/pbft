package node

import (
	"fmt"

	"github.com/michael112233/pbft/core"
)

// maxCounterForGeneration returns the highest counter recorded for the given
// generation, or 0 if no view from that generation has recorded throughput.
// Backed by maxCounterByGeneration, which is kept up to date on every write
// to viewThroughputs, so this is O(1) rather than a scan over the map.
func (n *Node) maxCounterForGeneration(generation uint64) uint64 {
	return n.throughputPerf.maxCounterByGeneration[generation]
}

// for proposal delay in new view can make sure if less than 100 then send a default value
// fromDefault is true when none of the window's views recorded a throughput, so the
// returned value is the configured default (PerfDefaultMaxThroughput) rather than a
// measurement.
func (n *Node) maxRecentViewThroughput(currentView core.ViewID) (maxRecent float64, fromDefault bool) {
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
		def := n.cfg.PerfDefaultMaxThroughput()
		n.log.Warn("PERF BAR DEFAULT: no recorded throughput in the %d views before (%d,%d) (%s); using default max recent throughput %.2f", window, currentView.Generation, currentView.Counter, concatStr, def)
		return def, true
	}
	n.log.Info("Recent view throughputs for the %d views before (%d,%d): %s", window, currentView.Generation, currentView.Counter, concatStr)

	return maxThroughput, false
}

func perfBarSource(fromDefault bool) string {
	if fromDefault {
		return "default"
	}
	return "recorded"
}

func (n *Node) newviewUpdatePerf(maxSeq int64, view core.ViewID) float64 {
	maxRecentThroughput := 0.0
	if n.cfg.Performance.Enabled {
		var fromDefault bool
		maxRecentThroughput, fromDefault = n.maxRecentViewThroughput(view)
		if maxRecentThroughput <= 100 {
			n.log.Error("Concerning how is tput <= 100")
		}
		n.resetTimedPerfWindow(maxSeq, view, maxRecentThroughput)
		n.log.Info("Max recent throughput for new view (%d,%d) is %.2f; target throughput set to %.2f (source=%s)", view.Generation, view.Counter, maxRecentThroughput, n.throughputPerf.timedTargetThroughput, perfBarSource(fromDefault))
	}
	return maxRecentThroughput

}

func (n *Node) handleNewViewUpdatePerf(maxSeq int64, view core.ViewID, throughput float64) {
	if n.cfg.Performance.Enabled {
		maxRecentThroughput := throughput
		// The leader computes the max and only the number travels in the NewView, so a
		// replica recognises the fallback by value. A measured rate landing exactly on
		// the default constant is not a realistic collision.
		fromDefault := maxRecentThroughput == n.cfg.PerfDefaultMaxThroughput()
		if fromDefault {
			n.log.Warn("PERF BAR DEFAULT: NewView for (%d,%d) carried the default max recent throughput %.2f (leader had no recorded throughput in its window)", view.Generation, view.Counter, maxRecentThroughput)
		}

		n.resetTimedPerfWindow(maxSeq, view, maxRecentThroughput)
		n.log.Info("Max recent throughput for new view (%d,%d) is %.2f; target throughput set to %.2f (source=%s)", view.Generation, view.Counter, maxRecentThroughput, n.throughputPerf.timedTargetThroughput, perfBarSource(fromDefault))
	}
}
