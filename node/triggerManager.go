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

	// From the config's timer block (config/timer.go). periodicTimeout is fixed for
	// the run; fixedTimeout is the Fixed/Perf floor of the current generation, set
	// on every SwitchTriggerMode (config.FixedFloorForScenario).
	periodicTimeout time.Duration
	fixedTimeout    time.Duration
}

// timeoutForMode gives Periodic its long timeout; Fixed and Perf share the short
// one (Perf keeps it as the progress floor under its throughput threshold).
func (tm *TriggerManager) timeoutForMode(mode core.TriggerMode) time.Duration {
	if mode == core.PeriodicTrigger {
		return tm.periodicTimeout
	}
	return tm.fixedTimeout
}

func NewTriggerManager(log *logger.Logger, triggerMode core.TriggerMode, node NodeTrigger, periodicTimeout, fixedTimeout time.Duration) *TriggerManager {
	tm := &TriggerManager{
		triggerMode:     triggerMode,
		log:             log,
		node:            node,
		periodicTimeout: periodicTimeout,
		fixedTimeout:    fixedTimeout,
	}
	timeout := tm.timeoutForMode(triggerMode)
	tm.progressTimeoutValue = timeout
	tm.newViewTimeoutValue = timeout
	return tm
}

// SwitchTriggerMode sets the mode and the Fixed/Perf floor for the generation
// being entered; Periodic keeps its own timeout.
func (tm *TriggerManager) SwitchTriggerMode(newMode core.TriggerMode, fixedTimeout time.Duration) {
	// no need to stop perf already going to call view change when jump gen by amplification or model
	// if tm.triggerMode == core.PerfTrigger && newMode != core.PerfTrigger {
	// 	tm.node.stopPerfTimer()
	// }
	tm.triggerMode = newMode
	tm.fixedTimeout = fixedTimeout
	if newMode != core.NullTrigger {
		timeout := tm.timeoutForMode(newMode)
		tm.progressTimeoutValue = timeout
		tm.newViewTimeoutValue = timeout
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

// SwitchTriggerMode switches to newMode for generation gen, with the Fixed/Perf
// floor of gen's scenario.
func (n *Node) SwitchTriggerMode(newMode core.TriggerMode, gen uint64) {
	floor := n.fixedFloorForGeneration(gen)
	n.triggerManager.SwitchTriggerMode(newMode, floor)
	n.log.Info("TIMER: gen %d scenario %s mode %d fixed floor %v (progress/new-view timeout %v)",
		gen, n.scenarioNameForGeneration(gen), newMode, floor, n.triggerManager.GetProgressTimeout())
}

// fixedFloorForGeneration is the Fixed/Perf timeout for gen: the scenario is
// derived from the generation, as for the election pool, so every node agrees.
func (n *Node) fixedFloorForGeneration(gen uint64) time.Duration {
	if !n.cfg.ScenarioMode {
		return n.cfg.FixedTriggerTimeout()
	}
	return n.cfg.FixedFloorForScenario(scenarioForGeneration(gen, n.cfg.ScenariosEnum, n.cfg.ScenarioGenerations))
}

func (n *Node) scenarioNameForGeneration(gen uint64) string {
	if !n.cfg.ScenarioMode {
		return "none"
	}
	return core.ScenarioToString(scenarioForGeneration(gen, n.cfg.ScenariosEnum, n.cfg.ScenarioGenerations))
}
