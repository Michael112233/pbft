package node

import "fmt"

func (n *Node) perfVC() {
	if n.performanceTrigger {
		n.log.Info("Starting perf view change my current for view %d and my n.view %d and the next for view will be %d", n.forView, n.view, n.forView+1)
		if n.viewChangeRunning {
			n.log.Warn(" vc already running when perf vc called")
		}
		n.enterViewChange()
	}
}

func (n *Node) maxRecentViewThroughput(currentView int64) float64 {
	window := int64(3*n.fNodes + 1)
	startView := currentView - window
	if startView < 1 {
		startView = 1
	}

	maxThroughput := 0.0
	found := false
	concatStr := ""
	for view := startView; view < currentView; view++ {
		if throughput, exists := n.throughputPerf.viewThroughputs[view]; exists {
			if !found || throughput > maxThroughput {
				maxThroughput = throughput
				found = true
			}
			concatStr += fmt.Sprintf("view %d: final throughput %.2f, ", view, throughput)
		} else {
			concatStr += fmt.Sprintf("view %d: no throughput data, ", view)
		}
	}

	if !found {
		n.log.Debug(" No throughput data found for views %d to %d, returning default target throughput %.2f", startView, currentView-1, defaultTargetThroughput)
		return defaultTargetThroughput
	}
	n.log.Info("Recent view throughputs for views %d to %d: %s", startView, currentView-1, concatStr)

	return maxThroughput
}

func (n *Node) newviewUpdatePerf(maxSeq int64, view int64) float64 {
	maxRecentThroughput := 0.0
	if n.cfg.Performance {

		n.throughputPerf.throughputIntervalStartSeq = maxSeq + THROUGHPUTINTERVAL_DELAY
		n.log.Info("Throughput interval start seq set to %d for new view %d", n.throughputPerf.throughputIntervalStartSeq, view)
		n.throughputPerf.throughputObservationStarted = false
		maxRecentThroughput = n.maxRecentViewThroughput(view)
		n.throughputPerf.targetThroughput = targetThroughputMaxFactor * maxRecentThroughput

		n.log.Info("Max recent throughput for new view %d is %.2f; target throughput set to %.2f", n.view, maxRecentThroughput, n.throughputPerf.targetThroughput)

	}
	return maxRecentThroughput

}

func (n *Node) handleNewViewUpdatePerf(maxSeq int64, view int64, throughput float64) {
	if n.cfg.Performance {
		n.throughputPerf.throughputIntervalStartSeq = maxSeq + THROUGHPUTINTERVAL_DELAY
		n.log.Info("Throughput interval start seq set to %d for new view %d", n.throughputPerf.throughputIntervalStartSeq, view)
		n.throughputPerf.throughputObservationStarted = false
		maxRecentThroughput := throughput
		n.throughputPerf.targetThroughput = targetThroughputMaxFactor * maxRecentThroughput

		n.log.Info("Max recent throughput for new view %d is %.2f; target throughput set to %.2f", n.view, maxRecentThroughput, n.throughputPerf.targetThroughput)

	}
}
