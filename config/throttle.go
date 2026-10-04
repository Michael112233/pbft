package config

import (
	"fmt"
	"time"
)

// Targeted proposal throttling (client/throttlemanager.go + node/proposalgate.go).
//
// Simulates a network throttle that can be aimed at a bounded number of replicas
// at a time and takes a while to take hold after being re-aimed.
//
// The controller holds k = f throttle slots. A slot assigned to a replica
// rate-limits that replica's proposal production whenever it leads; retargeting
// a slot costs ThrottlePrep() of wall-clock preparation before it bites, and a
// slot stays occupied until its disable is acknowledged. Nothing follows a
// leader automatically.
//
// The severity of the throttle is deliberately NOT part of the throttle config:
// it is ProposalMinInterval(), a node-side constant calibrated once and then
// used unchanged for every policy, so RoundRobin and Election are compared
// under an identical gate.

const (
	// ThrottleStrategyRoundRobin may compute successors from the public
	// round-robin schedule and prepare them before takeover.
	ThrottleStrategyRoundRobin = "roundrobin"
	// ThrottleStrategyElection is reactive only: it never predicts a leader and
	// acts solely on leader notices it has already received.
	ThrottleStrategyElection = "election"

	defaultThrottlePrep   = 2 * time.Second
	defaultThrottleTenure = 10 * time.Second
)

type ThrottleConfig struct {
	Enabled bool `json:"enabled"`
	// Strategy is ThrottleStrategyRoundRobin or ThrottleStrategyElection.
	Strategy string `json:"strategy"`
	// Slots overrides the number of throttle slots; 0 derives k = f.
	Slots int `json:"slots"`
	// PrepMs is how long a slot is occupied preparing a new target before its
	// gate is enabled.
	PrepMs int `json:"prep_ms"`
	// TenureMs is the leader tenure the controller assumes, used by the
	// round-robin strategy to time its early release. It mirrors
	// PeriodicTriggerTimeout and is controller knowledge, not protocol state.
	TenureMs int `json:"tenure_ms"`
}

func (c *Config) ThrottlePrep() time.Duration {
	if c.Throttle.PrepMs > 0 {
		return time.Duration(c.Throttle.PrepMs) * time.Millisecond
	}
	return defaultThrottlePrep
}

func (c *Config) ThrottleTenure() time.Duration {
	if c.Throttle.TenureMs > 0 {
		return time.Duration(c.Throttle.TenureMs) * time.Millisecond
	}
	return defaultThrottleTenure
}

// ThrottleSlots is k: the configured override, else f = (n-1)/3.
func (c *Config) ThrottleSlots() int {
	if c.Throttle.Slots > 0 {
		return c.Throttle.Slots
	}
	return int((c.NodeNum - 1) / 3)
}

// ProposalMinInterval is the minimum spacing between consecutive proposals a
// gated leader is allowed. Zero disables the gate entirely, whatever commands
// arrive.
func (c *Config) ProposalMinInterval() time.Duration {
	if c.ProposalMinIntervalMs > 0 {
		return time.Duration(c.ProposalMinIntervalMs) * time.Millisecond
	}
	return 0
}

// ValidateThrottle rejects throttle configs that could never take effect.
func (c *Config) ValidateThrottle() error {
	if c.ProposalMinIntervalMs < 0 {
		return fmt.Errorf("proposal_min_interval_ms must not be negative, got %d", c.ProposalMinIntervalMs)
	}
	if c.ProposalGateAtStart && c.ProposalMinIntervalMs <= 0 {
		return fmt.Errorf("proposal_gate_at_start needs proposal_min_interval_ms > 0")
	}
	if !c.Throttle.Enabled {
		return nil
	}
	switch c.Throttle.Strategy {
	case ThrottleStrategyRoundRobin, ThrottleStrategyElection:
	default:
		return fmt.Errorf("throttle.strategy must be %q or %q, got %q",
			ThrottleStrategyRoundRobin, ThrottleStrategyElection, c.Throttle.Strategy)
	}
	if c.ProposalMinIntervalMs <= 0 {
		return fmt.Errorf("throttle.enabled needs proposal_min_interval_ms > 0, otherwise the gate does nothing")
	}
	if k := c.ThrottleSlots(); k < 1 || int64(k) > c.NodeNum {
		return fmt.Errorf("throttle needs 1..%d slots, got %d", c.NodeNum, k)
	}
	if c.Throttle.Slots < 0 {
		return fmt.Errorf("throttle.slots must not be negative, got %d", c.Throttle.Slots)
	}
	if c.ProposalGateAtStart {
		return fmt.Errorf("throttle.enabled and proposal_gate_at_start both drive the gate; proposal_gate_at_start is for calibration runs with no controller")
	}
	return nil
}
