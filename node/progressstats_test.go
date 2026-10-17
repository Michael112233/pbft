package node

import (
	"testing"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/logger"
)

func TestProgressStatsFirstExecAndMaxGap(t *testing.T) {
	n := &Node{NodeID: 3, cfg: &config.Config{}, log: logger.NewLogger(0, "progressstatstest")}
	view := core.ViewID{Generation: 1, Counter: 5}
	n.progressQuorum(view)
	n.progressInstall(view, false)
	t0 := n.progress.install

	n.progressExec(t0.Add(40 * time.Millisecond))
	n.progressExec(t0.Add(50 * time.Millisecond))
	n.progressExec(t0.Add(170 * time.Millisecond)) // the 120 ms gap
	n.progressExec(t0.Add(180 * time.Millisecond))
	p := n.progress
	if p.firstExec != 40*time.Millisecond || p.maxGap != 120*time.Millisecond || p.slots != 4 {
		t.Fatalf("first %v max gap %v slots %d, want 40ms 120ms 4", p.firstExec, p.maxGap, p.slots)
	}

	n.progressViewEnd("view_change", t0.Add(400*time.Millisecond))
	if n.progress.active {
		t.Fatal("view still open after progressViewEnd")
	}
	n.progressExec(t0.Add(500 * time.Millisecond)) // executions after leaving don't count
	if n.progress.slots != 4 {
		t.Fatalf("slots %d after the view closed, want 4", n.progress.slots)
	}
}

// The new-view timer start is tied to the view being reached and survives the
// reset at install, like the quorum time.
func TestProgressStatsNewViewTimerStart(t *testing.T) {
	n := &Node{NodeID: 3, cfg: &config.Config{}, log: logger.NewLogger(0, "progressstatstest")}
	view := core.ViewID{Generation: 2, Counter: 1}
	n.forViewID = view
	n.progressNewViewTimerStarted()
	started := n.progress.nvTimerAt
	n.progressInstall(view, false)
	if n.progress.nvTimerView != view || !n.progress.nvTimerAt.Equal(started) {
		t.Fatalf("nv timer start lost at install: %+v", n.progress)
	}
}

// The view's leader is recorded at install, for splitting the summary by leader site.
func TestProgressStatsRecordsLeader(t *testing.T) {
	n := &Node{NodeID: 3, cfg: &config.Config{}, log: logger.NewLogger(0, "progressstatstest")}
	n.leaderId = 6
	n.progressInstall(core.ViewID{Generation: 1, Counter: 6}, false)
	if n.progress.leaderID != 6 {
		t.Fatalf("leaderID %d, want 6", n.progress.leaderID)
	}
}

// A view left without enterViewChange is closed by the next install, and the
// next view starts from zero.
func TestProgressStatsNextInstallClosesOpenView(t *testing.T) {
	n := &Node{NodeID: 3, cfg: &config.Config{}, log: logger.NewLogger(0, "progressstatstest")}
	n.progressInstall(core.ViewID{Generation: 1, Counter: 5}, false)
	n.progressExec(n.progress.install.Add(10 * time.Millisecond))
	next := core.ViewID{Generation: 1, Counter: 6}
	n.progressInstall(next, true)
	if !n.progress.active || n.progress.view != next || !n.progress.leader || n.progress.slots != 0 {
		t.Fatalf("next view not reset: %+v", n.progress)
	}
}
