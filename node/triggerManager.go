package node

import (
	"time"

	"github.com/michael112233/pbft/logger"
)

type TriggerMode int

const (
	PeriodicTrigger TriggerMode = iota
	PerfTrigger
	FixedTrigger
	NullTrigger
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
	triggerMode          TriggerMode
	log                  *logger.Logger
	node                 NodeTrigger
}

func NewTriggerManager(log *logger.Logger, triggerMode TriggerMode, node NodeTrigger) *TriggerManager {
	return &TriggerManager{
		progressTimeoutValue: FixedTriggerTimeout,
		newViewTimeoutValue:  FixedTriggerTimeout,
		triggerMode:          triggerMode,
		log:                  log,
		node:                 node,
	}
}

func (tm *TriggerManager) SwitchTriggerMode(newMode TriggerMode) {
	if tm.triggerMode == PerfTrigger && newMode != PerfTrigger {
		tm.node.stopPerfTimer()
	}
	tm.triggerMode = newMode
	if newMode == FixedTrigger {
		tm.progressTimeoutValue = FixedTriggerTimeout
		tm.newViewTimeoutValue = FixedTriggerTimeout
	} else if newMode == PeriodicTrigger {
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

func (tm *TriggerManager) GetTriggerMode() TriggerMode {
	return tm.triggerMode
}

func (n *Node) ResetOnExecution(seq int64) {
	if n.IsLeader() {
		return
	}
	if n.triggerManager.GetTriggerMode() == FixedTrigger {
		n.resetLeaderProgressTimer()
	} else if n.triggerManager.GetTriggerMode() == PeriodicTrigger && seq == 1 {
		n.resetLeaderProgressTimer()
	}

}

func (n *Node) IsPerformanceTrigger() bool {
	return n.triggerManager.GetTriggerMode() == PerfTrigger
}

func (n *Node) GetProgressTimeout() time.Duration {
	return n.triggerManager.GetProgressTimeout()
}

func (n *Node) GetNewViewTimeout() time.Duration {
	return n.triggerManager.GetNewViewTimeout()
}
