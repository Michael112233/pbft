package client

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/logger"
)

// Throttle manager: the client-side half of the targeted proposal throttling
// experiment (see config/throttle.go and node/proposalgate.go). It simulates a
// network throttle applied to whichever replica is leading, not an adversary.
//
// The controller runs in the client, so its first notice of a new leader is the
// client's own accepted-leader quorum in HandleLeaderUpdate — never earlier.
// That bound is the point of the experiment: RoundRobin's schedule is public, so
// the next leader can be throttled before it takes over, while Election's cannot
// and each rotation pays a fresh preparation delay.
//
// It holds k = f throttle slots. A slot assigned to a node enables that node's
// proposal-rate gate (via the unary Deliver RPC) once a prep delay has elapsed,
// and keeps it enabled until the slot is retargeted or released. Retargeting
// costs a fresh prep; a slot is not reused until its disable is acknowledged.
//
// RoundRobin may compute successors from the public schedule and prepare them
// before takeover. Election is reactive only: it never predicts a leader
// (electionCandidateForView is deliberately not ported here) and acts solely on
// notices it has already received.
//
// The manager runs on one goroutine; all slot state is owned by it. Gate
// commands are sent on per-slot worker goroutines so a slow node never blocks
// other slots, and their acks come back on ackCh.

// leaderNotice is one accepted-leader observation handed to the manager.
type leaderNotice struct {
	view       core.ViewID
	leaderID   int
	observedAt time.Time
}

// slot is one throttle slot. Its phase is derived from these fields rather
// than stored, which keeps the reconcile logic in one place:
//
//	free      = targetNode == 0 && !enabled && !cmdOutstanding
//	preparing = targetNode != 0 && !enabled && now < readyAt
//	enabling  = targetNode != 0 && !enabled && now >= readyAt (command in flight)
//	active    = enabled && enabledNode == targetNode
//	disabling = enabled && enabledNode != targetNode (command in flight)
//
// Both active and disabling mean the node's gate is physically on, which is what
// holds() below answers; a strategy must use it rather than testing targetNode.
type slot struct {
	targetNode     int       // node this slot wants to throttle; 0 = none
	observedAt     time.Time // when targetNode was observed as leader (stale ranking)
	readyAt        time.Time // prep completion for targetNode
	enabled        bool      // gate believed on at enabledNode (per last ack)
	enabledNode    int       // node the gate is currently enabled on; 0 = none
	cmdOutstanding bool
	nextActionAt   time.Time // retry backoff floor

	// RoundRobin k=1 only: a scheduled mid-tenure release so the single slot
	// pre-prepares the successor and is active on it exactly at handoff.
	rrRetargetAt time.Time
	rrSuccessor  int
}

func (s *slot) occupied() bool {
	return s.enabled || s.targetNode != 0 || s.cmdOutstanding
}

// holds reports whether this slot is responsible for node's gate right now,
// either because it is aiming at it or because its gate is still on there.
//
// The second half matters: between a retarget away from node and the ack of the
// resulting disable, the slot has targetNode != node while node is still
// physically gated. A strategy that asked only about targetNode would treat node
// as uncovered and hand it to a *second* slot, which then pays a prep for a node
// that is already throttled — and, if the first slot's disable is retried past
// that prep, the stale disable lands afterwards and silently ungates the sitting
// leader for the rest of its tenure. Asking holds() instead lets the strategy
// restore targetNode and abort the release, which costs at most one disable RTT.
func (s *slot) holds(node int) bool {
	if node == 0 {
		return false
	}
	return s.targetNode == node || (s.enabled && s.enabledNode == node)
}

type gateCmd struct {
	slot   int
	node   int
	enable bool
}

type gateAck struct {
	cmd gateCmd
	err error
	rtt time.Duration
}

type throttleManager struct {
	cfg      *config.Config
	hub      *ClientMessageHub
	log      *logger.Logger
	nodeNum  int
	k        int
	strategy string
	prep     time.Duration
	tenure   time.Duration

	slots     []slot
	slotCmdCh []chan gateCmd
	ackCh     chan gateAck
	noticeCh  chan leaderNotice
	stop      chan struct{}
	done      chan struct{}

	out *jsonlWriter
}

const (
	throttleRetryBackoff  = 200 * time.Millisecond
	throttleReconcileTick = 50 * time.Millisecond
	throttleNoticeBuffer  = 64
)

