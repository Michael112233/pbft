package node

import (
	"math/big"
	"sort"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/crypto"
	"github.com/michael112233/pbft/transportpb"
)

// Checkpoint state transfer (config checkpoint_fetch, config/checkpointfetch.go).
//
// The gap it closes. A node X leaves a view before executing up to checkpoint k
// (it entered the view change early, e.g. the epoch aggregator at a generation
// switch), while the others go on, execute k, and stabilize it after sending their
// ViewChanges. The NewView's O-set then starts below k, and every other replica
// skips the O-set slots below its own low watermark (HandleNewView), so nobody
// re-votes slots <= k. X needs exactly those, and nothing retransmits them: it
// executes nothing until a later NewView carries a checkpoint >= k. In the n=7
// multi-scenario run this hit node 4 in 10% of NetworkDelay generations under the
// Periodic actions, for a whole 10 s tenure each, ending in a 500-cert ViewChange.
//
// The trigger is evidence plus an execution stall, not time alone:
//   - evidence: a stable checkpoint certificate (2f+1 matching CHECKPOINT votes,
//     each signature-checked in Deliver) for a seq above this node's last executed
//     slot. Every node gets every vote, so X already holds it.
//   - stall: lastExecuted has not moved for a full CheckpointFetchDelay window in
//     one installed view. A node that is merely behind keeps executing, because the
//     pipeline keeps it fed; a stranded node's lastExecuted freezes. A window that
//     overlaps a view change is not judged: nobody executes then, and the NewView
//     may carry the checkpoint itself (fastPathStablizeCheckpointviaVC).
//
// Known false positive, accepted: while the ProposalDelay leader slept 100 ms per
// proposal on its event loop (before proposal_delay_interval_ms made it a non-blocking
// spacing, branch timer-model), at ~10 proposals/s it could execute nothing for seconds
// while its followers executed its proposals and stabilized a checkpoint. It then
// fetched although it would have caught up (8 installs in 39 ProposalDelay generations
// in results/ckpt_fetch_n7, none in Healthy, NetworkDelay, NetworkDelayFCrash or
// Throttle). With its loop no longer blocked it may not happen any more; not yet
// re-measured. Left in on purpose: the install is a certified state (26 accounts, under
// 1 KB, ~3 ms), and a trigger tied to the stranding pattern (certificate complete at
// install, a re-proposed slot missing) would miss other ways of falling behind.
//
// PBFT proper fetches only once it learns of a stable checkpoint beyond its log
// window; it can wait that long because it retransmits lost messages, so a node
// inside its window catches up normally. This implementation does not retransmit,
// and by the time the others pass X's high watermark X has dropped messages it
// needs, so waiting for the window edge would leave it stuck again after the
// transfer.
//
// The requester installs a reply only for a (seq, digest) it holds a certificate
// for, and only if the state hashes to that digest, so it never trusts the peer's
// own proof. All of this runs on the event loop.

// armCheckpointFetchWindow starts a stall window at the current lastExecuted and
// view.
func (n *Node) armCheckpointFetchWindow() {
	n.armCheckpointFetchTimer(n.cfg.CheckpointFetchDelay())
}

// armCheckpointFetchTimer starts the timer for d, remembering the lastExecuted and
// view it started at; handleCheckpointFetchTimeout judges the stall against them.
func (n *Node) armCheckpointFetchTimer(d time.Duration) {
	n.ckptFetchArmedExecuted = n.lastExecuted
	n.ckptFetchArmedView = n.GetViewID()
	n.ckptFetchTimerCh = resetOneShotTimer(&n.ckptFetchTimer, d)
}

func (n *Node) stopCheckpointFetchTimer() {
	stopOneShotTimer(n.ckptFetchTimer)
	n.ckptFetchTimerCh = nil
}

// maybeArmCheckpointFetch starts a stall window when this node holds a
// certificate above its last executed slot and no window is running. Called on
// every checkpoint vote and after an install.
func (n *Node) maybeArmCheckpointFetch() {
	if !n.cfg.CheckpointFetch.Enabled || n.ckptFetchTimerCh != nil {
		return
	}
	if _, _, ok := n.checkpointManager.certifiedCheckpointAbove(n.lastExecuted); !ok {
		return
	}
	n.armCheckpointFetchWindow()
}

