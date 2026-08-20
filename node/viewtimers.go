package node

import "time"

const (
	leaderProgressTimeout = 7 * time.Second
	newViewTimeout        = 7 * time.Second
)

// was 90

// resetOneShotTimer starts a timer or moves its deadline forward. All timer
// operations are performed by the node event loop, so no additional locking
// is needed.
func resetOneShotTimer(timer **time.Timer, timeout time.Duration) <-chan time.Time {
	if *timer == nil {
		*timer = time.NewTimer(timeout)
		return (*timer).C
	}

	if !(*timer).Stop() {
		select {
		case <-(*timer).C:
		default:
		}
	}
	(*timer).Reset(timeout)
	return (*timer).C
}

func stopOneShotTimer(timer *time.Timer) {
	if timer == nil {
		return
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

// resetLeaderProgressTimer is called after every executed sequence number and
// when a new view is accepted. Its first call starts the timer.

// keeps reseeting after expiry
func (n *Node) resetLeaderProgressTimer() {
	if !n.cfg.Fixed {
		return
	}
	n.leaderProgressTimerCh = resetOneShotTimer(&n.leaderProgressTimer, leaderProgressTimeout)
}

func (n *Node) stopLeaderProgressTimer() {
	stopOneShotTimer(n.leaderProgressTimer)
	n.leaderProgressTimerCh = nil
}

// startNewViewTimer starts the deadline for the primary of the pending view to
// produce a valid NewView message.
func (n *Node) startNewViewTimer() {
	if !n.cfg.Fixed {
		return
	}
	n.newViewTimerCh = resetOneShotTimer(&n.newViewTimer, newViewTimeout)
}

func (n *Node) stopNewViewTimer() {
	stopOneShotTimer(n.newViewTimer)
	n.newViewTimerCh = nil
}

func (n *Node) stopViewTimers() {
	n.stopLeaderProgressTimer()
	n.stopNewViewTimer()
}

func (n *Node) acceptNewViewTimers() {
	n.stopNewViewTimer()
	n.resetLeaderProgressTimer()
}

func (n *Node) handleLeaderProgressTimeout() {
	n.stopLeaderProgressTimer()
	n.log.Error("Leader progress timer expired; entering view change")
	if n.cfg.PeakTpsTest {
		n.log.Warn("Peak TPS test is enabled so ignoring")
		return
	}
	n.enterViewChange()
}

func (n *Node) handleNewViewTimeout() {
	n.stopNewViewTimer()
	n.log.Warn("New view timer expired; entering the next view change")
	n.enterViewChange()
}
