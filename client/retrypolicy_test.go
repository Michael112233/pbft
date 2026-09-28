package client

import (
	"testing"
	"time"

	"github.com/michael112233/pbft/config"
)

func TestRetryPolicyFixedUsesInterval(t *testing.T) {
	p := newRetryPolicy(&config.Config{ClientRetry: true, ClientRetryMode: config.ClientRetryFixed, ClientRetryIntervalMs: 80})
	if !p.includeRetried {
		t.Fatal("retry on but retried commits excluded from latency")
	}
	if p.first() != 80*time.Millisecond {
		t.Fatalf("first=%v, want 80ms", p.first())
	}
	for n := 1; n <= 5; n++ {
		if d := p.delay(n); d != 80*time.Millisecond {
			t.Fatalf("delay(%d)=%v, want 80ms", n, d)
		}
	}
}

func TestRetryPolicyBackoffDoublesToCap(t *testing.T) {
	p := newRetryPolicy(&config.Config{ClientRetry: true, ClientRetryMode: config.ClientRetryBackoff, ClientRetryIntervalMs: 50, ClientRetryMaxMs: 400})
	// delay(n) is jittered into [d/2, d) with d = min(50ms * 2^n, 400ms)
	for n, d := range map[int]time.Duration{1: 100, 2: 200, 3: 400, 6: 400} {
		d *= time.Millisecond
		for i := 0; i < 50; i++ {
			if got := p.delay(n); got < d/2 || got >= d {
				t.Fatalf("delay(%d)=%v, want in [%v, %v)", n, got, d/2, d)
			}
		}
	}
}

func TestRetryPolicyOffKeepsLegacyLatency(t *testing.T) {
	p := newRetryPolicy(&config.Config{ClientRetry: false})
	if p.includeRetried {
		t.Fatal("retry off but retried commits included in latency")
	}
}

func TestRetryPolicySweepInterval(t *testing.T) {
	for interval, want := range map[int]time.Duration{50: 10 * time.Millisecond, 1000: 50 * time.Millisecond, 2: time.Millisecond} {
		p := newRetryPolicy(&config.Config{ClientRetry: true, ClientRetryMode: config.ClientRetryFixed, ClientRetryIntervalMs: interval})
		if got := p.sweepInterval(); got != want {
			t.Fatalf("interval %dms: sweep=%v, want %v", interval, got, want)
		}
	}
}

func TestValidateClientRetry(t *testing.T) {
	if err := (&config.Config{ClientRetryMode: "exponential"}).ValidateClientRetry(); err == nil {
		t.Fatal("unknown mode accepted")
	}
	if err := (&config.Config{ClientRetryMode: config.ClientRetryBackoff, ClientRetryIntervalMs: 500, ClientRetryMaxMs: 100}).ValidateClientRetry(); err == nil {
		t.Fatal("backoff cap below the first delay accepted")
	}
	if err := (&config.Config{}).ValidateClientRetry(); err != nil {
		t.Fatalf("empty retry config rejected: %v", err)
	}
}
