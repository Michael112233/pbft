package node

import (
	"testing"
	"time"

	"github.com/michael112233/pbft/core"
)

func TestLeaderProgressTimerStartsAndStops(t *testing.T) {
	n := &Node{}
	n.resetLeaderProgressTimer()
	t.Cleanup(n.stopViewTimers)

	if n.leaderProgressTimer == nil || n.leaderProgressTimerCh == nil {
		t.Fatal("leader progress timer was not started")
	}

	select {
	case <-n.leaderProgressTimerCh:
	case <-time.After(5 * 100 * time.Millisecond):
		t.Fatal("leader progress timer did not expire")
	}

	n.stopLeaderProgressTimer()
	if n.leaderProgressTimerCh != nil {
		t.Fatal("leader progress timer channel remained enabled after stop")
	}
}

func TestAcceptNewViewSwitchesTimers(t *testing.T) {
	n := &Node{}
	n.startNewViewTimer()
	t.Cleanup(n.stopViewTimers)

	if n.newViewTimerCh == nil {
		t.Fatal("new view timer was not started")
	}

	n.acceptNewViewTimers()

	if n.newViewTimerCh != nil {
		t.Fatal("new view timer remained enabled after accepting a new view")
	}
	if n.leaderProgressTimerCh == nil {
		t.Fatal("leader progress timer was not started after accepting a new view")
	}
}

func TestViewChangeSenderIsCountedOnce(t *testing.T) {
	view := core.ViewID{Generation: 1, Counter: 2}
	n := &Node{viewChangeMsgsLog: make(map[core.ViewID][]*core.ViewChangeMsgSig)}
	viewChange := &core.ViewChangeMsgSig{
		ViewChangeMsg: core.ViewChangeMsg{ViewNumber: view, From: 1},
	}

	if !n.appendViewChangeIfNew(viewChange) {
		t.Fatal("first view-change message was rejected")
	}
	if n.appendViewChangeIfNew(viewChange) {
		t.Fatal("duplicate view-change message was accepted")
	}
	if got := n.uniqueViewChangeCount(view); got != 1 {
		t.Fatalf("unique view-change count = %d, want 1", got)
	}
}
