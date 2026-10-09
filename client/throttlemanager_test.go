package client

import (
	"errors"
	"testing"
	"time"

	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/logger"
)

var errTest = errors.New("test ack failure")

func newTestManager(t *testing.T, k int, strategy string) *throttleManager {
	t.Helper()
	m := &throttleManager{
		log:       logger.NewLogger(0, "throttletest"),
		nodeNum:   7,
		k:         k,
		strategy:  strategy,
		prep:      2 * time.Second,
		tenure:    10 * time.Second,
		slots:     make([]slot, k),
		slotCmdCh: make([]chan gateCmd, k),
		ackCh:     make(chan gateAck, 4*k),
		noticeCh:  make(chan leaderNotice, 8),
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
		out:       &jsonlWriter{}, // nil file: write is a no-op
		active:    true,           // static mode, as with throttle.enabled
	}
	for i := 0; i < k; i++ {
		m.slotCmdCh[i] = make(chan gateCmd, 4)
	}
	return m
}

// drainCmd returns the single command issued to slot i since the last drain, or
// cmd.node==0 if none.
func drainCmd(m *throttleManager, i int) (gateCmd, bool) {
	select {
	case c := <-m.slotCmdCh[i]:
		return c, true
	default:
		return gateCmd{}, false
	}
}

func TestPrimaryForView(t *testing.T) {
	for _, tt := range []struct {
		counter uint64
		n       int
		want    int
	}{
		{0, 4, 1}, {1, 4, 1}, {2, 4, 2}, {4, 4, 4}, {5, 4, 1}, {8, 7, 1}, {7, 7, 7},
	} {
		if got := primaryForView(tt.counter, tt.n); got != tt.want {
			t.Errorf("primaryForView(%d,%d) = %d, want %d", tt.counter, tt.n, got, tt.want)
		}
	}
}

// A slot retargeted to a node issues no command until prep elapses, then an
// enable; retargeting an active slot disables the old node first.
func TestSlotPrepareEnableRetarget(t *testing.T) {
	m := newTestManager(t, 1, "roundrobin")
	t0 := time.Now()

	m.retarget(0, 3, t0, t0)
	m.reconcile(t0) // before prep
	if _, ok := drainCmd(m, 0); ok {
		t.Fatal("issued a command before prep elapsed")
	}

	m.reconcile(t0.Add(2 * time.Second)) // prep elapsed
	cmd, ok := drainCmd(m, 0)
	if !ok || cmd.node != 3 || !cmd.enable {
		t.Fatalf("want enable node 3, got %+v ok=%v", cmd, ok)
	}
	m.onAck(gateAck{cmd: cmd}) // success
	if !m.slots[0].enabled || m.slots[0].enabledNode != 3 {
		t.Fatalf("slot not active on 3 after ack: %+v", m.slots[0])
	}

	// Retarget to node 5: the old node must be disabled before the new is enabled.
	t1 := t0.Add(8 * time.Second)
	m.retarget(0, 5, t1, t1)
	m.reconcile(t1)
	cmd, ok = drainCmd(m, 0)
	if !ok || cmd.node != 3 || cmd.enable {
		t.Fatalf("want disable node 3, got %+v ok=%v", cmd, ok)
	}
	m.onAck(gateAck{cmd: cmd})
	if m.slots[0].enabled {
		t.Fatal("slot still enabled after disable ack")
	}
	m.reconcile(t1.Add(2 * time.Second))
	cmd, ok = drainCmd(m, 0)
	if !ok || cmd.node != 5 || !cmd.enable {
		t.Fatalf("want enable node 5, got %+v ok=%v", cmd, ok)
	}
}