func newThrottleManager(c *Client) *throttleManager {
	k := c.config.ThrottleSlots()
	m := &throttleManager{
		cfg:       c.config,
		hub:       c.messageHub,
		log:       c.log,
		nodeNum:   int(c.config.NodeNum),
		k:         k,
		strategy:  c.config.Throttle.Strategy,
		prep:      c.config.ThrottlePrep(),
		tenure:    c.config.ThrottleTenure(),
		slots:     make([]slot, k),
		slotCmdCh: make([]chan gateCmd, k),
		ackCh:     make(chan gateAck, 2*k),
		noticeCh:  make(chan leaderNotice, throttleNoticeBuffer),
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
		out:       newJSONLWriter("logs/throttle_manager.jsonl", c.log),
	}
	for i := 0; i < k; i++ {
		m.slotCmdCh[i] = make(chan gateCmd, 4)
	}
	return m
}

func (m *throttleManager) start() {
	for i := 0; i < m.k; i++ {
		go m.slotWorker(m.slotCmdCh[i])
	}
	go m.run()
}

func (m *throttleManager) stopAndWait() {
	close(m.stop)
	<-m.done
	m.out.close()
}

// notify is the non-blocking entry point the client calls under no lock. A full
// channel drops the notice (logged once by the caller), so the manager can
// never stall consensus or the client.
func (m *throttleManager) notify(n leaderNotice) {
	select {
	case m.noticeCh <- n:
	default:
		m.log.Info("THROTTLE: notice channel full, dropped leader %d view (%d,%d)", n.leaderID, n.view.Generation, n.view.Counter)
	}
}

// 50ms reconcile funnel everything
func (m *throttleManager) run() {
	defer close(m.done)
	m.log.Info("THROTTLE: manager started strategy=%s slots=%d prep=%v tenure=%v", m.strategy, m.k, m.prep, m.tenure)

	ticker := time.NewTicker(throttleReconcileTick)
	defer ticker.Stop()

	for {
		select {
		case <-m.stop:
			for i := range m.slotCmdCh {
				close(m.slotCmdCh[i])
			}
			return
		case n := <-m.noticeCh:
			m.onNotice(n)
			m.reconcile(time.Now())
		case ack := <-m.ackCh:
			m.onAck(ack)
			m.reconcile(time.Now())
		case <-ticker.C:
			m.reconcile(time.Now())
		}
	}
}

func (m *throttleManager) onNotice(n leaderNotice) {
	lag := time.Since(n.observedAt)
	// holds, not targetNode: this must record the same judgement the strategies
	// below act on, or the analysis undercounts free coverage.
	alreadyTargeted := false
	for i := range m.slots {
		if m.slots[i].holds(n.leaderID) {
			alreadyTargeted = true
			break
		}
	}
	m.out.write(map[string]any{
		"t":                time.Now().UnixNano(),
		"kind":             "notice",
		"gen":              n.view.Generation,
		"counter":          n.view.Counter,
		"leader":           n.leaderID,
		"observed_at":      n.observedAt.UnixNano(),
		"lag_ms":           lag.Milliseconds(),
		"already_targeted": alreadyTargeted,
	})

	switch m.strategy {
	case config.ThrottleStrategyRoundRobin:
		m.onNoticeRoundRobin(n)
	case config.ThrottleStrategyElection:
		m.onNoticeElection(n)
	}
}

func (m *throttleManager) onNoticeRoundRobin(n leaderNotice) {
	now := time.Now()
	if m.k == 1 {
		s := &m.slots[0]
		if s.targetNode != n.leaderID {
			m.retarget(0, n.leaderID, n.observedAt, now)
		}
		// Release the current leader prep-ahead of handoff so the slot is active
		// on the successor exactly when it installs (steady-state ~80% coverage).
		m.slots[0].rrSuccessor = primaryForView(n.view.Counter+1, m.nodeNum)
		m.slots[0].rrRetargetAt = n.observedAt.Add(m.tenure - m.prep)
		return
	}

	// k >= 2: hold a sliding window {counter .. counter+k-1}, so the sitting
	// leader keeps a dedicated slot for its whole tenure while the next
	// leaders are prepared ahead (steady-state 100% on the sitting leader).
	desired := make([]int, 0, m.k)
	for i := 0; i < m.k; i++ {
		desired = append(desired, primaryForView(n.view.Counter+uint64(i), m.nodeNum))
	}
	// in prr start with node 1 , first leader update from node 2
	// which then fills desired with 2 and 3 if k=2
	// node 2 can only be throttled for 8s as prep time but node 3 and onward fully throttle
	m.assignToDesired(desired, n.observedAt, now)
}

