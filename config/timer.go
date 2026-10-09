package config

import (
	"fmt"
	"time"
)

// Trigger timeouts (node/triggerManager.go). Each one is used for both the
// leader progress timer and the new-view timer of its trigger mode; Perf shares
// the fixed one as its progress floor. Also the epoch timer (node/epochtimer.go).

const (
	defaultPeriodicTriggerTimeout     = 10 * time.Second
	defaultFixedTriggerTimeout        = 150 * time.Millisecond
	defaultRelaxedFixedTriggerTimeout = 300 * time.Millisecond
	defaultEpochTimer                 = 45 * time.Second
)

type TimerConfig struct {
	// PeriodicTriggerTimeoutMs is the Periodic leader tenure; 0 means 10000.
	PeriodicTriggerTimeoutMs int `json:"periodic_trigger_timeout_ms"`
	// FixedTriggerTimeoutMs is the Fixed (and Perf floor) timeout; 0 means 150.
	FixedTriggerTimeoutMs int `json:"fixed_trigger_timeout_ms"`
	// RelaxedFixedTriggerTimeoutMs is a longer fixed timeout; 0 means 300. Not
	// read by the protocol yet.
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

func (c *Config) FixedTriggerTimeout() time.Duration {
	return msOrDefault(c.Timer.FixedTriggerTimeoutMs, defaultFixedTriggerTimeout)
}

func (c *Config) RelaxedFixedTriggerTimeout() time.Duration {
	return msOrDefault(c.Timer.RelaxedFixedTriggerTimeoutMs, defaultRelaxedFixedTriggerTimeout)
}

func msOrDefault(ms int, def time.Duration) time.Duration {
	if ms > 0 {
		return time.Duration(ms) * time.Millisecond
	}
	return def
}

func (c *Config) ValidateTimer() error {
	t := c.Timer
	if t.PeriodicTriggerTimeoutMs < 0 || t.FixedTriggerTimeoutMs < 0 || t.RelaxedFixedTriggerTimeoutMs < 0 || t.EpochTimerMs < 0 {
		return fmt.Errorf("timer: timeouts must be >= 0 (0 means default), got %+v", t)
	}
	if c.OracleDecisionDelayMs < 0 {
		return fmt.Errorf("oracle_decision_delay_ms must be >= 0 (0 means 100), got %d", c.OracleDecisionDelayMs)
	}
	return nil
}
