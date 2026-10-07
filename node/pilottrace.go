package node

import (
	"strconv"
	"strings"
	"time"

	"github.com/michael112233/pbft/core"
)

// Pilot instrumentation (pilot_exec_trace): records when every slot executes during
// the first pilotTraceCap of each view, so the shape of the post-view-change burst and
// the throttle knee can be measured offline (scripts/analyze_exec_trace.py). Log only;
// it changes no trigger behaviour. The trace is buffered and written as one line per
// view, so the per-slot cost is an append rather than a log write.
const pilotTraceCap = 2500 * time.Millisecond

type execTrace struct {
	active    bool
	view      core.ViewID
	leader    bool
	install   time.Time
	maxSeq    int64
	startSeq  int64 // planned perf window start: maxSeq + THROUGHPUTINTERVAL_DELAY
	firstSeq  int64
	offsetsUs []int64 // execution time of firstSeq+i, in µs since install
}

// pilotTraceStart opens a trace for a newly installed view, flushing the previous
// view's trace if it was cut short by this install.
func (n *Node) pilotTraceStart(maxSeq int64, view core.ViewID) {
	if !n.cfg.PilotExecTrace {
		return
	}
	n.pilotTraceFlush("next_install")
	t := &n.pilotTrace
	t.active = true
	t.view = view
	t.leader = n.leaderId == n.GetNodeID()
	t.install = time.Now()
	t.maxSeq = maxSeq
	t.startSeq = maxSeq + THROUGHPUTINTERVAL_DELAY
	t.firstSeq = 0
	t.offsetsUs = t.offsetsUs[:0]
}

// pilotTraceExec records one executed slot. Slots execute in order, so only the
// first seq is stored and slot i is firstSeq+i.
func (n *Node) pilotTraceExec(seq int64, now time.Time) {
	t := &n.pilotTrace
	if !t.active {
		return
	}
	since := now.Sub(t.install)
	if since > pilotTraceCap {
		n.pilotTraceFlush("cap")
		return
	}
	if len(t.offsetsUs) == 0 {
		t.firstSeq = seq
	}
	t.offsetsUs = append(t.offsetsUs, since.Microseconds())
}

func (n *Node) pilotTraceFlush(end string) {
	t := &n.pilotTrace
	if !t.active {
		return
	}
	t.active = false
	var b strings.Builder
	for i, us := range t.offsetsUs {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatInt(us, 10))
	}
	n.log.Info("PILOT_EXEC view=(%d,%d) leader=%t install_unix_us=%d max_seq=%d start_seq=%d first_seq=%d n=%d end=%s offsets_us=%s",
		t.view.Generation, t.view.Counter, t.leader, t.install.UnixMicro(), t.maxSeq, t.startSeq, t.firstSeq, len(t.offsetsUs), end, b.String())
}
