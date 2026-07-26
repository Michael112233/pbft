package node

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/michael112233/pbft/logger"
)

const (
	defaultPeriodicTimerTimeout = 5 * time.Second
)

type PeriodicTimerNode interface {
	GetNodeID() int
	HandleSendIntent(view int64)
}

type PeriodicTimerManager struct {
	lock sync.Mutex

	timer          *time.Timer
	timerInitiated bool

	timerStopCh   chan struct{}
	timerDoneCh   chan struct{}
	timerStopOnce sync.Once
	timerStarted  atomic.Bool
	view          int64

	log  *logger.Logger
	node PeriodicTimerNode
}

func NewPeriodicTimerManager(node PeriodicTimerNode, log *logger.Logger) *PeriodicTimerManager {
	timer := time.NewTimer(defaultPeriodicTimerTimeout)
	timer.Stop()
	return &PeriodicTimerManager{
		timer:          timer,
		timerInitiated: false,
		timerStopCh:    make(chan struct{}),
		timerDoneCh:    make(chan struct{}),
		log:            log,
		node:           node,
	}
}

func (ptm *PeriodicTimerManager) Start() {
	if !ptm.timerStarted.CompareAndSwap(false, true) {
		return
	}

	go ptm.StartPeriodicTimerWorker()
}

func (ptm *PeriodicTimerManager) Close() {
	ptm.timerStopOnce.Do(func() {
		ptm.stopTimer()
		close(ptm.timerStopCh)
	})

	if ptm.timerStarted.Load() {
		<-ptm.timerDoneCh
	}
}

func (ptm *PeriodicTimerManager) StartPeriodicTimerWorker() {
	ptm.log.Time("Starting periodic timer worker")
	defer close(ptm.timerDoneCh)
	for {
		select {
		case <-ptm.timer.C:

			ptm.handleTimerExpiry()

		case <-ptm.timerStopCh:
			ptm.log.Time("Stopping periodic timer worker")

			return
		}
	}
}

func (ptm *PeriodicTimerManager) startTimer(view int64) {
	ptm.lock.Lock()
	defer ptm.lock.Unlock()
	if view >= ptm.view {
		ptm.view = view
	} else {
		ptm.log.Error("Attempted to start periodic timer for view %d, but current view is %d. Ignoring.", view, ptm.view)
		return
	}
	if !ptm.timerInitiated {
		ptm.startTimerLocked()
	}
}

func (ptm *PeriodicTimerManager) stopTimer() {
	ptm.lock.Lock()
	defer ptm.lock.Unlock()
	if ptm.timerInitiated {
		ptm.stopTimerLocked()
	}
}

func (ptm *PeriodicTimerManager) startTimerLocked() {
	if ptm.timer == nil {
		return
	}
	ptm.log.Info("periodic timer started")
	ptm.timer.Reset(defaultPeriodicTimerTimeout)
	ptm.timerInitiated = true
}

func (ptm *PeriodicTimerManager) stopTimerLocked() {
	if ptm.timer == nil {
		ptm.timerInitiated = false
		return
	}
	ptm.timer.Stop()
	ptm.timerInitiated = false
}

func (ptm *PeriodicTimerManager) handleTimerExpiry() {
	ptm.lock.Lock()
	ptm.timerInitiated = false
	view := ptm.view
	ptm.lock.Unlock()

	ptm.log.Info("Periodic timer expired for view %d; triggering periodic action", view)
	go ptm.node.HandleSendIntent(view)

}

func (n *Node) startPeriodicTimerForReqExe(seq int64) {
	if n.periodic {
		if seq == 1 {
			n.log.Info("Starting periodic timer for first request execution")
			n.periodicTimerManager.startTimer(n.GetView())
		}
	}
}

func (n *Node) startPeriodicTimerForNewView(view int64) {
	if n.periodic {
		n.log.Info("Starting periodic timer for new view %d", view)
		n.periodicTimerManager.startTimer(view)
	}
}

func (n *Node) HandleSendIntent(view int64) {
	n.viewIntent.sendViewIntent(view)
}
