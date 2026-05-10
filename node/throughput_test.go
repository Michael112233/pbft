package node

import (
	"math"
	"testing"
	"time"

	"github.com/michael112233/pbft/core"
)

func TestCheckpointThroughputRecordsBoundaryAndResetsInterval(t *testing.T) {
	n, _ := newTestNodeWithKeys(t, 1, 4)
	start := time.Unix(100, 0)

	if belowTarget := n.observeExecutedSlotForThroughput(1, start, 1, 1); belowTarget {
		t.Fatal("seq 1 initialization returned below target")
	}
	if belowTarget := n.observeExecutedSlotForThroughput(CHECKPOINT_INTERVAL, start.Add(2*time.Second), 1, 1); !belowTarget {
		t.Fatal("checkpoint throughput should be below default target")
	}

	got := n.CheckpointThroughputsSnapshot()
	viewThroughputs := got[1]
	if len(viewThroughputs) != 1 {
		t.Fatalf("view 1 throughput count = %d, want 1", len(viewThroughputs))
	}
	if viewThroughputs[0] != 10 {
		t.Fatalf("throughput = %f, want 10", viewThroughputs[0])
	}

	n.throughputMu.RLock()
	defer n.throughputMu.RUnlock()
	if !n.throughputIntervalStart.Equal(start.Add(2 * time.Second)) {
		t.Fatalf("throughput interval start = %v, want %v", n.throughputIntervalStart, start.Add(2*time.Second))
	}
	if n.throughputIntervalStartSeq != CHECKPOINT_INTERVAL {
		t.Fatalf("throughput interval start seq = %d, want %d", n.throughputIntervalStartSeq, CHECKPOINT_INTERVAL)
	}
	if n.targetThroughput != defaultTargetThroughput {
		t.Fatalf("target throughput = %f, want %f", n.targetThroughput, float64(defaultTargetThroughput))
	}
}

func TestCheckpointThroughputGroupsByCurrentView(t *testing.T) {
	n, _ := newTestNodeWithKeys(t, 1, 4)
	start := time.Unix(100, 0)

	n.observeExecutedSlotForThroughput(1, start, 1, 1)
	n.observeExecutedSlotForThroughput(CHECKPOINT_INTERVAL, start.Add(2*time.Second), 1, 1)

	n.viewMu.Lock()
	n.view = 2
	n.viewMu.Unlock()

	n.observeExecutedSlotForThroughput(CHECKPOINT_INTERVAL+1, start.Add(3*time.Second), 2, 2)
	n.observeExecutedSlotForThroughput(2*CHECKPOINT_INTERVAL, start.Add(7*time.Second), 2, 2)

	got := n.CheckpointThroughputsSnapshot()
	if len(got[1]) != 1 {
		t.Fatalf("view 1 throughput count = %d, want 1", len(got[1]))
	}
	if len(got[2]) != 1 {
		t.Fatalf("view 2 throughput count = %d, want 1", len(got[2]))
	}
	if got[1][0] != 10 {
		t.Fatalf("view 1 throughput = %f, want 10", got[1][0])
	}
	if got[2][0] != 4 {
		t.Fatalf("view 2 throughput = %f, want 4", got[2][0])
	}
}

func TestCheckpointThroughputsSnapshotIsDeepCopy(t *testing.T) {
	n, _ := newTestNodeWithKeys(t, 1, 4)
	start := time.Unix(100, 0)

	n.observeExecutedSlotForThroughput(1, start, 1, 1)
	n.observeExecutedSlotForThroughput(CHECKPOINT_INTERVAL, start.Add(2*time.Second), 1, 1)

	snapshot := n.CheckpointThroughputsSnapshot()
	snapshot[1][0] = 99
	snapshot[2] = []float64{123}

	got := n.CheckpointThroughputsSnapshot()
	if got[1][0] != 10 {
		t.Fatalf("internal view 1 throughput = %f, want 10", got[1][0])
	}
	if _, exists := got[2]; exists {
		t.Fatal("snapshot mutation added view 2 to internal throughput map")
	}
}

func TestCheckpointThroughputIncreasesTargetWhenAboveTarget(t *testing.T) {
	n, _ := newTestNodeWithKeys(t, 1, 4)
	start := time.Unix(100, 0)

	n.observeExecutedSlotForThroughput(1, start, 1, 1)
	n.targetThroughput = 9

	if belowTarget := n.observeExecutedSlotForThroughput(CHECKPOINT_INTERVAL, start.Add(2*time.Second), 1, 1); belowTarget {
		t.Fatal("checkpoint throughput should not be below target")
	}
	if n.targetThroughput != 9.09 {
		t.Fatalf("target throughput = %f, want 9.09", n.targetThroughput)
	}
}