// handleCheckpointFetchTimeout ends a stall window: fetch if this node executed
// nothing during it, in one installed view, and still has a certificate above it.
func (n *Node) handleCheckpointFetchTimeout() {
	n.ckptFetchTimerCh = nil
	target, voters, ok := n.checkpointManager.certifiedCheckpointAbove(n.lastExecuted)
	if !ok {
		n.ckptFetchAttempts = 0 // caught up on its own
		return
	}
	if n.lastExecuted != n.ckptFetchArmedExecuted || n.viewChangeRunning || !n.GetViewID().Equal(n.ckptFetchArmedView) {
		// Executed something (behind but catching up), or the window overlapped a
		// view change: watch a fresh window.
		n.armCheckpointFetchWindow()
		return
	}
	n.sendCheckpointFetch(target, voters)
	// Retry, at the next voter, if no reply is installed within the retry timeout
	// (config.CheckpointFetchRetry: longer than a round trip, unlike the stall delay).
	n.armCheckpointFetchTimer(n.cfg.CheckpointFetchRetry())
}

func (n *Node) sendCheckpointFetch(target checkpoint, voters []int) {
	peers := make([]int, 0, len(voters))
	for _, v := range voters {
		if v != n.GetNodeID() {
			peers = append(peers, v)
		}
	}
	if len(peers) == 0 {
		return
	}
	// Offset by node id so stranded nodes spread over voters; rotate on retry.
	peer := peers[(n.GetNodeID()+n.ckptFetchAttempts)%len(peers)]
	n.ckptFetchAttempts++
	addr, ok := config.NodeAddr[peer]
	if !ok {
		n.log.Error("CKPT FETCH: no address for node %d", peer)
		return
	}
	msg := core.CheckpointFetchMsg{SeqNum: target.seq, Digest: target.digest, From: n.GetNodeID()}
	payload, err := marshalDeterministic(transportpb.CheckpointFetchToPB(msg))
	if err != nil {
		n.log.Error("CKPT FETCH: marshal failed: %v", err)
		return
	}
	signature := crypto.SignMessageEd25519(payload, n.encryptionKeyStore.GetPrivateKey())
	n.log.Info("CKPT FETCH sent seq=%d to=%d attempt=%d last_executed=%d stalled_for>=%v view=(%d,%d)",
		target.seq, peer, n.ckptFetchAttempts, n.lastExecuted, n.cfg.CheckpointFetchDelay(), n.GetViewID().Generation, n.GetViewID().Counter)
	go n.messageHub.Send(core.MsgCheckpointFetchMessage, addr, msg, signature)
}

// handleCheckpointFetch is the peer's side: send the asked-for checkpoint's state,
// or this node's newer stable checkpoint if it no longer holds that one.
// TODO(byzantine): no per-requester rate limit; a requester can make a node re-send
// its state on every request.
func (n *Node) handleCheckpointFetch(req core.CheckpointFetchMsg) {
	if !n.cfg.CheckpointFetch.Enabled {
		return
	}
	addr, ok := config.NodeAddr[req.From]
	if !ok {
		return
	}
	key, balances, ok := n.checkpointStateToServe(req)
	if !ok {
		n.log.Info("CKPT FETCH ignored from=%d seq=%d: no state for it, stable=%d", req.From, req.SeqNum, n.GetLastStableCheckpointSeq())
		return
	}
	msg := core.CheckpointStateMsg{SeqNum: key.seq, Digest: key.digest, From: n.GetNodeID(), Balances: balances}
	payload, err := marshalDeterministic(transportpb.CheckpointStateToPB(msg))
	if err != nil {
		n.log.Error("CKPT FETCH: marshal state failed: %v", err)
		return
	}
	signature := crypto.SignMessageEd25519(payload, n.encryptionKeyStore.GetPrivateKey())
	n.log.Info("CKPT FETCH served to=%d asked=%d sent=%d accounts=%d", req.From, req.SeqNum, key.seq, len(balances))
	// Checkpoint state maps are copies (CheckpointSnapshot) that nothing mutates,
	// so the send goroutine may read this one.
	go n.messageHub.Send(core.MsgCheckpointStateMessage, addr, msg, signature)
}

