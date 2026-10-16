package node

import (
	"time"

	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/logger"
)

type NodeTrigger interface {
	stopPerfTimer()
}

type TriggerManager struct {
	progressTimeoutValue time.Duration
	newViewTimeoutValue  time.Duration
	triggerMode          core.TriggerMode
	log                  *logger.Logger
	node                 NodeTrigger

	// From the config's timer block (config/timer.go), fixed for the run.
	// Periodic uses periodicTimeout for both timers; Fixed and Perf use their own
	// progress and new-view timeouts (Perf keeps the progress one as its floor).
	periodicTimeout      time.Duration
	fixedProgressTimeout time.Duration
	fixedNewViewTimeout  time.Duration
}

// setTimeoutsForMode gives Periodic its long timeout for both timers, and Fixed
// and Perf the configured progress and new-view timeouts.
func (tm *TriggerManager) setTimeoutsForMode(mode core.TriggerMode) {
	if mode == core.PeriodicTrigger {
		tm.progressTimeoutValue = tm.periodicTimeout
		tm.newViewTimeoutValue = tm.periodicTimeout
		return
	}
	tm.progressTimeoutValue = tm.fixedProgressTimeout
	tm.newViewTimeoutValue = tm.fixedNewViewTimeout
}

func NewTriggerManager(log *logger.Logger, triggerMode core.TriggerMode, node NodeTrigger, periodicTimeout, fixedProgressTimeout, fixedNewViewTimeout time.Duration) *TriggerManager {
	tm := &TriggerManager{
		triggerMode:          triggerMode,
		log:                  log,
		node:                 node,
		periodicTimeout:      periodicTimeout,
		fixedProgressTimeout: fixedProgressTimeout,
		fixedNewViewTimeout:  fixedNewViewTimeout,
	}
	tm.setTimeoutsForMode(triggerMode)
	return tm
}

// SwitchTriggerMode sets the mode for the generation being entered, and with it
// the timeouts.
func (tm *TriggerManager) SwitchTriggerMode(newMode core.TriggerMode) {
	// no need to stop perf already going to call view change when jump gen by amplification or model
	// if tm.triggerMode == core.PerfTrigger && newMode != core.PerfTrigger {
	// 	tm.node.stopPerfTimer()
	// }
	tm.triggerMode = newMode
	if newMode != core.NullTrigger {
		tm.setTimeoutsForMode(newMode)
	}
}

func (tm *TriggerManager) GetProgressTimeout() time.Duration {
	return tm.progressTimeoutValue
}

func (tm *TriggerManager) GetNewViewTimeout() time.Duration {
	return tm.newViewTimeoutValue
}

func (tm *TriggerManager) GetTriggerMode() core.TriggerMode {
	return tm.triggerMode
}

func (n *Node) ResetOnExecution(seq int64) {
	if n.IsLeader() {
		return
	}
	if n.triggerManager.GetTriggerMode() == core.FixedTrigger || n.triggerManager.GetTriggerMode() == core.PerfTrigger {
		n.resetLeaderProgressTimer()
	} else if n.triggerManager.GetTriggerMode() == core.PeriodicTrigger && seq == 1 {
		n.resetLeaderProgressTimer()
	}

}

func (n *Node) IsPerformanceTrigger() bool {
	return n.triggerManager.GetTriggerMode() == core.PerfTrigger
}

func (n *Node) GetProgressTimeout() time.Duration {
	return n.triggerManager.GetProgressTimeout()
}

func (n *Node) GetNewViewTimeout() time.Duration {
	return n.triggerManager.GetNewViewTimeout()
}

// SwitchTriggerMode switches to newMode for generation gen.
func (n *Node) SwitchTriggerMode(newMode core.TriggerMode, gen uint64) {
	n.triggerManager.SwitchTriggerMode(newMode)
	n.log.Info("TIMER: gen %d scenario %s mode %d progress timeout %v new-view timeout %v",
		gen, n.scenarioNameForGeneration(gen), newMode, n.triggerManager.GetProgressTimeout(), n.triggerManager.GetNewViewTimeout())
}

func (n *Node) scenarioNameForGeneration(gen uint64) string {
	if !n.cfg.ScenarioMode {
		return "none"
	}
	return core.ScenarioToString(scenarioForGeneration(gen, n.cfg.ScenariosEnum, n.cfg.ScenarioGenerations))
}