// A failed command keeps the slot occupied and is retried after the backoff.
func TestSlotCommandRetryOnFailure(t *testing.T) {
	m := newTestManager(t, 1, "roundrobin")
	t0 := time.Now()
	m.retarget(0, 2, t0, t0)
	m.reconcile(t0.Add(2 * time.Second))
	cmd, _ := drainCmd(m, 0)
	m.onAck(gateAck{cmd: cmd, err: errTest}) // fail
	if m.slots[0].enabled {
		t.Fatal("slot became active despite a failed enable")
	}
	// Pin the backoff deadline onto the fake clock (onAck stamped it off the real
	// wall clock) so the rest of the test is deterministic.
	m.slots[0].nextActionAt = t0.Add(3 * time.Second)

	// Within the backoff: no new command.
	m.reconcile(t0.Add(2500 * time.Millisecond))
	if _, ok := drainCmd(m, 0); ok {
		t.Fatal("retried inside the backoff window")
	}
	// After the backoff: retried (prep already elapsed, so an enable).
	m.reconcile(t0.Add(3100 * time.Millisecond))
	if cmd2, ok := drainCmd(m, 0); !ok || cmd2.node != 2 || !cmd2.enable {
		t.Fatalf("expected a retried enable for node 2, got %+v ok=%v", cmd2, ok)
	}
}

func TestPickEvictablePrefersFreeThenStalest(t *testing.T) {
	m := newTestManager(t, 3, "election")
	now := time.Now()
	// slot 0 occupied (observed long ago), slot 1 occupied (recent), slot 2 free.
	m.slots[0] = slot{targetNode: 1, enabled: true, enabledNode: 1, observedAt: now.Add(-9 * time.Second)}
	m.slots[1] = slot{targetNode: 2, enabled: true, enabledNode: 2, observedAt: now.Add(-1 * time.Second)}
	m.slots[2] = slot{}
	if got := m.pickEvictable(); got != 2 {
		t.Fatalf("pickEvictable with a free slot = %d, want 2", got)
	}
	// No free slot: the stalest (slot 0) is chosen.
	m.slots[2] = slot{targetNode: 3, enabled: true, enabledNode: 3, observedAt: now.Add(-2 * time.Second)}
	if got := m.pickEvictable(); got != 0 {
		t.Fatalf("pickEvictable stalest = %d, want 0", got)
	}
}

// Election keeps a slot already serving the newly observed leader (free coverage).
func TestElectionKeepsAlreadyTargetedLeader(t *testing.T) {
	m := newTestManager(t, 1, "election")
	t0 := time.Now()
	m.slots[0] = slot{targetNode: 4, enabled: true, enabledNode: 4, observedAt: t0.Add(-5 * time.Second), readyAt: t0.Add(-5 * time.Second)}
	before := m.slots[0]
	m.onNoticeElection(leaderNotice{view: core.ViewID{Generation: 1, Counter: 9}, leaderID: 4, observedAt: t0})
	if m.slots[0].targetNode != 4 || !m.slots[0].enabled {
		t.Fatalf("slot changed target for an already-targeted leader: %+v", m.slots[0])
	}
	if !m.slots[0].readyAt.Equal(before.readyAt) {
		t.Fatal("readyAt was reset, i.e. a fresh prep was paid for the same leader")
	}
}

// Election retargets its single slot to a newly observed, untargeted leader.
func TestElectionRetargetsNewLeader(t *testing.T) {
	m := newTestManager(t, 1, "election")
	t0 := time.Now()
	m.slots[0] = slot{targetNode: 4, enabled: true, enabledNode: 4, observedAt: t0.Add(-10 * time.Second)}
	m.onNoticeElection(leaderNotice{view: core.ViewID{Generation: 1, Counter: 10}, leaderID: 6, observedAt: t0})
	if m.slots[0].targetNode != 6 {
		t.Fatalf("slot not retargeted to 6: %+v", m.slots[0])
	}
}