func TestMaxRecentViewThroughputLockedUsesPreviousFaultWindow(t *testing.T) {
	n, _ := newTestNodeWithKeys(t, 1, 4)
	n.checkpointThroughputs[1] = []float64{100}
	n.checkpointThroughputs[2] = []float64{7}
	n.checkpointThroughputs[3] = []float64{12}
	n.checkpointThroughputs[4] = []float64{9}
	n.checkpointThroughputs[5] = []float64{11}
	n.checkpointThroughputs[6] = []float64{99}

	n.throughputMu.Lock()
	got := n.maxRecentViewThroughputLocked(6)
	n.throughputMu.Unlock()

	if got != 12 {
		t.Fatalf("max recent throughput = %f, want 12", got)
	}
}

func TestMaxRecentViewThroughputLockedReturnsDefaultWhenEmpty(t *testing.T) {
	n, _ := newTestNodeWithKeys(t, 1, 4)

	n.throughputMu.Lock()
	got := n.maxRecentViewThroughputLocked(2)
	n.throughputMu.Unlock()

	if got != defaultTargetThroughput {
		t.Fatalf("max recent throughput = %f, want %f", got, float64(defaultTargetThroughput))
	}
}

func TestMaxRecentViewFinalThroughputLockedUsesOnlyLastValuePerView(t *testing.T) {
	n, _ := newTestNodeWithKeys(t, 1, 4)
	n.checkpointThroughputs[1] = []float64{100}
	n.checkpointThroughputs[2] = []float64{7, 8}
	n.checkpointThroughputs[3] = []float64{50, 12}
	n.checkpointThroughputs[4] = []float64{9}
	n.checkpointThroughputs[5] = []float64{11, 10}
	n.checkpointThroughputs[6] = []float64{99}

	n.throughputMu.Lock()
	got := n.maxRecentViewFinalThroughputLocked(6)
	n.throughputMu.Unlock()

	if got != 12 {
		t.Fatalf("max recent final throughput = %f, want 12", got)
	}
}

func TestMaxRecentViewFinalThroughputLockedReturnsDefaultWhenEmpty(t *testing.T) {
	n, _ := newTestNodeWithKeys(t, 1, 4)

	n.throughputMu.Lock()
	got := n.maxRecentViewFinalThroughputLocked(2)
	n.throughputMu.Unlock()

	if got != defaultTargetThroughput {
		t.Fatalf("max recent final throughput = %f, want %f", got, float64(defaultTargetThroughput))
	}
}

func TestHandleNewViewSetsTargetThroughputFromRecentViews(t *testing.T) {
	n, _ := newTestNodeWithKeys(t, 1, 4)
	n.lastExecuted = 42
	n.checkpointThroughputs[1] = []float64{8}
	n.checkpointThroughputs[2] = []float64{13}
	viewChangeLog := testRoundRobinViewChangeLog(3, 1, 2, 3)
	n.viewChangeMsgsLog[3] = viewChangeLog

	n.HandleNewView(core.NewViewMsg{
		NewViewNumber: 3,
		From:          3,
		PreprepareLog: []core.PreprepareMsgSig{},
		ViewChangeLog: viewChangeLog,
	}, nil)

	if !n.throughputMu.TryLock() {
		t.Fatal("throughputMu remained locked after HandleNewView")
	}
	defer n.throughputMu.Unlock()

	if math.Abs(n.targetThroughput-11.7) > 0.000001 {
		t.Fatalf("target throughput = %f, want 11.7", n.targetThroughput)
	}
	if n.throughputIntervalStartSeq != 42 {
		t.Fatalf("throughput interval start seq = %d, want 42", n.throughputIntervalStartSeq)
	}
}

func testRoundRobinViewChangeLog(view int64, fromIDs ...int) []*core.ViewChangeMsgSig {
	viewChangeLog := make([]*core.ViewChangeMsgSig, 0, len(fromIDs))
	for _, from := range fromIDs {
		viewChangeLog = append(viewChangeLog, &core.ViewChangeMsgSig{
			ViewChangeMsg: core.ViewChangeMsg{
				ViewNumber:          view,
				CheckpointSeqNumber: 0,
				From:                from,
				PreparedCerts:       map[int64]*core.PreparedCert{},
				Type:                core.VCTypeRoundRobin,
				RoundRobinData:      &core.RoundRobinVCData{},
			},
		})
	}
	return viewChangeLog
}
