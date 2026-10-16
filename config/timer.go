package config

import (
	"fmt"
	"time"
)

// Trigger timeouts (node/triggerManager.go). Periodic uses one timeout for both
// the leader progress timer and the new-view timer. Fixed and Perf have one each
// (Perf keeps the progress one as its floor), set per deployment from the view
// change cost and the client handover, not per scenario. Also the epoch timer
// (node/epochtimer.go).

const (
	defaultPeriodicTriggerTimeout = 10 * time.Second
	defaultFixedTriggerTimeout    = 150 * time.Millisecond
	defaultEpochTimer             = 45 * time.Second
)

type TimerConfig struct {
	// PeriodicTriggerTimeoutMs is the Periodic leader tenure; 0 means 10000.
	PeriodicTriggerTimeoutMs int `json:"periodic_trigger_timeout_ms"`
	// FixedTriggerTimeoutMs is the Fixed/Perf leader progress timeout (and the
	// Perf floor); 0 means 150.
	FixedTriggerTimeoutMs int `json:"fixed_trigger_timeout_ms"`
	// FixedNewViewTimeoutMs is the Fixed/Perf new-view timeout; 0 means the same
	// as the progress timeout.
	FixedNewViewTimeoutMs int `json:"fixed_new_view_timeout_ms"`
	// FixedTimersShared makes both Fixed/Perf timers use the larger of the two
	// values instead of their own.
	FixedTimersShared bool `json:"fixed_timers_shared"`
	// RelaxedFixedTriggerTimeoutMs was the per-scenario Fixed/Perf timeout. It is
	// parsed only so that a config which still sets it is rejected rather than
	// quietly running with different timers.
	RelaxedFixedTriggerTimeoutMs int `json:"relaxed_fixed_trigger_timeout_ms"`
	// EpochTimerMs is the epoch length: how long after seq 1 (and after each
	// generation switch) a node sends its epoch data; 0 means 45000.
	EpochTimerMs int `json:"epoch_timer_ms"`
	// EpochGrid pins epoch boundaries to a fixed grid: generation g's epoch timer
	// fires at anchor + g × epoch_timer_ms, where the anchor is the node's seq 1.
	// Off (the default), the timer is re-armed for a fresh epoch_timer_ms when the
	// decision is applied, so each decision's latency pushes every later epoch
	// back. See docs/epoch-grid.md.
	EpochGrid bool `json:"epoch_grid"`
}

func (c *Config) EpochTimer() time.Duration {
	return msOrDefault(c.Timer.EpochTimerMs, defaultEpochTimer)
}

const defaultOracleDecisionDelay = 100 * time.Millisecond

// OracleDecisionDelay is the oracle's stand-in for the learning agent's decision
// time.
func (c *Config) OracleDecisionDelay() time.Duration {
	return msOrDefault(c.OracleDecisionDelayMs, defaultOracleDecisionDelay)
}

func (c *Config) PeriodicTriggerTimeout() time.Duration {
	return msOrDefault(c.Timer.PeriodicTriggerTimeoutMs, defaultPeriodicTriggerTimeout)
}

// fixedTimeouts gives the configured Fixed/Perf progress and new-view timeouts,
// before fixed_timers_shared is applied.
func (c *Config) fixedTimeouts() (progress, newView time.Duration) {
	progress = msOrDefault(c.Timer.FixedTriggerTimeoutMs, defaultFixedTriggerTimeout)
	return progress, msOrDefault(c.Timer.FixedNewViewTimeoutMs, progress)
}

// FixedProgressTimeout is the Fixed/Perf leader progress timeout, also the Perf
// floor.
func (c *Config) FixedProgressTimeout() time.Duration {
	progress, newView := c.fixedTimeouts()
	if c.Timer.FixedTimersShared {
		return max(progress, newView)
	}
	return progress
}

// FixedNewViewTimeout is the Fixed/Perf new-view timeout.
func (c *Config) FixedNewViewTimeout() time.Duration {
	progress, newView := c.fixedTimeouts()
	if c.Timer.FixedTimersShared {
		return max(progress, newView)
	}
	return newView
}

const defaultProposalDelaySleep = 100 * time.Millisecond

// ProposalDelaySleep is how long a ProposalDelay leader sleeps before each
// proposal (node/node.go tryPropose).
func (c *Config) ProposalDelaySleep() time.Duration {
	return msOrDefault(c.ProposalDelaySleepMs, defaultProposalDelaySleep)
}

const defaultViewChangeFetchDelay = 20 * time.Millisecond

// ViewChangeFetchDelay is how long a replica waits for a ViewChange its pending
// NewView references before fetching it from the new primary. The common miss is
// a ViewChange still in transit or queued behind another one, so a short wait
// avoids the primary re-sending large messages for nothing.
func (c *Config) ViewChangeFetchDelay() time.Duration {
	return msOrDefault(c.ViewChangeFetchDelayMs, defaultViewChangeFetchDelay)
}

func msOrDefault(ms int, def time.Duration) time.Duration {
	if ms > 0 {
		return time.Duration(ms) * time.Millisecond
	}
	return def
}

func (c *Config) ValidateTimer() error {
	t := c.Timer
	if t.PeriodicTriggerTimeoutMs < 0 || t.FixedTriggerTimeoutMs < 0 || t.FixedNewViewTimeoutMs < 0 || t.EpochTimerMs < 0 {
		return fmt.Errorf("timer: timeouts must be >= 0 (0 means default), got %+v", t)
	}
	if t.RelaxedFixedTriggerTimeoutMs != 0 {
		return fmt.Errorf("timer: relaxed_fixed_trigger_timeout_ms was removed (Fixed/Perf timeouts no longer "+
			"depend on the scenario); set fixed_trigger_timeout_ms (progress) and fixed_new_view_timeout_ms instead, got %d",
			t.RelaxedFixedTriggerTimeoutMs)
	}
	if c.ProposalDelaySleepMs < 0 {
		return fmt.Errorf("proposal_delay_sleep_ms must be >= 0 (0 means 100), got %d", c.ProposalDelaySleepMs)
	}
	if c.OracleDecisionDelayMs < 0 {
		return fmt.Errorf("oracle_decision_delay_ms must be >= 0 (0 means 100), got %d", c.OracleDecisionDelayMs)
	}
	if c.ViewChangeFetchDelayMs < 0 {
		return fmt.Errorf("view_change_fetch_delay_ms must be >= 0 (0 means 20), got %d", c.ViewChangeFetchDelayMs)
	}
	return nil
}