// A notice for a node whose slot is mid-release aborts the release instead of
// handing the node to a second slot. Without this, the first slot's outstanding
// disable lands after the second slot's enable and silently ungates the sitting
// leader for the rest of its tenure.
func TestElectionKeepsLeaderStillGatedByReleasingSlot(t *testing.T) {
	m := newTestManager(t, 2, "election")
	t0 := time.Now()
	// Slot 0 was retargeted 5 -> 6 at an earlier notice; the disable of node 5 is
	// still in flight, so node 5's gate is physically on.
	readyAt := t0.Add(-time.Second)
	m.slots[0] = slot{targetNode: 6, enabled: true, enabledNode: 5, observedAt: t0, readyAt: readyAt, cmdOutstanding: true}
	// Slot 1 is the stalest, so the unfixed rule would have evicted it for node 5.
	m.slots[1] = slot{targetNode: 3, enabled: true, enabledNode: 3, observedAt: t0.Add(-30 * time.Second)}

	m.onNoticeElection(leaderNotice{view: core.ViewID{Generation: 1, Counter: 11}, leaderID: 5, observedAt: t0})

	if m.slots[0].targetNode != 5 {
		t.Fatalf("slot 0 did not reclaim the node it still has gated: %+v", m.slots[0])
	}
	if !m.slots[0].readyAt.Equal(readyAt) {
		t.Fatal("readyAt was reset: a prep was paid for a node that was never released")
	}
	if m.slots[1].targetNode != 3 {
		t.Fatalf("slot 1 was evicted for a node another slot still holds: %+v", m.slots[1])
	}
	// Nothing to command: the gate is already where it needs to be.
	m.slots[0].cmdOutstanding = false
	m.reconcile(t0)
	if cmd, ok := drainCmd(m, 0); ok {
		t.Fatalf("expected no command for a slot already holding its target, got %+v", cmd)
	}
}

// The same rule in the RoundRobin window: a slot still enabled on a node that is
// back in {counter..counter+k-1} keeps it, rather than being treated as spare
// while a different slot re-preps the same node.
func TestRoundRobinWindowKeepsSlotStillEnabledOnWantedNode(t *testing.T) {
	m := newTestManager(t, 2, "roundrobin") // nodeNum 7
	t0 := time.Now()
	// Slot 1 was retargeted 3 -> 2 earlier and its disable of node 3 has not
	// landed, so node 3 is still gated. Node 3 is the leader again at counter 3.
	m.slots[0] = slot{targetNode: 1, enabled: true, enabledNode: 1, observedAt: t0}
	m.slots[1] = slot{targetNode: 2, enabled: true, enabledNode: 3, observedAt: t0}

	m.onNoticeRoundRobin(leaderNotice{view: core.ViewID{Generation: 1, Counter: 3}, leaderID: 3, observedAt: t0})

	if m.slots[1].targetNode != 3 {
		t.Fatalf("slot 1 did not keep the node it still has gated: %+v", m.slots[1])
	}
	if m.slots[0].targetNode != 4 {
		t.Fatalf("the spare slot should have taken successor 4, got %+v", m.slots[0])
	}
	// Slot 1 issues nothing; slot 0 releases node 1 before preparing node 4.
	m.reconcile(t0)
	if cmd, ok := drainCmd(m, 1); ok {
		t.Fatalf("expected no command for the slot holding the sitting leader, got %+v", cmd)
	}
	cmd, ok := drainCmd(m, 0)
	if !ok || cmd.node != 1 || cmd.enable {
		t.Fatalf("expected a disable of node 1 on the spare slot, got %+v ok=%v", cmd, ok)
	}
}

