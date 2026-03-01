package node

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/logger"
)

type TimerManager struct {
	pendingClientReqMu sync.Mutex // also protects pbftTimerInitiated in locked fn
	pendingClientReq   map[clientRequestKey]core.ClientMsgSignature
	pbftTimer          *time.Timer
	pbftTimerInitiated bool
	pbftTimeout        time.Duration
	pbftTimerStopCh    chan struct{}
	pbftTimerStopOnce  sync.Once
	pbftTimerStarted   atomic.Bool
	newViewTimeout     time.Duration
	newViewTimerOn     atomic.Bool
	newViewTimerEpoch  atomic.Int64

	viewChangeTimeoutDummyCount atomic.Int64
	log                         *logger.Logger
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
		pendingClientReq:   make(map[clientRequestKey]core.ClientMsgSignature),
		pbftTimer:          pbftTimer,
		pbftTimerInitiated: false,
		pbftTimeout:        defaultPBFTRequestTimeout,
		newViewTimeout:     defaultPBFTRequestTimeout,
		pbftTimerStopCh:    make(chan struct{}),
		log:                log,
	}
}

func (tm *TimerManager) startNewViewTimer(n *Node) {
	if !tm.newViewTimerOn.CompareAndSwap(false, true) {
		return
	}

	epoch := tm.newViewTimerEpoch.Add(1)
	tm.log.Info("new-view timer started")
	go func(localEpoch int64) {
		timer := time.NewTimer(tm.newViewTimeout)
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
		n.handleViewChangeTimeoutDummy()
		n.viewMu.Unlock()
	}(epoch)
}

func (tm *TimerManager) stopNewViewTimer() {
	tm.log.Info("new-view timer stopped")
	tm.newViewTimerEpoch.Add(1)
	tm.newViewTimerOn.Store(false)
}

func (tm *TimerManager) pbftTimerWorker(n *Node) {
	tm.log.Info("PBFT timer worker started")
	for {
		select {
		case <-tm.pbftTimer.C:
			tm.handlePBFTTimerExpiry(n)
		case <-tm.pbftTimerStopCh:
			return
		}
	}
}

func (tm *TimerManager) trackPreprepareRequest(msg core.ClientMsgSignature) {
	key := makeClientRequestKey(msg.Data)

	tm.pendingClientReqMu.Lock()
	defer tm.pendingClientReqMu.Unlock()

	if _, exists := tm.pendingClientReq[key]; exists {
		return
	}
	tm.pendingClientReq[key] = msg

	if !tm.pbftTimerInitiated {
		tm.log.Info("Starting PBFT timer for new pending request")
		tm.startPBFTTimerLocked()
	}
}

func (tm *TimerManager) onRequestExecuted(msg core.ClientMsg) {
	key := makeClientRequestKey(msg)

	tm.pendingClientReqMu.Lock()
	if _, exists := tm.pendingClientReq[key]; !exists {
		tm.log.Warn("Executed request not found in pending requests; key: %v", key)
		// tm.pendingClientReqMu.Unlock()
		// return
	}
	defer tm.pendingClientReqMu.Unlock()
	delete(tm.pendingClientReq, key)

	if len(tm.pendingClientReq) == 0 { // imp in case gap in client req then for new req premature timeout
		tm.log.Info("No more pending requests; stopping PBFT timer at execute")
		tm.stopPBFTTimerLocked()
		return
	}
	if tm.pbftTimerInitiated {
		// tm.log.Info("Resetting PBFT timer at execute with pending requests remaining")
		tm.resetPBFTTimerLocked()
	}
}

func (tm *TimerManager) startPBFTTimerLocked() {
	if tm.pbftTimer == nil {
		return
	}
	if !tm.pbftTimer.Stop() {
		select {
		case <-tm.pbftTimer.C:
		default:
		}
	}
	tm.pbftTimer.Reset(tm.pbftTimeout)
	tm.pbftTimerInitiated = true
}

func (tm *TimerManager) resetPBFTTimerLocked() {
	if tm.pbftTimer == nil {
		return
	}
	if !tm.pbftTimer.Stop() {
		select {
		case <-tm.pbftTimer.C:
		default:
		}
	}
	tm.pbftTimer.Reset(tm.pbftTimeout)
	tm.pbftTimerInitiated = true
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
	tm.log.Info("PBFT timer expired; checking pending requests")
	shouldTriggerViewChange := false

	tm.pendingClientReqMu.Lock()
	if len(tm.pendingClientReq) > 0 {
		n.viewMu.Lock()
		defer n.viewMu.Unlock()
		// n.viewChangeRunning = true

		tm.log.Info(" remaining request count: %d; triggering dummy view-change", len(tm.pendingClientReq))
		shouldTriggerViewChange = true
	} else {
		tm.log.Info("No pending requests at timer expiry; no dummy trigger needed")
		tm.stopPBFTTimerLocked()
	}
	tm.pbftTimerInitiated = false
	tm.pendingClientReqMu.Unlock()

	if shouldTriggerViewChange {
		tm.viewChangeTimeoutDummyCount.Add(1)
		// n.handleViewChangeTimeoutDummy()
	}
}
