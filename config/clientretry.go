package config

import (
	"fmt"
	"time"
)

// Client retry modes.
//
//	fixed:   every retry waits client_retry_interval_ms.
//	backoff: the first retry waits client_retry_interval_ms, each later one doubles
//	         the wait up to client_retry_max_ms, with equal jitter ([d/2, d)).
const (
	ClientRetryFixed   = "fixed"
	ClientRetryBackoff = "backoff"

	defaultClientRetryInterval = 50 * time.Millisecond
	defaultClientRetryMax      = 2 * time.Second
)

// ClientRetryModeOrDefault returns the configured mode, backoff when unset.
func (c *Config) ClientRetryModeOrDefault() string {
	if c.ClientRetryMode == "" {
		return ClientRetryBackoff
	}
	return c.ClientRetryMode
}

func (c *Config) ClientRetryInterval() time.Duration {
	if c.ClientRetryIntervalMs > 0 {
		return time.Duration(c.ClientRetryIntervalMs) * time.Millisecond
	}
	return defaultClientRetryInterval
}

func (c *Config) ClientRetryMax() time.Duration {
	if c.ClientRetryMaxMs > 0 {
		return time.Duration(c.ClientRetryMaxMs) * time.Millisecond
	}
	return defaultClientRetryMax
}

func (c *Config) ValidateClientRetry() error {
	switch mode := c.ClientRetryModeOrDefault(); mode {
	case ClientRetryFixed:
	case ClientRetryBackoff:
		if c.ClientRetryMax() < c.ClientRetryInterval() {
			return fmt.Errorf("client_retry_max_ms (%v) is below client_retry_interval_ms (%v)", c.ClientRetryMax(), c.ClientRetryInterval())
		}
	default:
		return fmt.Errorf("unknown client_retry_mode %q, want %q or %q", mode, ClientRetryFixed, ClientRetryBackoff)
	}
	return nil
}