// The RoundRobin k>=2 window holds the sitting leader and prepares the successor.
func TestRoundRobinWindowCoversSittingAndSuccessor(t *testing.T) {
	m := newTestManager(t, 2, "roundrobin") // nodeNum 7
	// Observe leader for counter 3 (node 3). Window {3,4} -> nodes {3,4}.
	m.onNoticeRoundRobin(leaderNotice{view: core.ViewID{Generation: 1, Counter: 3}, leaderID: 3, observedAt: time.Now()})
	targets := map[int]bool{m.slots[0].targetNode: true, m.slots[1].targetNode: true}
	if !targets[3] || !targets[4] {
		t.Fatalf("window did not cover nodes 3 and 4: %+v %+v", m.slots[0], m.slots[1])
	}

	// Next notice, counter 4 (node 4). Window {4,5}. Node 4 kept, node 3 -> node 5.
	m.onNoticeRoundRobin(leaderNotice{view: core.ViewID{Generation: 1, Counter: 4}, leaderID: 4, observedAt: time.Now()})
	targets = map[int]bool{m.slots[0].targetNode: true, m.slots[1].targetNode: true}
	if !targets[4] || !targets[5] {
		t.Fatalf("window did not slide to cover 4 and 5: %+v %+v", m.slots[0], m.slots[1])
	}
}

// RoundRobin k=1 schedules a mid-tenure release that hands the slot to the successor.
func TestRoundRobinSingleSlotSchedulesSuccessor(t *testing.T) {
	m := newTestManager(t, 1, "roundrobin")
	now := time.Now()
	m.onNoticeRoundRobin(leaderNotice{view: core.ViewID{Generation: 1, Counter: 2}, leaderID: 2, observedAt: now})
	if m.slots[0].targetNode != 2 {
		t.Fatalf("slot not serving current leader 2: %+v", m.slots[0])
	}
	if m.slots[0].rrSuccessor != 3 {
		t.Fatalf("successor not node 3: got %d", m.slots[0].rrSuccessor)
	}
	// The scheduled release is tenure-prep after the observation.
	wantAt := now.Add(m.tenure - m.prep)
	if !m.slots[0].rrRetargetAt.Equal(wantAt) {
		t.Fatalf("rrRetargetAt = %v, want %v", m.slots[0].rrRetargetAt, wantAt)
	}
	// Reconcile past that time hands the slot to the successor.
	m.reconcile(wantAt.Add(time.Millisecond))
	if m.slots[0].targetNode != 3 {
		t.Fatalf("slot not retargeted to successor 3 after release: %+v", m.slots[0])
	}
}

// Scenario mode: generations 1-10 Healthy, 11-20 Throttle, 21-30 Healthy, ...
func newScenarioTestManager(t *testing.T, k int) *throttleManager {
	t.Helper()
	m := newTestManager(t, k, "")
	m.scenarioMode = true
	m.scenarios = []core.Scenario{core.ScenarioHealthy, core.ScenarioThrottle}
	m.scenarioSpan = 10
	m.active = false
	return m
}

func notice(gen, counter uint64, leader int, action core.Action, at time.Time) leaderNotice {
	return leaderNotice{view: core.ViewID{Generation: gen, Counter: counter}, leaderID: leader, action: action, observedAt: at}
}

// ackAll answers every queued command successfully and returns them.
func ackAll(m *throttleManager) []gateCmd {
	var cmds []gateCmd
	for i := range m.slotCmdCh {
		if c, ok := drainCmd(m, i); ok {
			m.onAck(gateAck{cmd: c})
			cmds = append(cmds, c)
		}
	}
	return cmds
}