// checkpointStateToServe picks the state to answer req with: the asked-for
// checkpoint's if this node executed it, else its stable checkpoint if that is at
// least as new. ok is false when it has neither.
func (n *Node) checkpointStateToServe(req core.CheckpointFetchMsg) (checkpoint, map[string]*big.Int, bool) {
	key := checkpoint{seq: req.SeqNum, digest: req.Digest}
	if balances := n.checkpointManager.stateOf(key); balances != nil {
		return key, balances, true
	}
	stable, _, balances := n.GetLastStableCheckpointwithProofandBalances()
	if stable.seq < req.SeqNum || balances == nil {
		return checkpoint{}, nil, false
	}
	return stable, balances, true
}

// handleCheckpointState installs a fetched checkpoint state: only for a checkpoint
// this node holds a certificate for, only if the state hashes to its digest, and
// only if it is still ahead of this node.
func (n *Node) handleCheckpointState(msg core.CheckpointStateMsg) {
	if !n.cfg.CheckpointFetch.Enabled {
		return
	}
	if msg.SeqNum <= n.lastExecuted {
		// The node caught up before the reply arrived: this fetch was not needed.
		n.log.Info("CKPT FETCH stale from=%d seq=%d last_executed=%d", msg.From, msg.SeqNum, n.lastExecuted)
		return
	}
	key := checkpoint{seq: msg.SeqNum, digest: msg.Digest}
	proof := n.checkpointManager.certificate(key)
	if proof == nil {
		n.log.Warn("CKPT FETCH rejected from=%d seq=%d: no local certificate for it", msg.From, msg.SeqNum)
		return
	}
	if digestBalances(msg.Balances) != msg.Digest {
		n.log.Warn("CKPT FETCH rejected from=%d seq=%d: state does not hash to the checkpoint digest", msg.From, msg.SeqNum)
		return
	}
	before := n.lastExecuted
	start := time.Now()
	n.fastPathStablizeCheckpointviaVC(key, proof, msg.Balances, "fetch")
	n.log.Info("CKPT FETCH installed seq=%d from=%d last_executed %d->%d attempts=%d view=(%d,%d) vc_running=%t took=%v",
		key.seq, msg.From, before, n.lastExecuted, n.ckptFetchAttempts, n.GetViewID().Generation, n.GetViewID().Counter, n.viewChangeRunning, time.Since(start))
	n.stopCheckpointFetchTimer()
	n.ckptFetchAttempts = 0
	// Slots above the checkpoint this node already committed execute now. During a
	// view change execution waits for the NewView, as everywhere else.
	if !n.viewChangeRunning {
		n.exeLoop()
	}
	n.maybeArmCheckpointFetch()
}

// certifiedCheckpointAbove returns the highest checkpoint above seq that has 2f+1
// matching votes, with its voters in ascending id order.
func (cm *CheckpointManager) certifiedCheckpointAbove(seq int64) (checkpoint, []int, bool) {
	var best checkpoint
	var voters []int
	found := false
	for key, data := range cm.checkpoints {
		if key.seq <= seq || len(data.votes) < cm.node.QuorumSize() || (found && key.seq <= best.seq) {
			continue
		}
		best, found = key, true
		voters = voters[:0]
		for from := range data.votes {
			voters = append(voters, from)
		}
	}
	sort.Ints(voters)
	return best, voters, found
}

// certificate returns key's 2f+1 votes as a proof, or nil without a quorum.
func (cm *CheckpointManager) certificate(key checkpoint) []core.CheckpointMsgSig {
	data, ok := cm.checkpoints[key]
	if !ok || len(data.votes) < cm.node.QuorumSize() {
		return nil
	}
	proof := make([]core.CheckpointMsgSig, 0, len(data.votes))
	for _, vote := range data.votes {
		proof = append(proof, vote)
	}
	return proof
}

// stateOf returns key's state if this node executed it, else nil.
func (cm *CheckpointManager) stateOf(key checkpoint) map[string]*big.Int {
	return cm.checkpoints[key].balances
}
