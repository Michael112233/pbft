package node

import (
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/logger"
)

func uniformMatrix(n int, links map[[2]int]float64) [][]float64 {
	D := make([][]float64, n)
	for i := range D {
		D[i] = make([]float64, n)
	}
	for k, v := range links {
		D[k[0]-1][k[1]-1], D[k[1]-1][k[0]-1] = v, v
	}
	return D
}

// Numbers from latency_sim/far_node_n4.py: node 1 is 35 ms from everyone.
func TestPredictAwareLatencyFarNode(t *testing.T) {
	D := uniformMatrix(4, map[[2]int]float64{
		{1, 2}: 35, {1, 3}: 35, {1, 4}: 35, {2, 3}: 1, {2, 4}: 1, {3, 4}: 1,
	})
	want := []float64{71, 3, 3, 3}
	for p := 0; p < 4; p++ {
		if got := predictAwareLatency(D, 1, p); got != want[p] {
			t.Fatalf("leader %d: got %v want %v", p+1, got, want[p])
		}
	}
}

// Numbers from latency_sim/shortcut_flip_n4.py: the hub (node 1) looks best by
// its own links but the backups-exchange makes node 2 the real best.
func TestPredictAwareLatencyShortcutFlip(t *testing.T) {
	D := [][]float64{
		{0, 1, 1, 40},
		{1, 0, 100, 5},
		{1, 100, 0, 40},
		{40, 5, 40, 0},
	}
	want := []float64{80, 46, 81, 80}
	for p := 0; p < 4; p++ {
		if got := predictAwareLatency(D, 1, p); got != want[p] {
			t.Fatalf("leader %d: got %v want %v", p+1, got, want[p])
		}
	}
}

func TestSelectAwareCandidates(t *testing.T) {
	if got := selectAwareCandidates([]float64{71, 3, 3, 3}, 1, 0.1, 1); !reflect.DeepEqual(got, []int{2, 3, 4}) {
		t.Fatalf("far node: got %v", got)
	}
	// One node far better: pad to 2f+1 with the next best, in score order.
	if got := selectAwareCandidates([]float64{10, 100, 90, math.Inf(1)}, 1, 0.1, 1); !reflect.DeepEqual(got, []int{1, 3, 2}) {
		t.Fatalf("floor padding: got %v", got)
	}
	// Uniform network: everyone is a candidate, id order.
	if got := selectAwareCandidates([]float64{3, 3, 3, 3}, 1, 0.1, 1); !reflect.DeepEqual(got, []int{1, 2, 3, 4}) {
		t.Fatalf("uniform: got %v", got)
	}
}

func TestAwareMatrixCarryForwardStalenessAndMax(t *testing.T) {
	s := newAwareState()
	s.applyRows([]core.AwareRow{
		{Node: 1, Gen: 5, RTTms: []float64{0, 10, 30, 0}},
		{Node: 2, Gen: 5, RTTms: []float64{20, 0, 0, 0}},
		{Node: 3, Gen: 1, RTTms: []float64{99, 0, 0, 8}},
	})
	D := s.buildMatrix(4, 5, 3)
	if D[0][1] != 10 {
		t.Fatalf("link 1-2 must take the larger end (20) halved, got %v", D[0][1])
	}
	if D[0][2] != 15 {
		t.Fatalf("link 1-3 uses node 1's fresh end (30) even though node 3's row is stale, got %v", D[0][2])
	}
	if !math.IsInf(D[2][3], 1) {
		t.Fatalf("link 3-4 has only a stale end, want +Inf, got %v", D[2][3])
	}
	// An older row never replaces a newer one; a newer one does.
	s.applyRows([]core.AwareRow{{Node: 1, Gen: 4, RTTms: []float64{0, 500, 0, 0}}})
	if s.rows[1].Gen != 5 {
		t.Fatal("older row replaced a newer one")
	}
	s.applyRows([]core.AwareRow{{Node: 3, Gen: 5, RTTms: []float64{0, 0, 0, 8}}})
	if D := s.buildMatrix(4, 5, 3); D[2][3] != 4 {
		t.Fatalf("refreshed row must be used, got %v", D[2][3])
	}
}

func TestAwareLeaderRotation(t *testing.T) {
	s := newAwareState()
	s.candidates[5] = []int{2, 3, 4}
	got := []int{
		s.leader(core.ViewID{Generation: 5, Counter: 1}, 4),
		s.leader(core.ViewID{Generation: 5, Counter: 2}, 4),
		s.leader(core.ViewID{Generation: 5, Counter: 3}, 4),
	}
	if !reflect.DeepEqual(got, []int{4, 2, 3}) {
		t.Fatalf("rotation within generation 5: got %v", got)
	}
	if l := s.leader(core.ViewID{Generation: 9, Counter: 1}, 4); l != 2 {
		t.Fatalf("fallback without candidates: got %d want 2", l)
	}
}

