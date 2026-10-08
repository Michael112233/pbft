package node

import (
	"testing"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/logger"
)

func TestPilotTraceRecordsUntilCap(t *testing.T) {
	n := &Node{NodeID: 3, cfg: &config.Config{PilotExecTrace: true}, log: logger.NewLogger(0, "pilottracetest")}
	n.pilotTraceStart(100, core.ViewID{Generation: 1, Counter: 5})
	t0 := n.pilotTrace.install

	n.pilotTraceExec(101, t0.Add(10*time.Millisecond))
	n.pilotTraceExec(102, t0.Add(20*time.Millisecond))
	if n.pilotTrace.firstSeq != 101 || n.pilotTrace.startSeq != 100+n.cfg.PerfWindowDelaySlots() {
		t.Fatalf("firstSeq %d startSeq %d", n.pilotTrace.firstSeq, n.pilotTrace.startSeq)
	}
	if got := n.pilotTrace.offsetsUs; len(got) != 2 || got[0] != 10000 || got[1] != 20000 {
		t.Fatalf("offsets %v", got)
	}
	n.pilotTraceExec(103, t0.Add(pilotTraceCap+time.Millisecond))
	if n.pilotTrace.active || len(n.pilotTrace.offsetsUs) != 2 {
		t.Fatalf("trace not closed at cap: active=%t n=%d", n.pilotTrace.active, len(n.pilotTrace.offsetsUs))
	}
}

func TestPilotTraceOffByDefault(t *testing.T) {
	n := &Node{cfg: &config.Config{}}
	n.pilotTraceStart(100, core.ViewID{Generation: 1, Counter: 5})
	n.pilotTraceExec(101, time.Now())
	if n.pilotTrace.active || len(n.pilotTrace.offsetsUs) != 0 {
		t.Fatal("trace recorded with pilot_exec_trace off")
	}
}