// onNoticeElection is the reactive rule: if a slot already holds this leader,
// keep camping on it; otherwise take a free slot if there is one, and evict the
// stalest if there is not. Slots are free only for the first notice or two of a
// run (at counter 2 nothing has been targeted yet), so in steady state every
// notice is an eviction — which is why Election pays a prep inside every tenure.
func (m *throttleManager) onNoticeElection(n leaderNotice) {
	now := time.Now()
	for i := range m.slots {
		if m.slots[i].holds(n.leaderID) {
			// Election can draw the same leader again: keep the gate where it is
			// and pay no new prep. Restoring targetNode also aborts a release
			// this slot had already decided on (see holds). readyAt is
			// deliberately left alone: nothing is being re-aimed, so there is no
			// preparation to redo. Refresh observedAt so the slot is not evicted
			// as stale.
			m.slots[i].targetNode = n.leaderID
			m.slots[i].observedAt = n.observedAt
			return
		}
	}
	victim := m.pickEvictable()
	m.retarget(victim, n.leaderID, n.observedAt, now)
}

// assignToDesired keeps slots that already hold a wanted node and retargets the
// rest to the desired nodes not yet covered. Used by the k>=2 RoundRobin window.
//
// A slot counts as covering a node when it holds() it, so a slot still enabled
// on a wanted node is kept rather than being treated as spare — restoring its
// targetNode aborts a release decided at an earlier notice. Without that, the
// node would be handed to a second slot while the first slot's stale disable was
// still outstanding. RoundRobin reaches that state far less often than Election
// (a node leaves the window for ~n-1 counters before it can return, which under
// the Periodic trigger is tens of seconds), but the rule belongs in both
// strategies: the only intended difference between them is whether the schedule
// can be predicted.
func (m *throttleManager) assignToDesired(desired []int, observedAt, now time.Time) {
	wanted := make(map[int]bool, len(desired))
	for _, node := range desired {
		wanted[node] = true
	}
	covered := make(map[int]bool, len(desired))
	free := make([]int, 0, m.k)
	for i := range m.slots {
		if held := m.heldWanted(i, wanted); held != 0 {
			m.slots[i].targetNode = held
			covered[held] = true
		} else {
			free = append(free, i)
		}
	}
	fi := 0
	for _, node := range desired {
		if covered[node] {
			continue
		}
		if fi >= len(free) {
			break
		}
		m.retarget(free[fi], node, observedAt, now)
		fi++
	}
}

// heldWanted returns the node slot i holds that is in wanted, or 0 if it holds
// none. targetNode is preferred over enabledNode so an in-flight retarget
// between two wanted nodes is not undone.
func (m *throttleManager) heldWanted(i int, wanted map[int]bool) int {
	s := &m.slots[i]
	if s.targetNode != 0 && wanted[s.targetNode] {
		return s.targetNode
	}
	if s.enabled && wanted[s.enabledNode] {
		return s.enabledNode
	}
	return 0
}

// pickEvictable returns a free slot if one exists, else the occupied slot whose
// target was observed longest ago (the stalest). A fixed, documented rule.
func (m *throttleManager) pickEvictable() int {
	for i := range m.slots {
		if !m.slots[i].occupied() {
			return i
		}
	} // only run at start then only slots free every other time slot occupied
	stalest := 0
	for i := range m.slots {
		if m.slots[i].observedAt.Before(m.slots[stalest].observedAt) {
			stalest = i
		}
	}
	return stalest
}

// retarget points a slot at a new node. The reconcile step disables any node it
// is still enabled on, then enables the new node once prep elapses.
func (m *throttleManager) retarget(i, node int, observedAt, now time.Time) {
	s := &m.slots[i]
	from := s.targetNode
	s.targetNode = node
	s.observedAt = observedAt
	s.readyAt = now.Add(m.prep)
	s.rrRetargetAt = time.Time{} // for k=1 rr
	s.rrSuccessor = 0
	m.out.write(map[string]any{
		"t":    time.Now().UnixNano(),
		"kind": "retarget",
		"slot": i,
		"from": from,
		"to":   node,
	})
}

