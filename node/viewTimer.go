package node

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/michael112233/pbft/logger"
)

const defaultViewTimerTimeout = 100 * time.Millisecond

type viewTimerNode interface {
	handleViewTimerExpiry(view int64)
}

// ViewTimerManager detects execution gaps within an active view. It is
// intentionally independent from TimerManager, which continues to own the
// new-view and periodic-election timers.
type ViewTimerManager struct {
	lock sync.Mutex

	timer        *time.Timer
	timeout      time.Duration
	enabled      bool
	armed        bool
	activeView   int64
	deadline     time.Time
	timedOutView int64
	activated    bool
	closed       bool
	node         viewTimerNode

	stopCh   chan struct{}
	doneCh   chan struct{}
	started  atomic.Bool
	stopOnce sync.Once

	log *logger.Logger
}

func NewViewTimerManager(log *logger.Logger, enabled bool) *ViewTimerManager {
	return newViewTimerManager(log, enabled, defaultViewTimerTimeout)
}

func newViewTimerManager(log *logger.Logger, enabled bool, timeout time.Duration) *ViewTimerManager {
	if timeout <= 0 {
		timeout = defaultViewTimerTimeout
	}
	timer := time.NewTimer(timeout)
	timer.Stop()
	return &ViewTimerManager{
		timer:        timer,
		timeout:      timeout,
		enabled:      enabled,
		timedOutView: -1,
		stopCh:       make(chan struct{}),
		doneCh:       make(chan struct{}),
		log:          log,
	}
}

func (vtm *ViewTimerManager) Start(node viewTimerNode) {
	if vtm == nil || !vtm.enabled || node == nil {
		return
	}

	vtm.lock.Lock()
	if vtm.closed {
		vtm.lock.Unlock()
		return
	}
	vtm.node = node
	vtm.lock.Unlock()

	if vtm.started.CompareAndSwap(false, true) {
		go vtm.worker()
	}
}

func (vtm *ViewTimerManager) worker() {
	defer close(vtm.doneCh)
	for {
		select {
		case <-vtm.timer.C:
			vtm.handleTimerDelivery(time.Now())
		case <-vtm.stopCh:
			return
		}
	}
}

// RecordExecution starts the initial-view timer only after sequence one. Once
// activated, executions reset an already armed timer for the same view. A
// stopped or expired view is not re-armed until StartView installs a new view.
func (vtm *ViewTimerManager) RecordExecution(view int64, sequence int64, executedAt time.Time) {
	if vtm == nil || !vtm.enabled {
		return
	}
	if executedAt.IsZero() {
		executedAt = time.Now()
	}

	vtm.lock.Lock()
	defer vtm.lock.Unlock()
	if vtm.closed {
		return
	}
	// only for first exe
	if !vtm.activated {
		if sequence != 1 {
			return
		}
		vtm.activated = true
		vtm.activeView = view
		vtm.timedOutView = -1
		vtm.armLocked(executedAt.Add(vtm.timeout))
		return
	}

	if !vtm.armed || vtm.activeView != view || vtm.timedOutView == view {
		return
	}
	vtm.armLocked(executedAt.Add(vtm.timeout))
}

// StartView starts a fresh execution window for an installed view regardless
// of whether that view has executed a slot yet.
func (vtm *ViewTimerManager) StartView(view int64, startedAt time.Time) {
	if vtm == nil || !vtm.enabled {
		return
	}
	if startedAt.IsZero() {
		startedAt = time.Now()
	}

	vtm.lock.Lock()
	defer vtm.lock.Unlock()
	if vtm.closed {
		return
	}
	if vtm.activated && view <= vtm.activeView {
		return
	}
	vtm.activated = true
	vtm.activeView = view
	vtm.timedOutView = -1
	vtm.armLocked(startedAt.Add(vtm.timeout))
}

// StopView disarms only the supplied view. It intentionally leaves the
// activation state intact so only StartView can arm the next view.
func (vtm *ViewTimerManager) StopView(view int64) {
	if vtm == nil || !vtm.enabled {
		return
	}

	vtm.lock.Lock()
	defer vtm.lock.Unlock()
	if vtm.closed || !vtm.armed || vtm.activeView != view {
		return
	}
	vtm.stopLocked()
}

func (vtm *ViewTimerManager) Close() {
	if vtm == nil {
		return
	}
	vtm.stopOnce.Do(func() {
		vtm.lock.Lock()
		vtm.closed = true
		vtm.stopLocked()
		vtm.lock.Unlock()
		close(vtm.stopCh)
	})
	if vtm.started.Load() {
		<-vtm.doneCh
	}
}

func (vtm *ViewTimerManager) handleTimerDelivery(now time.Time) {
	vtm.lock.Lock()
	// just in case timer fired after close
	if vtm.closed || !vtm.armed {
		vtm.lock.Unlock()
		return
	}

	view := vtm.activeView
	if vtm.timedOutView == view {
		vtm.stopLocked()
		vtm.lock.Unlock()
		return
	}
	//stale execution timer got lock later and some other execution already updated deadline
	if remaining := vtm.deadline.Sub(now); remaining > 0 {
		vtm.timer.Reset(remaining)
		vtm.lock.Unlock()
		return
	}

	vtm.timer.Stop()
	vtm.armed = false
	vtm.deadline = time.Time{}
	vtm.timedOutView = view
	node := vtm.node
	vtm.lock.Unlock()

	if vtm.log != nil {
		vtm.log.Error("View timer expired for view %d after %v without execution", view, vtm.timeout)
	}
	if node != nil {
		node.handleViewTimerExpiry(view)
	}
}

func (vtm *ViewTimerManager) armLocked(deadline time.Time) {
	vtm.deadline = deadline
	vtm.armed = true
	delay := time.Until(deadline)
	if delay < 0 {
		delay = 0
	}
	vtm.timer.Reset(delay)
}

func (vtm *ViewTimerManager) stopLocked() {
	if vtm.timer != nil {
		vtm.timer.Stop()
	}
	vtm.armed = false
	vtm.deadline = time.Time{}
}
