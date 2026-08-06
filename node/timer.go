package node

import (
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/logger"
)

type TimerManager struct {
	lock sync.Mutex // also protects pbftTimerInitiated in locked fn

	pbftTimer            *time.Timer
	pbftTimerInitiated   bool
	pbftTimeout          time.Duration
	pbftTimeoutJitterMax time.Duration
	pbftTimerStopCh      chan struct{}
	pbftTimerStopOnce    sync.Once
	pbftTimerStarted     atomic.Bool
	// newViewTimeout       time.Duration
	newViewTimerOn     atomic.Bool
	newViewTimerEpoch  atomic.Int64
	newViewTimeoutLock sync.Mutex // protects
	newViewTimeout     time.Duration

	viewChangeTimeoutDummyCount atomic.Int64

	periodicElectionTimeout    time.Duration
	periodicElectionTimerOn    atomic.Bool
	periodicElectionTimerEpoch atomic.Int64

	log      *logger.Logger
	node_ref *Node
}

func NewTimerManager(log *logger.Logger) *TimerManager {
	pbftTimer := time.NewTimer(defaultPBFTRequestTimeout)
	if !pbftTimer.Stop() {
		select {
		case <-pbftTimer.C:
		default:
		}
	}
	return &TimerManager{

		pbftTimer:          pbftTimer,
		pbftTimerInitiated: false,
		pbftTimeout:        defaultPBFTRequestTimeout,
		// newViewTimeout:          500 * time.Millisecond,
		pbftTimeoutJitterMax:    defaultPBFTRequestTimeoutJitterMax,
		newViewTimeout:          defaultPBFTRequestTimeout,
		periodicElectionTimeout: defaultPBFTRequestTimeout,
		pbftTimerStopCh:         make(chan struct{}),
		log:                     log,
	}
}

func (tm *TimerManager) startNewViewTimer(n *Node) {
	if !tm.newViewTimerOn.CompareAndSwap(false, true) {
		tm.log.Time("new-view timer already started")
		return
	}

	epoch := tm.newViewTimerEpoch.Add(1)
	// tm.log.Time("new-view timer started with duration %v", 2*tm.newViewTimeout)
	go func(localEpoch int64) {
		// tm.newViewTimeoutLock.Lock()
		// tm.newViewTimeout = 2 * tm.newViewTimeout

		// timer := time.NewTimer(tm.newViewTimeout)
		// tm.newViewTimeoutLock.Unlock()
		timer := time.NewTimer(tm.NextPBFTTimeoutLocked())
		defer timer.Stop()

		select {
		case <-timer.C:
		case <-tm.pbftTimerStopCh:
			if tm.newViewTimerEpoch.Load() == localEpoch {
				tm.newViewTimerOn.Store(false)
			}
			return
		}

		// Ignore stale timer goroutines.
		if tm.newViewTimerEpoch.Load() != localEpoch || !tm.newViewTimerOn.Load() {
			return
		}

		// This one-shot timer has fired; allow callback code to re-arm a new timer.
		tm.newViewTimerOn.Store(false)

		n.viewMu.Lock()
		tm.log.Error("New-view timer expired for view %d; triggering dummy view-change", n.forView)
		if !n.peakTpsTest || false {
			tm.log.Error("Triggering dummy view-change due to new-view timer expiry")
			n.handleViewChangeTimeoutDummy()
		}
		n.viewMu.Unlock()
	}(epoch)
}

func (tm *TimerManager) startPeriodicElectionTimer(n *Node) {
	if !tm.periodicElectionTimerOn.CompareAndSwap(false, true) {
		tm.log.Time("periodic election timer already started")
		return
	}

	epoch := tm.periodicElectionTimerEpoch.Add(1)
	tm.log.Time("periodic election timer started")
	go func(localEpoch int64) {
		timeout := time.Duration(rand.Int64N(500)+1) * time.Millisecond
		timer := time.NewTimer(timeout)
		defer timer.Stop()

		select {
		case <-timer.C:
		case <-tm.pbftTimerStopCh:
			if tm.periodicElectionTimerEpoch.Load() == localEpoch {
				tm.periodicElectionTimerOn.Store(false)
			}
			return
		}

		// Ignore stale timer goroutines.
		if tm.periodicElectionTimerEpoch.Load() != localEpoch || !tm.periodicElectionTimerOn.Load() {
			return
		}

		// This one-shot timer has fired; allow callback code to re-arm a new timer.
		tm.periodicElectionTimerOn.Store(false)

		n.viewMu.Lock()
		tm.log.Error("Periodic election timer expired for view %d; triggering dummy view-change", n.forView)
		if !n.peakTpsTest || true {
			tm.log.Error("Triggering dummy view-change due to periodic election timer expiry")
			n.handleViewChangeTimeoutDummy()
		}
		n.viewMu.Unlock()
	}(epoch)
}

func (tm *TimerManager) stopNewViewTimer() {
	// tm.newViewTimeoutLock.Lock()
	// tm.newViewTimeout = 500 * time.Millisecond
	// tm.newViewTimeoutLock.Unlock()
	tm.log.Time("new-view timer stopped")
	tm.newViewTimerEpoch.Add(1)
	tm.newViewTimerOn.Store(false)
}

