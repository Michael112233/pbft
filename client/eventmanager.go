package client

import (
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/michael112233/pbft/logger"
)

const (
	defaultEventLowerBound = 20 * time.Second
	defaultEventUpperBound = 30 * time.Second
)

// ClientEventManager is what the EventManager needs from the client.
type ClientEventManager interface {
	currentLeaderAddr() string
	sendEventMsg(leaderAddr string)
}

// EventManager runs a background goroutine that sleeps for a uniformly
// random interval drawn from [lowerBound, upperBound), and on each timer
// fire reads the client's current leader, prints it, and re-draws the
// interval before waiting again. The goroutine is started with Start and
// torn down with Stop, mirroring the timer pattern used by
// TransactionRetryManager.
type EventManager struct {
	client ClientEventManager
	log    *logger.Logger

	lowerBound time.Duration
	upperBound time.Duration

	timer         *time.Timer
	timerStopCh   chan struct{}
	timerDoneCh   chan struct{}
	timerStarted  atomic.Bool
	timerStopOnce sync.Once
	totalTicks    int
}

func NewEventManager(client ClientEventManager, log *logger.Logger, lowerBound, upperBound time.Duration) *EventManager {
	eventTimer := time.NewTimer(time.Hour)
	eventTimer.Stop()

	return &EventManager{
		client:      client,
		log:         log,
		lowerBound:  lowerBound,
		upperBound:  upperBound,
		timer:       eventTimer,
		timerStopCh: make(chan struct{}),
		timerDoneCh: make(chan struct{}),
	}
}

// drawInterval returns a duration drawn uniformly from [lowerBound, upperBound).
func (em *EventManager) drawInterval() time.Duration {
	if em.upperBound <= em.lowerBound {
		return em.lowerBound
	}
	return em.lowerBound + rand.N(em.upperBound-em.lowerBound)
}

func (em *EventManager) eventTimerWorker() {
	defer close(em.timerDoneCh)

	em.timer.Reset(em.drawInterval())
	for {
		select {
		case <-em.timer.C:
			leader := em.client.currentLeaderAddr()
			em.totalTicks++
			em.log.Info("EventManager tick: current leader %s\n and number of ticks %d", leader, em.totalTicks)
			em.client.sendEventMsg(leader)

			// if em.totalTicks == 4 {
			// 	return
			// }
			em.timer.Reset(em.drawInterval())
		case <-em.timerStopCh:
			return
		}
	}
}

func (em *EventManager) Start() {
	if em.timerStarted.CompareAndSwap(false, true) {
		go em.eventTimerWorker()
	}
}

func (em *EventManager) Stop() {
	em.timerStopOnce.Do(func() {
		close(em.timerStopCh)
	})
	if em.timerStarted.Load() {
		<-em.timerDoneCh
	}
}