func TestAwareCatchUpAdoptsRowsOnce(t *testing.T) {
	t.Chdir(t.TempDir())
	n := &Node{
		NodeID: 2,
		cfg:    &config.Config{NodeNum: 4},
		fNodes: 1,
		aware:  newAwareState(),
		log:    logger.NewLogger(2, "node"),
	}
	// The sender is in generation 4, so its rows are stamped 4.
	rows := []core.AwareRow{
		{Node: 2, Gen: 4, RTTms: []float64{70, 0, 2, 2}},
		{Node: 3, Gen: 4, RTTms: []float64{70, 2, 0, 2}},
		{Node: 4, Gen: 4, RTTms: []float64{70, 2, 2, 0}},
	}
	// Rows are adopted whatever the target action, so a later Aware generation
	// does not inherit a gap from a non-Aware jump.
	n.maybeAdoptAwareRows(4, core.FixedRoundRobin, rows)
	if got := n.aware.candidates[4]; !reflect.DeepEqual(got, []int{2, 3, 4}) {
		t.Fatalf("catch-up candidates = %v, want far node 1 excluded", got)
	}
	n.maybeAdoptAwareRows(4, core.FixedAware, []core.AwareRow{{Node: 1, Gen: 4, RTTms: []float64{0, 1, 1, 1}}})
	if _, ok := n.aware.rows[1]; ok {
		t.Fatal("rows must not be adopted again once candidates exist for the generation")
	}
}

// The aggregate for generation 3 must not touch rows or candidates while the
// node is still in 3; leaving 3 applies it stamped 4 and builds candidates[4].
func TestAwareAggregateAppliedOnGenerationSwitch(t *testing.T) {
	t.Chdir(t.TempDir())
	n := &Node{
		NodeID: 2,
		cfg:    &config.Config{NodeNum: 4},
		fNodes: 1,
		aware:  newAwareState(),
		log:    logger.NewLogger(2, "node"),
	}
	sigs := []core.EpochDataMsgSig{
		{EpochDataMsg: core.EpochDataMsg{EpochGeneration: 3, From: 2, RTTms: []float64{70, 0, 2, 2}}},
		{EpochDataMsg: core.EpochDataMsg{EpochGeneration: 3, From: 3, RTTms: []float64{70, 2, 0, 2}}},
		{EpochDataMsg: core.EpochDataMsg{EpochGeneration: 3, From: 4, RTTms: []float64{70, 2, 2, 0}}},
	}
	n.aware.pending[2] = []core.AwareRow{{Node: 1, RTTms: []float64{0, 9, 9, 9}}} // older leftover, must be dropped
	n.storeAwareAggregate(3, sigs)
	if len(n.aware.rows) != 0 || len(n.aware.candidates) != 0 {
		t.Fatalf("storing must not apply: rows=%v candidates=%v", n.aware.rows, n.aware.candidates)
	}

	n.applyPendingAwareAggregate(3)
	if got := n.aware.candidates[4]; !reflect.DeepEqual(got, []int{2, 3, 4}) {
		t.Fatalf("candidates[4] = %v, want far node 1 excluded", got)
	}
	if _, ok := n.aware.candidates[3]; ok {
		t.Fatal("aggregate 3 must build candidates[4], not candidates[3]")
	}
	for id, row := range n.aware.rows {
		if row.Gen != 4 {
			t.Fatalf("row %d stamped %d, want 4 (the generation it feeds)", id, row.Gen)
		}
	}
	if _, ok := n.aware.rows[1]; ok {
		t.Fatal("leftover aggregate for an older generation must not be applied")
	}
	if len(n.aware.pending) != 0 {
		t.Fatalf("pending must be empty after the switch, got %v", n.aware.pending)
	}

	// Leaving 4 with no stored aggregate (it never arrived) changes nothing, so
	// catch-up on the jump decides candidates[5].
	n.applyPendingAwareAggregate(4)
	if _, ok := n.aware.candidates[5]; ok {
		t.Fatal("no stored aggregate must not produce candidates")
	}
}

func TestRTTVectorMedianWindowAndUnknown(t *testing.T) {
	v := newRTTVector(10 * time.Second)
	now := time.Now()
	v.add(rttSample{peer: 2, rttMs: 5, at: now.Add(-20 * time.Second)}) // outside window
	v.add(rttSample{peer: 2, rttMs: 1, at: now})
	v.add(rttSample{peer: 2, rttMs: 3, at: now})
	v.add(rttSample{peer: 3, rttMs: 70, at: now})
	got := v.snapshot(4, 1, now)
	want := []float64{0, 2, 70, 0}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot = %v, want %v (self and unprobed peers are 0)", got, want)
	}
}
