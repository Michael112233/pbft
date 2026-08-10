package node

import (
	"testing"
	"time"

	"github.com/michael112233/pbft/logger"
)

func TestPeriodicViewTimerCountsShadowEpisodesWithoutChangingView(t *testing.T) {
	const timeout = time.Second
	n, manager := newShadowSuspicionTestNode(t, false, timeout)

	firstExecution := time.Now()
	manager.RecordExecution(1, 1, firstExecution)
	manager.handleTimerDelivery(firstExecution.Add(timeout))

	if got := n.GetShadowSuspicionTotal(); got != 1 {
		t.Fatalf("shadow suspicion total = %d, want 1", got)
	}
	if n.view != 1 || n.forView != 1 || n.viewChangeRunning {
		t.Fatalf("periodic shadow timeout changed view state: view=%d forView=%d running=%t", n.view, n.forView, n.viewChangeRunning)
	}

	manager.handleTimerDelivery(firstExecution.Add(2 * timeout))
	if got := n.GetShadowSuspicionTotal(); got != 1 {
		t.Fatalf("continuous stall changed shadow suspicion total to %d, want 1", got)
	}

	resumedAt := firstExecution.Add(3 * timeout)
	manager.RecordExecution(1, 2, resumedAt)
	manager.handleTimerDelivery(resumedAt.Add(timeout))
	if got := n.GetShadowSuspicionTotal(); got != 2 {
		t.Fatalf("shadow suspicion total after second episode = %d, want 2", got)
	}

	resumedAgainAt := resumedAt.Add(2 * timeout)
	manager.RecordExecution(1, 3, resumedAgainAt)
	manager.handleTimerDelivery(resumedAgainAt.Add(timeout))
	if got := n.GetShadowSuspicionTotal(); got != 3 {
		t.Fatalf("shadow suspicion total after third episode = %d, want 3", got)
	}
}

func TestFixedViewTimerCountsBeforeStartingViewChange(t *testing.T) {
	const timeout = time.Second
	n, manager := newShadowSuspicionTestNode(t, true, timeout)

	firstExecution := time.Now()
	manager.RecordExecution(1, 1, firstExecution)
	manager.handleTimerDelivery(firstExecution.Add(timeout))

	if got := n.GetShadowSuspicionTotal(); got != 1 {
		t.Fatalf("shadow suspicion total = %d, want 1", got)
	}
	if !n.viewChangeRunning {
		t.Fatal("fixed timeout did not start a view change")
	}
	if n.forView != 2 {
		t.Fatalf("forView = %d, want 2", n.forView)
	}
}

func TestViewTimerDoesNotCountStaleOrOverlappingExpiry(t *testing.T) {
	n, manager := newShadowSuspicionTestNode(t, false, time.Second)

	n.handleViewTimerExpiry(2)
	if got := n.GetShadowSuspicionTotal(); got != 0 {
		t.Fatalf("stale expiry changed shadow suspicion total to %d", got)
	}

	n.viewChangeRunning = true
	n.handleViewTimerExpiry(1)
	if got := n.GetShadowSuspicionTotal(); got != 0 {
		t.Fatalf("overlapping expiry changed shadow suspicion total to %d", got)
	}

	manager.lock.Lock()
	shadowAwaitingProgress := manager.shadowAwaitingProgress
	manager.lock.Unlock()
	if shadowAwaitingProgress {
		t.Fatal("ignored expiry marked a shadow timeout")
	}
}

func newShadowSuspicionTestNode(t *testing.T, fixed bool, timeout time.Duration) (*Node, *ViewTimerManager) {
	t.Helper()
	log := logger.NewLogger(1, "shadow-suspicion-test")
	manager := newViewTimerManager(log, true, timeout)
	n := &Node{
		NodeID:           1,
		view:             1,
		forView:          1,
		fixed:            fixed,
		log:              log,
		viewTimerManager: manager,
	}
	manager.Start(n)
	t.Cleanup(manager.Close)
	return n, manager
}
