package config

import (
	"fmt"
	"time"
)

// Aware candidate-list rules (aware_candidates).
const (
	// AwareCandidatesPad: threshold set padded with the next-best nodes by
	// (score, id) up to 2f+1, ordered by (score, id). The pilot rule.
	AwareCandidatesPad = "pad"
	// AwareCandidatesThresholdOrAll: threshold set if it has 2f+1 nodes, else
	// every node; ordered by id.
	AwareCandidatesThresholdOrAll = "threshold_or_all"
)

const (
	defaultAwareProbeInterval   = 500 * time.Millisecond
	defaultAwareProbeTimeout    = 2 * time.Second
	defaultAwareRTTWindow       = 10 * time.Second
	defaultAwareGrace           = 500 * time.Millisecond
	defaultAwareStalenessEpochs = 3
	defaultAwareAlpha           = 0.1
	defaultAwareEpsilonMs       = 1.0
	defaultAwareCandidates      = AwareCandidatesThresholdOrAll
)

// how freq we probe peers
// send probe to all by all so n^2 probes per interval
// all probes off event loop 
// use unary rpc so off stream as well

func (c *Config) AwareProbeInterval() time.Duration {
	if c.AwareProbeIntervalMs > 0 {
		return time.Duration(c.AwareProbeIntervalMs) * time.Millisecond
	}
	return defaultAwareProbeInterval
}

func (c *Config) AwareProbeTimeout() time.Duration {
	if c.AwareProbeTimeoutMs > 0 {
		return time.Duration(c.AwareProbeTimeoutMs) * time.Millisecond
	}
	return defaultAwareProbeTimeout
}

func (c *Config) AwareRTTWindow() time.Duration {
	if c.AwareRTTWindowS > 0 {
		return time.Duration(c.AwareRTTWindowS) * time.Second
	}
	return defaultAwareRTTWindow
}

func (c *Config) AwareGrace() time.Duration {
	if c.AwareGraceMs > 0 {
		return time.Duration(c.AwareGraceMs) * time.Millisecond
	}
	return defaultAwareGrace
}

// AwareStaleness is E: a matrix row older than E epoch generations counts as missing.
func (c *Config) AwareStaleness() uint64 {
	if c.AwareStalenessEpochs > 0 {
		return c.AwareStalenessEpochs
	}
	return defaultAwareStalenessEpochs
}

// AwareAlphaFraction and AwareEpsilon set the candidate filter:
// score <= best + max(best*alpha, epsilon), scores in milliseconds.
func (c *Config) AwareAlphaFraction() float64 {
	if c.AwareAlpha > 0 {
		return c.AwareAlpha
	}
	return defaultAwareAlpha
}

func (c *Config) AwareEpsilon() float64 {
	if c.AwareEpsilonMs > 0 {
		return c.AwareEpsilonMs
	}
	return defaultAwareEpsilonMs
}

// AwareCandidateRule is how the candidate list is built from the scores:
// AwareCandidatesThresholdOrAll (default) or AwareCandidatesPad.
func (c *Config) AwareCandidateRule() string {
	if c.AwareCandidates != "" {
		return c.AwareCandidates
	}
	return defaultAwareCandidates
}

func (c *Config) ValidateAware() error {
	switch c.AwareCandidateRule() {
	case AwareCandidatesPad, AwareCandidatesThresholdOrAll:
		return nil
	default:
		return fmt.Errorf("aware_candidates must be %q or %q, got %q",
			AwareCandidatesPad, AwareCandidatesThresholdOrAll, c.AwareCandidates)
	}
}
