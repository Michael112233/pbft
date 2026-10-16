package node

import (
	"time"

	"github.com/michael112233/pbft/core"
)

// Per-view progress summary: what the Fixed/Perf timers have to cover, measured
// on every node so their timeouts can be set from data
// (scripts/timer_budget_summary.py). Log only, event-loop owned, one or two lines
// per view.
//
//	PROGRESS NV    at install: nv_timer_to_install_ms, from the last start of this
//	               node's new-view timer to installing the view (what the new-view
//	               timer covers), and quorum_to_install_ms, from this node's 2f+1
//	               ViewChanges. They differ under Election: the timer starts only when
//	               the candidate's VDF finishes (candidate) or the node grants its vote
//	               (voters), so the quorum figure also holds the VDF race. -1 when the
//	               node had no such event for the view (e.g. it followed a NewView).
//	PROGRESS VIEW  when the node leaves the view: time from install to its first
//	               executed slot and the largest gap between executed slots (what the
//	               progress timer covers), slots executed, and the tail from the last
//	               executed slot (or the install) to leaving.
type progressStats struct {
	active    bool
	view      core.ViewID
	leader    bool
	action    core.Action
	install   time.Time
	firstExec time.Duration
	lastExec  time.Time
	maxGap    time.Duration
	slots     int

	// this node's latest 2f+1 ViewChanges, and latest new-view timer start
	quorumView  core.ViewID
	quorumAt    time.Time
	nvTimerView core.ViewID
	nvTimerAt   time.Time
}

// progressQuorum records when this node collected 2f+1 ViewChanges for forView.
func (n *Node) progressQuorum(forView core.ViewID) {
	n.progress.quorumView = forView
	n.progress.quorumAt = time.Now()
}

// progressNewViewTimerStarted records a start of the new-view timer, for the view
// this node is trying to reach.
func (n *Node) progressNewViewTimerStarted() {
	n.progress.nvTimerView = n.GetForViewID()
	n.progress.nvTimerAt = time.Now()
}

// progressInstall opens the summary of a newly installed view and logs how long
// the install took after this node's quorum. A view still open (left without
// enterViewChange, e.g. by following a NewView to a higher view) is closed first.
func (n *Node) progressInstall(view core.ViewID, leader bool) {
	now := time.Now()
	n.progressViewEnd("next_install", now)
	p := &n.progress
	role := "replica"
	if leader {
		role = "leader"
	}
	quorumMs, nvTimerMs := -1.0, -1.0
	if p.quorumView == view && !p.quorumAt.IsZero() {
		quorumMs = msSince(p.quorumAt, now)
	}
	if p.nvTimerView == view && !p.nvTimerAt.IsZero() {
		nvTimerMs = msSince(p.nvTimerAt, now)
	}
	action := n.GetCurrAction()
	n.log.Info("PROGRESS NV: view (%d,%d) role=%s action=%s nv_timer_to_install_ms=%.1f quorum_to_install_ms=%.1f",
		view.Generation, view.Counter, role, core.ActiontoString(action), nvTimerMs, quorumMs)
	*p = progressStats{active: true, view: view, leader: leader, action: action, install: now,
		quorumView: p.quorumView, quorumAt: p.quorumAt, nvTimerView: p.nvTimerView, nvTimerAt: p.nvTimerAt}
}

// progressExec records one executed slot of the open view.
func (n *Node) progressExec(now time.Time) {
	p := &n.progress
	if !p.active {
		return
	}
	if p.slots == 0 {
		p.firstExec = now.Sub(p.install)
	} else if gap := now.Sub(p.lastExec); gap > p.maxGap {
		p.maxGap = gap
	}
	p.lastExec = now
	p.slots++
}

// progressViewEnd closes the open view's summary; end says why it closed.
func (n *Node) progressViewEnd(end string, now time.Time) {
	p := &n.progress
	if !p.active {
		return
	}
	p.active = false
	role := "replica"
	if p.leader {
		role = "leader"
	}
	firstMs, tailMs := -1.0, msSince(p.install, now)
	if p.slots > 0 {
		firstMs = float64(p.firstExec.Microseconds()) / 1000
		tailMs = msSince(p.lastExec, now)
	}
	n.log.Info("PROGRESS VIEW: view (%d,%d) role=%s action=%s first_exec_ms=%.1f max_gap_ms=%.1f slots=%d tail_ms=%.1f tenure_ms=%.1f end=%s",
		p.view.Generation, p.view.Counter, role, core.ActiontoString(p.action), firstMs,
		float64(p.maxGap.Microseconds())/1000, p.slots, tailMs, msSince(p.install, now), end)
}

func msSince(from, to time.Time) float64 {
	return float64(to.Sub(from).Microseconds()) / 1000
}
