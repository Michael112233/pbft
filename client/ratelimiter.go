package client

import (
	"sync"
	"time"
)

// requestPacer owns the single send timeline shared by normal and retry
// transactions. Keeping the send callback under the mutex prevents concurrent
// producers from reserving stale slots and bursting after a slow send.
type requestPacer struct {
	mu         sync.Mutex
	nextSendAt time.Time
	now        func() time.Time
	sleep      func(time.Duration)
}

func requestSendSpacing(txCount, batchSize int, interval time.Duration) time.Duration {
	if txCount <= 0 || batchSize <= 0 || interval <= 0 {
		return 0
	}
	//uually this is interval * 1 as both txcount and batchSize same but if txcount less  than batchSize then we do less spacing
	spacing := interval * time.Duration(txCount) / time.Duration(batchSize)
	if spacing < time.Nanosecond {
		return time.Nanosecond
	}
	return spacing
}

// we use callback fn for easy testing so no need to involve full client
func (p *requestPacer) pace(txCount, batchSize int, interval time.Duration, send func()) {

	spacing := requestSendSpacing(txCount, batchSize, interval)
	if spacing == 0 || send == nil { // just safety check ignore
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	// serialise send under pacer lock for retry and normal path
	now := time.Now
	if p.now != nil {
		now = p.now
	}
	// now and sleep only relevant for testing could have directly used time.sleep and time.now
	sleep := time.Sleep
	if p.sleep != nil {
		sleep = p.sleep
	}

	currentTime := now()
	// if send speed slow then current time greater than next send so no sleep
	// but if current time less than next send then sleep or remaining time
	if p.nextSendAt.After(currentTime) {
		sleep(p.nextSendAt.Sub(currentTime))
	}

	sendStarted := now()
	send()
	// Base the next slot on this attempt rather than the old schedule. Slow or
	// failed sends therefore lower the observed rate instead of causing catch-up
	// sends when the callback returns.
	p.nextSendAt = sendStarted.Add(spacing)
}