func (tm *TimerManager) stopPeriodicElectionTimer() {
	tm.log.Time("periodic election timer stopped")
	tm.periodicElectionTimerEpoch.Add(1)
	tm.periodicElectionTimerOn.Store(false)
}
func (tm *TimerManager) pbftTimerWorker(n *Node) {
	tm.log.Time("PBFT timer worker started")
	tm.node_ref = n
	for {
		select {
		case <-tm.pbftTimer.C:
			tm.handlePBFTTimerExpiry(n)
		case <-tm.pbftTimerStopCh:
			return
		}
	}
}

func (tm *TimerManager) trackPreprepareRequest() {

	tm.lock.Lock()
	defer tm.lock.Unlock()

	if !tm.pbftTimerInitiated {
		// tm.log.Time("Starting PBFT timer for new pending request")
		tm.startPBFTTimerLocked()
	}
}

func (tm *TimerManager) forceResetPBFTTimer() {
	tm.lock.Lock()
	defer tm.lock.Unlock()

	if tm.pbftTimerInitiated {
		tm.log.Time("Force resetting PBFT timer")
		tm.resetPBFTTimerLocked()
	}
}
func (tm *TimerManager) forceStopPBFTTimer() {
	tm.lock.Lock()
	defer tm.lock.Unlock()

	if tm.pbftTimerInitiated {
		tm.log.Time("Force stopping PBFT timer")
		tm.stopPBFTTimerLocked()
	}
}
func (tm *TimerManager) onRequestExecuted(n *Node) {

	if n.pool.PendingRequests() == 0 { // imp in case gap in client req then for new req premature
		// tm.log.Time("No more pending requests; stopping PBFT timer at execute")
		tm.lock.Lock()
		tm.stopPBFTTimerLocked()
		tm.lock.Unlock()
		return
	}
	tm.lock.Lock()
	if tm.pbftTimerInitiated {
		// tm.log.Info("Resetting PBFT timer at execute with pending requests remaining")
		tm.resetPBFTTimerLocked()
	}
	tm.lock.Unlock()
}

func (tm *TimerManager) startPBFTTimerLocked() {
	if !tm.node_ref.fixed {
		return
	}
	if tm.pbftTimer == nil {
		return
	}
	if !tm.pbftTimer.Stop() {
		select {
		case <-tm.pbftTimer.C:
		default:
		}
	}
	tm.pbftTimer.Reset(500 * time.Millisecond)
	tm.pbftTimerInitiated = true
}

func (tm *TimerManager) resetPBFTTimerLocked() {
	if !tm.node_ref.fixed {
		return
	}
	if tm.pbftTimer == nil {
		return
	}
	if !tm.pbftTimer.Stop() {
		select {
		case <-tm.pbftTimer.C:
		default:
		}
	}
	tm.pbftTimer.Reset(500 * time.Millisecond)
	tm.pbftTimerInitiated = true
}

func (tm *TimerManager) NextPBFTTimeoutLocked() time.Duration {

	if tm.node_ref.split || tm.node_ref.vcType == core.VCTypeRoundRobin {
		// tm.log.Info("Node is in split mode/ roundrobin; using base PBFT timeout without jitter")
		return tm.pbftTimeout
	}
	if tm.pbftTimeoutJitterMax <= 0 {
		return tm.pbftTimeout
	}

	maxJitterNs := tm.pbftTimeoutJitterMax.Nanoseconds()
	if maxJitterNs <= 0 {
		return tm.pbftTimeout
	}

	jitterNs := rand.Int64N(maxJitterNs + 1)
	timeout := tm.pbftTimeout + time.Duration(jitterNs)
	if timeout < tm.pbftTimeout {
		return tm.pbftTimeout
	}
	// tm.log.Info("Timeout value is %v (base %v + jitter %v)", timeout, tm.pbftTimeout, time.Duration(jitterNs))
	return timeout
}

func (tm *TimerManager) stopPBFTTimerLocked() {
	if tm.pbftTimer == nil {
		tm.pbftTimerInitiated = false
		return
	}
	if !tm.pbftTimer.Stop() {
		select {
		case <-tm.pbftTimer.C:
		default:
		}
	}
	tm.pbftTimerInitiated = false
}

func (tm *TimerManager) handlePBFTTimerExpiry(n *Node) {
	tm.log.Error("PBFT timer expired; checking pending requests")

	tm.lock.Lock()
	lenOfPending := n.pool.PendingRequests()
	if lenOfPending == 0 {
		tm.log.Time("No pending requests at timer expiry; no dummy trigger needed")
		tm.stopPBFTTimerLocked()
		tm.lock.Unlock()
		return
	}
	tm.pbftTimerInitiated = false
	tm.lock.Unlock()
	tm.log.Time("Pending requests found at timer expiry: %d; triggering dummy view-change", lenOfPending)
	n.viewMu.Lock()
	if !n.viewChangeRunning {

		if !n.peakTpsTest {
			n.log.Error("Triggering dummy view-change due to PBFT timer expiry")
			n.handleViewChangeTimeoutDummy()
		} else {
			tm.log.Error("PBFT timer expired but peak TPS test is enabled; not triggering dummy view-change")
		}
	} else {
		tm.log.Error("View change already running at timer expiry; not triggering another dummy view-change")
	}
	n.viewMu.Unlock()
}
