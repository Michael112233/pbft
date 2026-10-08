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

	// From the config's timer block (config/timer.go); fixed for the run.
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

func (tm *TriggerManager) SwitchTriggerMode(newMode core.TriggerMode) {
	// no need to stop perf already going to call view change when jump gen by amplification or model
	// if tm.triggerMode == core.PerfTrigger && newMode != core.PerfTrigger {
	// 	tm.node.stopPerfTimer()
	// }
	tm.triggerMode = newMode
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

func (n *Node) SwitchTriggerMode(newMode core.TriggerMode) {
	n.triggerManager.SwitchTriggerMode(newMode)
}
