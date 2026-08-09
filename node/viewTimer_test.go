package node

import (
	"sync"
	"testing"
	"time"

	"github.com/michael112233/pbft/logger"
)

type fakeViewTimerNode struct {
	expiries chan int64
}

func (n *fakeViewTimerNode) handleViewTimerExpiry(view int64) {
	n.expiries <- view
}

func newFakeViewTimerNode() *fakeViewTimerNode {
	return &fakeViewTimerNode{expiries: make(chan int64, 32)}
}

func TestViewTimerInitialViewArmsOnlyAfterSequenceOne(t *testing.T) {
	manager := newViewTimerManager(logger.NewLogger(1, "view-timer-test"), true, time.Second)
	t.Cleanup(manager.Close)
	manager.Start(newFakeViewTimerNode())

	manager.RecordExecution(1, 2, time.Now())
	manager.lock.Lock()
	armedBeforeSequenceOne := manager.armed
	manager.lock.Unlock()
	if armedBeforeSequenceOne {
		t.Fatal("timer armed before sequence one executed")
	}

	manager.RecordExecution(1, 1, time.Now())
	manager.lock.Lock()
	armedAfterSequenceOne := manager.armed
	activeView := manager.activeView
	manager.lock.Unlock()
	if !armedAfterSequenceOne {
		t.Fatal("timer did not arm after sequence one executed")
	}
	if activeView != 1 {
		t.Fatalf("active view = %d, want 1", activeView)
	}
}

func TestViewTimerStaleDeliveryUsesLatestExecutionDeadline(t *testing.T) {
	const timeout = 100 * time.Millisecond
	node := newFakeViewTimerNode()
	manager := newViewTimerManager(logger.NewLogger(1, "view-timer-test"), true, timeout)
	t.Cleanup(manager.Close)
	manager.Start(node)

	firstExecution := time.Now()
	manager.RecordExecution(1, 1, firstExecution)
	secondExecution := firstExecution.Add(50 * time.Millisecond)
	manager.RecordExecution(1, 2, secondExecution)

	// Simulate delivery from the first arm just after its old deadline. The
	// second execution's deadline is still 50 ms away and must remain valid.
	manager.handleTimerDelivery(firstExecution.Add(timeout + time.Millisecond))

	manager.lock.Lock()
	armed := manager.armed
	deadline := manager.deadline
	timedOutView := manager.timedOutView
	manager.lock.Unlock()
	if !armed {
		t.Fatal("stale delivery disarmed the current execution window")
	}
	if !deadline.Equal(secondExecution.Add(timeout)) {
		t.Fatalf("deadline = %v, want %v", deadline, secondExecution.Add(timeout))
	}
	if timedOutView != -1 {
		t.Fatalf("timed out view = %d, want -1", timedOutView)
	}
	select {
	case view := <-node.expiries:
		t.Fatalf("stale delivery expired view %d", view)
	default:
	}
}

func TestViewTimerExpiresOncePerViewAndRearmsForNewView(t *testing.T) {
	const timeout = 100 * time.Millisecond
	node := newFakeViewTimerNode()
	manager := newViewTimerManager(logger.NewLogger(1, "view-timer-test"), true, timeout)
	t.Cleanup(manager.Close)
	manager.Start(node)

	viewTwoStart := time.Now()
	manager.StartView(2, viewTwoStart)
	manager.handleTimerDelivery(viewTwoStart.Add(timeout))
	assertViewTimerExpiry(t, node.expiries, 2)

	manager.handleTimerDelivery(viewTwoStart.Add(2 * timeout))
	assertNoViewTimerExpiry(t, node.expiries)

	viewThreeStart := viewTwoStart.Add(3 * timeout)
	manager.StartView(3, viewThreeStart)
	manager.handleTimerDelivery(viewThreeStart.Add(timeout))
	assertViewTimerExpiry(t, node.expiries, 3)
}

func TestViewTimerStopViewAndClosePreventExpiry(t *testing.T) {
	const timeout = 20 * time.Millisecond
	node := newFakeViewTimerNode()
	manager := newViewTimerManager(logger.NewLogger(1, "view-timer-test"), true, timeout)
	manager.Start(node)

	startedAt := time.Now()
	manager.StartView(2, startedAt)
	manager.StopView(2)
	manager.handleTimerDelivery(startedAt.Add(timeout))
	assertNoViewTimerExpiry(t, node.expiries)

	manager.StartView(3, time.Now())
	manager.Close()
	time.Sleep(2 * timeout)
	assertNoViewTimerExpiry(t, node.expiries)
}

func TestViewTimerWorkerResetsAndExpiresFromLatestExecution(t *testing.T) {
	const timeout = 80 * time.Millisecond
	node := newFakeViewTimerNode()
	manager := newViewTimerManager(logger.NewLogger(1, "view-timer-test"), true, timeout)
	t.Cleanup(manager.Close)
	manager.Start(node)

	manager.RecordExecution(1, 1, time.Now())
	time.Sleep(timeout / 2)
	manager.RecordExecution(1, 2, time.Now())

	select {
	case view := <-node.expiries:
		t.Fatalf("view %d expired at the first execution deadline", view)
	case <-time.After(3 * timeout / 4):
	}

	select {
	case view := <-node.expiries:
		if view != 1 {
			t.Fatalf("expired view = %d, want 1", view)
		}
	case <-time.After(timeout):
		t.Fatal("timer did not expire from the latest execution deadline")
	}
}

func TestViewTimerConcurrentStateChanges(t *testing.T) {
	node := newFakeViewTimerNode()
	manager := newViewTimerManager(logger.NewLogger(1, "view-timer-test"), true, time.Second)
	manager.Start(node)
	manager.StartView(2, time.Now())

	var workers sync.WaitGroup
	workers.Add(3)
	go func() {
		defer workers.Done()
		for i := int64(2); i < 200; i++ {
			manager.RecordExecution(2, i, time.Now())
		}
	}()
	go func() {
		defer workers.Done()
		for i := 0; i < 200; i++ {
			manager.StopView(2)
			manager.StartView(2, time.Now())
		}
	}()
	go func() {
		defer workers.Done()
		for i := 0; i < 200; i++ {
			manager.handleTimerDelivery(time.Now())
		}
	}()
	workers.Wait()
	manager.Close()
}

func assertViewTimerExpiry(t *testing.T, expiries <-chan int64, expectedView int64) {
	t.Helper()
	select {
	case view := <-expiries:
		if view != expectedView {
			t.Fatalf("expired view = %d, want %d", view, expectedView)
		}
	case <-time.After(time.Second):
		t.Fatalf("view %d did not expire", expectedView)
	}
}

func assertNoViewTimerExpiry(t *testing.T, expiries <-chan int64) {
	t.Helper()
	select {
	case view := <-expiries:
		t.Fatalf("unexpected expiry for view %d", view)
	default:
	}
}
