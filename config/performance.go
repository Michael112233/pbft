package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

// Per-view throughput measurement and the perf trigger's bar
// (node/viewthroughput.go, node/perfTrigger.go, node/perftimer.go).
//
// The perf window opens at the first executed slot at or past the NewView's
// maxSeq + WindowDelaySlots. A view's throughput is measured Aardvark-style over back-to-back intervals of
// IntervalSlots executed slots. The first interval starts GraceMs after the perf
// window opens, so the burst right after a view change is never measured, and
// only complete intervals count. The view's record is the max or the pooled mean
// of its complete intervals (ViewStrategy). The bar is BarFactor times the max
// record over the last 3f+1 views; with no record in that window the max is
// DefaultMaxThroughput.

const (
	PerfViewStrategyMax  = "max"
	PerfViewStrategyMean = "mean"

	defaultPerfIntervalSlots = 250
	defaultPerfWindowDelay   = 3
	defaultPerfGrace         = 1 * time.Second
	// Healthy unthrottled rate on the reference host (8000 req/s at batch size
	// 50), so the default bar is the bar a healthy run would set.
	defaultPerfMaxThroughput = 160.0
	defaultPerfBarFactor     = 0.91
	defaultPerfViewStrategy  = PerfViewStrategyMax
)

type PerformanceConfig struct {
	// Enabled turns on throughput measurement and the bar bookkeeping. The perf
	// trigger itself only acts in an action whose trigger mode is Perf.
	Enabled bool `json:"enabled"`
	// ViewStrategy is how a view's complete intervals become its record:
	// PerfViewStrategyMax (default) or PerfViewStrategyMean (total slots over
	// total time of the complete intervals).
	ViewStrategy string `json:"view_strategy"`
	// WindowDelaySlots is how far past the NewView's maxSeq the perf window opens,
	// in slots; 0 means 3, negative means it opens at maxSeq. It applies to both
	// the perf timer and the view record, which anchors GraceMs after the open.
	WindowDelaySlots int64 `json:"window_delay_slots"`
	// IntervalSlots is the measurement interval in executed slots; 0 means 250.
	IntervalSlots int64 `json:"interval_slots"`
	// GraceMs is the wait after the perf window opens before the first interval
	// starts; 0 means 1000, negative means no grace.
	GraceMs int `json:"grace_ms"`
	// BarFactor is the bar as a fraction of the max recent throughput; 0 means 0.91.
	BarFactor float64 `json:"bar_factor"`
	// DefaultMaxThroughput (slots/s) stands in for the max recent throughput when
	// none of the last 3f+1 views has a record; 0 means 160.
	DefaultMaxThroughput float64 `json:"default_max_throughput"`
}

// UnmarshalJSON accepts the legacy boolean form ("performance": true) as well as
// the object form, so older configs keep working with every option at its default.
func (p *PerformanceConfig) UnmarshalJSON(data []byte) error {
	switch string(bytes.TrimSpace(data)) {
	case "true":
		*p = PerformanceConfig{Enabled: true}
		return nil
	case "false", "null":
		*p = PerformanceConfig{}
		return nil
	}
	type plain PerformanceConfig // no UnmarshalJSON, so no recursion
	var v plain
	if err := json.Unmarshal(data, &v); err != nil {
		return fmt.Errorf("performance: want true/false or an object: %w", err)
	}
	*p = PerformanceConfig(v)
	return nil
}

func (c *Config) PerfIntervalSlots() int64 {
	if c.Performance.IntervalSlots > 0 {
		return c.Performance.IntervalSlots
	}
	return defaultPerfIntervalSlots
}

func (c *Config) PerfWindowDelaySlots() int64 {
	switch {
	case c.Performance.WindowDelaySlots > 0:
		return c.Performance.WindowDelaySlots
	case c.Performance.WindowDelaySlots < 0:
		return 0
	}
	return defaultPerfWindowDelay
}

func (c *Config) PerfGrace() time.Duration {
	switch {
	case c.Performance.GraceMs > 0:
		return time.Duration(c.Performance.GraceMs) * time.Millisecond
	case c.Performance.GraceMs < 0:
		return 0
	}
	return defaultPerfGrace
}

func (c *Config) PerfBarFactor() float64 {
	if c.Performance.BarFactor > 0 {
		return c.Performance.BarFactor
	}
	return defaultPerfBarFactor
}

func (c *Config) PerfDefaultMaxThroughput() float64 {
	if c.Performance.DefaultMaxThroughput > 0 {
		return c.Performance.DefaultMaxThroughput
	}
	return defaultPerfMaxThroughput
}

// PerfDefaultBar is the bar a view gets when no recent view has a record.
func (c *Config) PerfDefaultBar() float64 {
	return c.PerfBarFactor() * c.PerfDefaultMaxThroughput()
}

func (c *Config) PerfViewStrategy() string {
	if c.Performance.ViewStrategy != "" {
		return c.Performance.ViewStrategy
	}
	return defaultPerfViewStrategy
}

func (c *Config) ValidatePerformance() error {
	switch c.PerfViewStrategy() {
	case PerfViewStrategyMax, PerfViewStrategyMean:
	default:
		return fmt.Errorf("performance.view_strategy must be %q or %q, got %q",
			PerfViewStrategyMax, PerfViewStrategyMean, c.Performance.ViewStrategy)
	}
	if c.Performance.IntervalSlots < 0 {
		return fmt.Errorf("performance.interval_slots must be >= 0, got %d", c.Performance.IntervalSlots)
	}
	if f := c.Performance.BarFactor; f < 0 || f > 1 {
		return fmt.Errorf("performance.bar_factor must be in (0, 1], got %g", f)
	}
	if c.Performance.DefaultMaxThroughput < 0 {
		return fmt.Errorf("performance.default_max_throughput must be >= 0, got %g", c.Performance.DefaultMaxThroughput)
	}
	return nil
}