// onnotice and on ack change intent
// reconcile manage slot state
// reconcile tick help with prep expiry and retry
func (m *throttleManager) reconcile(now time.Time) {
	for i := range m.slots {
		s := &m.slots[i]

		// RoundRobin k=1 scheduled release: hand the slot to the successor near
		// the end of the current tenure.
		if !s.rrRetargetAt.IsZero() && now.After(s.rrRetargetAt) {
			succ := s.rrSuccessor
			s.rrRetargetAt = time.Time{}
			s.rrSuccessor = 0
			m.retarget(i, succ, now, now)
		}

		if s.cmdOutstanding || now.Before(s.nextActionAt) {
			continue
		}
		// Disable a stale node this slot is no longer targeting.
		if s.enabled && s.enabledNode != s.targetNode {
			m.issue(i, s.enabledNode, false)
			continue
		}
		// Enable the target once prep has elapsed.
		if s.targetNode != 0 && !s.enabled && !now.Before(s.readyAt) {
			m.issue(i, s.targetNode, true)
			continue
		}
	}
}

// cmd outstanding, enable and enabled most critical and only touched by reconcile or ack
// never sends cmd on slot if alread outstanding

func (m *throttleManager) issue(i, node int, enable bool) {
	m.slots[i].cmdOutstanding = true
	select {
	case m.slotCmdCh[i] <- gateCmd{slot: i, node: node, enable: enable}:
	case <-m.stop:
		m.slots[i].cmdOutstanding = false
	}
}

func (m *throttleManager) onAck(ack gateAck) {
	s := &m.slots[ack.cmd.slot]
	s.cmdOutstanding = false

	ok := ack.err == nil
	m.out.write(map[string]any{
		"t":      time.Now().UnixNano(),
		"kind":   "command",
		"slot":   ack.cmd.slot,
		"node":   ack.cmd.node,
		"enable": ack.cmd.enable,
		"ok":     ok,
		"rtt_ms": ack.rtt.Milliseconds(),
	})
	if !ok {
		// Keep the slot occupied and retry on the next tick (a node busy in a
		// multi-MiB NewView verify can miss the 1s RPC deadline).
		s.nextActionAt = time.Now().Add(throttleRetryBackoff)
		m.log.Info("THROTTLE: gate %v on node %d failed, will retry: %v", ack.cmd.enable, ack.cmd.node, ack.err)
		return
	}
	// if new leader notice and want to enable first make sure disable is acked till then there is cmd outstanding and it keep retrying with backoff
	if ack.cmd.enable {
		s.enabled = true
		s.enabledNode = ack.cmd.node
	} else {
		s.enabled = false
		s.enabledNode = 0
	}
}

func (m *throttleManager) slotWorker(ch <-chan gateCmd) {
	for cmd := range ch {
		event := core.EventMsg{EventType: core.EventTypeThrottleGateOff}
		if cmd.enable {
			event.EventType = core.EventTypeThrottleGateOn
		}
		addr := config.NodeAddr[cmd.node]
		start := time.Now()
		err := m.hub.SendEventWithAck(addr, event)
		ack := gateAck{cmd: cmd, err: err, rtt: time.Since(start)}
		select {
		case m.ackCh <- ack:
		case <-m.stop:
			return
		}
	}
}

// primaryForView mirrors node/roundrobin.go: the round-robin schedule is public,
// so the controller may compute successors from it.
func primaryForView(counter uint64, nodeNum int) int {
	if counter == 0 {
		return 1
	}
	return int((counter-1)%uint64(nodeNum)) + 1
}

// jsonlWriter appends newline-delimited JSON. Written only from the manager
// goroutine (or the client's timeline goroutine), so it needs no locking.
type jsonlWriter struct {
	f   *os.File
	log *logger.Logger
}

func newJSONLWriter(path string, log *logger.Logger) *jsonlWriter {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		log.Error("THROTTLE: cannot open %s: %v", path, err)
		return &jsonlWriter{log: log}
	}
	return &jsonlWriter{f: f, log: log}
}

func (w *jsonlWriter) write(rec map[string]any) {
	if w == nil || w.f == nil {
		return
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return
	}
	if _, err := fmt.Fprintln(w.f, string(b)); err != nil {
		w.log.Error("THROTTLE: write failed: %v", err)
	}
}

func (w *jsonlWriter) close() {
	if w != nil && w.f != nil {
		_ = w.f.Close()
	}
}
