package node

// SHOULD CHECK HOW MUCH LAG TO ACTIVATE PERF TIMER

import (
	"time"

	"github.com/michael112233/pbft/core"
)

const (
	// perfTimerInterval is how often the timed performance trigger samples the
	// average throughput of the current measurement window.
	perfTimerInterval = 1 * time.Second
	// perfTimedTargetGrowth is how much the bar is raised on every tick whose
	// observed throughput beats the target.
	perfTimedTargetGrowth = 1.01
)

func (n *Node) perfTimedVC() {
	if n.viewChangeRunning {
		n.log.Warn(" vc already running when timed perf vc called")
	}
	forView := n.incrementCounter()
	n.enterViewChange(forView)
}

// resetTimedPerfWindow closes the current timed measurement window and arms a new
// one starting PerfWindowDelaySlots() slots past maxSeq, sets the new view's bar,
// and starts measuring the new view's throughput record. The perf timer itself is
// only restarted once that seq is actually executed, in
// observeExecutedSlotForTimedThroughput.
func (n *Node) resetTimedPerfWindow(maxSeq int64, view core.ViewID, maxRecentThroughput float64) {
	n.stopPerfTimer()
	n.throughputPerf.timedIntervalStartSeq = maxSeq + n.cfg.PerfWindowDelaySlots()
	n.throughputPerf.timedObservationStarted = false
	n.throughputPerf.timedTargetThroughput = n.cfg.PerfBarFactor() * maxRecentThroughput
	n.resetViewRecord(view)
	n.log.Info("Timed trigger: interval start seq set to %d for new view (%d,%d); timed target throughput set to %.2f from max recent throughput %.2f", n.throughputPerf.timedIntervalStartSeq, view.Generation, view.Counter, n.throughputPerf.timedTargetThroughput, maxRecentThroughput)
}

// resetPerfTimer starts the perf timer or moves its deadline forward by one
// interval. Like the view timers it is only ever touched from the node event
// loop, so no locking is needed.
func (n *Node) resetPerfTimer() {
	if !n.IsPerformanceTrigger() {
		return
	}
	n.perfTimerCh = resetOneShotTimer(&n.perfTimer, perfTimerInterval)
}

func (n *Node) stopPerfTimer() {
	stopOneShotTimer(n.perfTimer)
	n.perfTimerCh = nil
}

// handlePerfTimerTimeout fires once per perfTimerInterval while a measurement
// window is open. It compares the average throughput since the window opened
// against the timed target: below target triggers a performance view change,
// above target raises the bar and re-arms the timer.
func (n *Node) handlePerfTimerTimeout() {
	// if timing out before switch that mean switch hasnt happened so legal to call perf vc
	if n.viewChangeRunning || !n.throughputPerf.timedObservationStarted {
		// this can never run because once vc running perf timer already stopped
		// if observation false then perf timer never rest so cant time out
		n.log.Debug("Perf timer fired while no measurement window is open (vcRunning=%t started=%t), re-arming", n.viewChangeRunning, n.throughputPerf.timedObservationStarted)
		n.resetPerfTimer()
		return
	}

	now := time.Now()
	elapsedSeconds := now.Sub(n.throughputPerf.timedIntervalStart).Seconds()
	executedSlots := n.lastExecuted - n.throughputPerf.timedIntervalStartSeq
	view := n.GetViewID()

	throughput := float64(executedSlots) / elapsedSeconds
	if throughput <= n.throughputPerf.timedTargetThroughput {
		n.log.Info("Perf timer: throughput %.2f is below timed target %.2f for view (%d,%d), elapsed time %.2f seconds, executed slots %d; triggering view change", throughput, n.throughputPerf.timedTargetThroughput, view.Generation, view.Counter, elapsedSeconds, executedSlots)
		n.stopPerfTimer()
		n.perfTimedVC()
		return
	}

	oldTarget := n.throughputPerf.timedTargetThroughput
	n.throughputPerf.timedTargetThroughput *= perfTimedTargetGrowth
	n.log.Info("Perf timer: throughput %.2f is above timed target %.2f for view (%d,%d), elapsed time %.2f seconds, executed slots %d; raising target to %.2f", throughput, oldTarget, view.Generation, view.Counter, elapsedSeconds, executedSlots, n.throughputPerf.timedTargetThroughput)
	n.resetPerfTimer()
}