// Off in Healthy generations, on in Throttle ones with the policy's strategy,
// and leaving Throttle disables every gate the slots hold.
func TestScenarioModeActivatesAndDrains(t *testing.T) {
	m := newScenarioTestManager(t, 2)
	t0 := time.Now()

	m.onNotice(notice(5, 3, 3, core.FixedRoundRobin, t0)) // Healthy
	if m.active {
		t.Fatal("active in a Healthy generation")
	}
	for i := range m.slots {
		if m.slots[i].occupied() {
			t.Fatalf("slot %d occupied in a Healthy generation: %+v", i, m.slots[i])
		}
	}

	m.onNotice(notice(11, 1, 1, core.PeriodicRoundRobin, t0)) // Throttle, RoundRobin
	if !m.active || m.strategy != "roundrobin" {
		t.Fatalf("after a Throttle notice: active %v strategy %q, want true roundrobin", m.active, m.strategy)
	}
	if m.slots[0].targetNode != 1 || m.slots[1].targetNode != 2 {
		t.Fatalf("RoundRobin window not {1,2}: %d %d", m.slots[0].targetNode, m.slots[1].targetNode)
	}
	m.reconcile(t0.Add(3 * time.Second)) // prep elapsed: both enabled
	if cmds := ackAll(m); len(cmds) != 2 || !cmds[0].enable || !cmds[1].enable {
		t.Fatalf("want two enables, got %+v", cmds)
	}

	m.onNotice(notice(21, 1, 1, core.PeriodicRoundRobin, t0.Add(4*time.Second))) // Healthy again
	if m.active || m.strategy != "" {
		t.Fatalf("after leaving Throttle: active %v strategy %q", m.active, m.strategy)
	}
	m.reconcile(t0.Add(4 * time.Second))
	cmds := ackAll(m)
	if len(cmds) != 2 || cmds[0].enable || cmds[1].enable {
		t.Fatalf("want two disables on leaving Throttle, got %+v", cmds)
	}
	for i := range m.slots {
		if m.slots[i].occupied() {
			t.Fatalf("slot %d still occupied after drain: %+v", i, m.slots[i])
		}
	}
	m.onNotice(notice(21, 2, 2, core.PeriodicRoundRobin, t0.Add(5*time.Second)))
	if m.slots[0].occupied() || m.slots[1].occupied() {
		t.Fatal("a Healthy notice retargeted a slot")
	}
}

// Inside a Throttle window the strategy follows each generation's policy; the
// switch keeps the slot state and drops the RoundRobin k=1 scheduled release.
func TestScenarioModeStrategyFollowsPolicy(t *testing.T) {
	m := newScenarioTestManager(t, 1)
	t0 := time.Now()
	m.onNotice(notice(11, 1, 1, core.PeriodicRoundRobin, t0))
	if m.strategy != "roundrobin" || m.slots[0].rrRetargetAt.IsZero() {
		t.Fatalf("RoundRobin k=1: strategy %q, scheduled release %v", m.strategy, m.slots[0].rrRetargetAt)
	}
	m.onNotice(notice(12, 1, 4, core.PeriodicElection, t0.Add(time.Second)))
	if m.strategy != "election" {
		t.Fatalf("strategy %q after an Election generation, want election", m.strategy)
	}
	if !m.slots[0].rrRetargetAt.IsZero() || m.slots[0].rrSuccessor != 0 {
		t.Fatal("RoundRobin scheduled release survived the switch to Election")
	}
	if m.slots[0].targetNode != 4 {
		t.Fatalf("Election did not retarget to the new leader: %d", m.slots[0].targetNode)
	}
	m.onNotice(notice(13, 1, 1, core.PerformanceRoundRobin, t0.Add(2*time.Second)))
	if m.strategy != "roundrobin" {
		t.Fatalf("strategy %q after a RoundRobin generation", m.strategy)
	}
}

// An enable still in flight when the run leaves Throttle is disabled once acked.
func TestScenarioModeDrainWithEnableInFlight(t *testing.T) {
	m := newScenarioTestManager(t, 1)
	t0 := time.Now()
	m.onNotice(notice(11, 1, 1, core.PeriodicElection, t0))
	m.reconcile(t0.Add(3 * time.Second))
	enable, ok := drainCmd(m, 0)
	if !ok || !enable.enable {
		t.Fatalf("want an enable in flight, got %+v ok=%v", enable, ok)
	}
	m.onNotice(notice(21, 1, 1, core.PeriodicElection, t0.Add(3*time.Second))) // leaves Throttle
	m.onAck(gateAck{cmd: enable})                                              // the enable lands
	m.reconcile(t0.Add(3 * time.Second))
	if c, ok := drainCmd(m, 0); !ok || c.enable || c.node != 1 {
		t.Fatalf("want disable node 1 after the in-flight enable, got %+v ok=%v", c, ok)
	}
}
