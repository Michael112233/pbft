package client

import (
	"math/rand/v2"
	"time"

	"github.com/michael112233/pbft/config"
)

// retryPolicy is the client's retry schedule (config/clientretry.go). first is the
// wait before a request's first retry; delay(n) the wait after its n-th retry.
type retryPolicy struct {
	fixed    bool
	interval time.Duration
	max      time.Duration
	// includeRetried counts commits of retried requests in the latency samples.
	includeRetried bool
}

// defaultRetryPolicy is the backoff schedule the complete_suite drain path used
// before retries were configurable; retried commits stay out of the latency samples.
func defaultRetryPolicy() retryPolicy {
	return retryPolicy{interval: 50 * time.Millisecond, max: 2 * time.Second}
}

func newRetryPolicy(cfg *config.Config) retryPolicy {
	if cfg == nil || !cfg.ClientRetry {
		return defaultRetryPolicy()
	}
	return retryPolicy{
		fixed:          cfg.ClientRetryModeOrDefault() == config.ClientRetryFixed,
		interval:       cfg.ClientRetryInterval(),
		max:            cfg.ClientRetryMax(),
		includeRetried: true,
	}
}

func (p retryPolicy) first() time.Duration {
	return p.interval
}

func (p retryPolicy) delay(retryCount int) time.Duration {
	if p.fixed {
		return p.interval
	}
	delay := p.interval
	for i := 0; i < retryCount && delay < p.max; i++ {
		if delay >= p.max/2 {
			delay = p.max
		} else {
			delay *= 2
		}
	}

	// Equal jitter produces a value in [delay/2, delay).
	half := delay / 2
	return half + rand.N(half)
}

// sweepInterval is how often the retry worker scans for due requests: a fifth of
// the retry interval, so a due retry goes out at most that late, capped at 50ms.
func (p retryPolicy) sweepInterval() time.Duration {
	tick := p.interval / 5
	if tick < time.Millisecond {
		return time.Millisecond
	}
	if tick > 50*time.Millisecond {
		return 50 * time.Millisecond
	}
	return tick
}
