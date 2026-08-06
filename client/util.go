package client

import (
	"math/rand/v2"
	"time"
)

const (
	initialRetryDelay = 1 * time.Second
	maximumRetryDelay = 10 * time.Second
)

func retryDelay(retryCount int) time.Duration {
	delay := initialRetryDelay

	// retryCount 1 => 10s
	// retryCount 2 => 20s
	// retryCount 3+ => 30s
	for i := 0; i < retryCount && delay < maximumRetryDelay; i++ {
		if delay >= maximumRetryDelay/2 {
			delay = maximumRetryDelay
		} else {
			delay *= 2
		}
	}

	// Equal jitter produces a value in [delay/2, delay).
	half := delay / 2
	return half + rand.N(half)
}
