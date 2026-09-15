package node

import (
	"time"

	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/logger"
)

const (
	PeriodicTriggerTimeout = 10 * time.Second
	FixedTriggerTimeout    = 100 * time.Millisecond
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
}

func NewTriggerManager(log *logger.Logger, triggerMode core.TriggerMode, node NodeTrigger) *TriggerManager {
	return &TriggerManager{
		progressTimeoutValue: FixedTriggerTimeout,
		newViewTimeoutValue:  FixedTriggerTimeout,
		triggerMode:          triggerMode,
		log:                  log,
		node:                 node,
	}
}


func (tm *TriggerManager) SwitchTriggerMode(newMode core.TriggerMode) {
	if tm.triggerMode == core.PerfTrigger && newMode != core.PerfTrigger {
		tm.node.stopPerfTimer()
	}
	tm.triggerMode = newMode
	if newMode == core.FixedTrigger {
		tm.progressTimeoutValue = FixedTriggerTimeout
		tm.newViewTimeoutValue = FixedTriggerTimeout
	} else if newMode == core.PeriodicTrigger {
		tm.progressTimeoutValue = PeriodicTriggerTimeout
		tm.newViewTimeoutValue = PeriodicTriggerTimeout
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
	if n.triggerManager.GetTriggerMode() == core.FixedTrigger {
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
