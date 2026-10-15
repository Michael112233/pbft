package node

import (
	"bytes"
	"math/big"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/crypto"
	"github.com/michael112233/pbft/transportpb"
)

func (n *Node) incrementCounter() core.ViewID {
	forView := n.GetForViewID()
	return core.ViewID{Generation: forView.Generation, Counter: forView.Counter + 1}
}

// path to inc gen learning decision normal flow , get aggregate and then learning decision
// from vc we catchup to higher gen
// from nv we catchup to higher gen
// if catchuping we may or maynot have received agg or learning decison yet
// if g-1 there we can apply it it will make matrix of g and row of g

func (n *Node) incrementGeneration(action core.Action) core.ViewID {
	forView := n.GetForViewID()
	// Leaving forView.Generation: if its learning data never went out (caught up
	// before its aggregate arrived), send placeholder data so the agent stays in
	// sequence. Uses the action of the generation being left, before it changes.
	n.fillDeciderGap(forView.Generation, n.GetCurrAction())
	n.resetEpochTimer(forView.Generation + 1)
	n.SetCurrAction(action)
	n.SwitchTriggerMode(action.TriggerMode, forView.Generation+1) // its is some what parallel state with curr action both update together onn generation update
	n.epochManager.GCBelow(forView.Generation + 1)
	// Leaving forView.Generation: apply its stored aggregate now, so rows and
	// candidates for the next generation appear together with the switch. Runs
	// before the callers' maybeAdoptAwareRows / enterViewChange.
	n.applyPendingAwareAggregate(forView.Generation)
	return core.ViewID{Generation: forView.Generation + 1, Counter: 1}
}

// pruneViewChangeState drops per-view bookkeeping for every view <= installed once
// installed is the node's viewID. Safe because views only move forward and
// HandleViewChangeRoundRobin discards any ViewChange for a view <= viewID on arrival,
// so nothing can re-populate or read those entries again: verifyNewView / newview /
// maybeHandleViewChangeQuorum only look up the view being moved to, which is > installed
// until the moment it is installed. Entries for views above installed are kept, since
// f+1 amplification and an in-flight view change still need them (a ViewChange can be
// logged for a higher view while we are still in an older one).
//
// The map only ever holds views in flight, so the scan is over a handful of keys.
func (n *Node) pruneViewChangeState(installed core.ViewID) {
	for view := range n.viewChangeMsgsLog {
		if view.LessThanOrEqual(installed) {
			delete(n.viewChangeMsgsLog, view)
		}
	}
	n.pruneNewViewRefState(func(view core.ViewID) bool { return view.LessThanOrEqual(installed) })
	// only written, never read (leaderForView is hardcoded); kept as a record of the
	// current view's leader only
	for view := range n.leaderIdForView {
		if view.LessThan(installed) {
			delete(n.leaderIdForView, view)
		}
	}
}

// pruneBelowForView drops per-view bookkeeping for every view below forView, the
// view this node is now trying to reach. Nothing reads those entries again:
// forViewID only moves forward (SetForViewID is its only setter), the 2f+1 quorum
// and newview look up forView itself, f+1 amplification counts views above it,
// HandleNewView rejects a NewView below forView before verifyNewView reads the
// cache, and HandleViewChangeRoundRobin no longer stores a ViewChange below it.
//
// pruneViewChangeState only runs when a view is installed, so without this a
// cascade that never installs one keeps every ViewChange of every view it passes
// through: Election under NetworkDelay at n=7 grew ~33 GB/h per node and OOM-killed
// a node in the PFE multi-scenario run. RoundRobin was only saved by the primary
// installing locally in newview every nodeNum views.
func (n *Node) pruneBelowForView(forView core.ViewID) {
	for view := range n.viewChangeMsgsLog {
		if view.LessThan(forView) {
			delete(n.viewChangeMsgsLog, view)
		}
	}
	n.pruneNewViewRefState(func(view core.ViewID) bool { return view.LessThan(forView) })
	n.electionManager.pruneBelow(forView)
	n.pruneBufferedBelow(forView)
}

func (n *Node) enterViewChange(forView core.ViewID) {
	n.stopViewTimers()
	n.stopPerfTimer()
	// if n.lastExecuted >= 11500 {
	// 	n.log.Info("Node %d has executed %d requests, stopping further view changes", n.GetNodeID(), n.lastExecuted)
	// 	return
	// }
	n.viewChangeRunning = true
	n.SetForViewID(forView)
	// assuming generation slow so only plus one will later fix other paths
	view := n.GetViewID()
	// n.forView = n.forView + 1
	action := n.GetCurrAction()

	n.VC(forView, view, action)
}

func (n *Node) createVCContent(stableCheckpointSeq int64, forView, view core.ViewID) map[int64]*core.PreparedCert {
	preparedCerts := make(map[int64]*core.PreparedCert)
	lastStableCheckpointSeq := n.GetLastStableCheckpointSeq()
	// The P-set carries the prepared certificate for every seq above the last stable
	// checkpoint that ever became prepared at this replica, at whatever view it
	// prepared in. preparedProof is captured in tryAdvancePrepare and is only cleared
	// by GCLog, so it survives any number of intervening view changes (including ones
	// where this replica installed a new view locally as primary and then failed).
	iterations := 0
	for seq := lastStableCheckpointSeq + 1; seq <= n.consensusLog.maxSeqNum; seq++ {
		slot, exists := n.consensusLog.GetLogEntry(seq)
		if !exists || slot.preparedProof == nil {
			continue
		}
		preparedCerts[seq] = slot.preparedProof
		iterations++
	}
	n.log.Info("createVCContent carried %d prepared certs; forView (%d, %d), n.view (%d, %d)", iterations, forView.Generation, forView.Counter, view.Generation, view.Counter)
	return preparedCerts
}

// warnRetainedDurableSlots logs any slot that RemoveLogEntriesAboveSeq kept above maxSeq
// because it still carried a prepared certificate. committedAbove should always be
// empty: a committed request is prepared at f+1 honest replicas, so any 2f+1 ViewChange
// messages carry its prepared cert and maxS covers it. A hit there means the P-sets
// feeding createO were wrong, so surface it loudly.
func (n *Node) warnRetainedDurableSlots(retained, committedAbove []int64, maxSeq int64, path string, view core.ViewID) {
	for _, seq := range committedAbove {
		n.log.Error("new-view %s: COMMITTED slot seq %d was above maxSeq %d in view (%d, %d)", path, seq, maxSeq, view.Generation, view.Counter)
	}
	for _, seq := range retained {
		slot, ok := n.consensusLog.GetLogEntry(seq)
		if !ok {
			continue
		}
		n.log.Warn("new-view %s: prepared slot seq %d (preparedView %d) retained above maxSeq %d in view (%d, %d)", path, seq, slot.preparedView, maxSeq, view.Generation, view.Counter)
	}
}

// verifyVC checks a ViewChange's prepared certs. Most prepares in a peer's cert are
// ones this node already verified when they arrived, so certsLeftToVerify first
// drops every byte-identical copy of one (on the event loop: it reads
// consensusLog), and only the rest are signature-checked.
func (n *Node) verifyVC(vc core.ViewChangeMsg) bool {
	start := time.Now()
	checks, cached, ok := n.certsLeftToVerify(vc.PreparedCerts)
	if !ok {
		return false
	}
	var verified bool
	if n.cfg.ParallelWorkers {
		verified = n.verifyCertChecksParallel(checks)
	} else {
		verified = n.verifyCertChecks(checks)
	}
	toVerify := 0
	for _, c := range checks {
		toVerify += len(c.prepares)
	}
	n.log.Info("VC CERTS from=%d view=(%d,%d) certs=%d prepares cached=%d to_verify=%d ok=%v took=%v",
		vc.From, vc.ViewNumber.Generation, vc.ViewNumber.Counter, len(vc.PreparedCerts), cached, toVerify, verified, time.Since(start))
	return verified
}

// certCheck is what is left to verify of one prepared cert after the cache pass.
type certCheck struct {
	seq      int64
	view     core.ViewID
	digest   [32]byte
	need     int                        // valid prepares still needed for 2f
	prepares map[int]core.PrepareMsgSig // the cert's prepares this node has not verified before
}

// certsLeftToVerify does the per-cert checks that need no signature (cert present,
// map key matches its pre-prepare's seq, enough prepares) and counts every prepare
// this node already verified toward the cert's 2f. It returns the certs that still
// need signature checks, how many prepares were skipped, and false if a cert is
// malformed.
func (n *Node) certsLeftToVerify(certs map[int64]*core.PreparedCert) ([]certCheck, int, bool) {
	required := n.QuorumSize() - 1
	var checks []certCheck
	cached := 0
	for certSeq, cert := range certs {
		if cert == nil {
			return nil, cached, false
		}
		ok, view, seq, digest := n.verifyPreprepare(cert.PreprepareMsg)
		if !ok {
			return nil, cached, false
		}
		if seq != certSeq {
			n.log.Error("prepared cert seq mismatch: map key=%d preprepare seq=%d", certSeq, seq)
			return nil, cached, false
		}
		if required <= 0 {
			continue
		}
		if len(cert.PrepareLog) < required {
			n.log.Error("prepared cert seq %d has %d prepares, need %d", seq, len(cert.PrepareLog), required)
			return nil, cached, false
		}
		known := 0
		var rest map[int]core.PrepareMsgSig
		for from, p := range cert.PrepareLog {
			m := p.PrepareMsg
			if m.From == from && m.View.Equal(view) && m.SeqNum == seq && m.Digest == digest && n.prepareAlreadyVerified(p) {
				known++
				continue
			}
			if rest == nil {
				rest = make(map[int]core.PrepareMsgSig, len(cert.PrepareLog))
			}
			rest[from] = p
		}
		cached += known
		if known >= required {
			continue
		}
		checks = append(checks, certCheck{seq: seq, view: view, digest: digest, need: required - known, prepares: rest})
	}
	return checks, cached, true
}

// prepareAlreadyVerified reports whether this node holds exactly this prepare (same
// message, same signature bytes) as one it already verified. Every prepare in
// slot.prepares is this node's own or came through Deliver, which checked its
// signature against its sender and that the sender is PrepareMsg.From; preparedProof
// is built from slot.prepares. Ed25519 verification depends only on key, message and
// signature, so a byte-identical copy would verify again. The current-view prepares
// cover certs from the view that just failed; preparedProof survives a new-view
// install, for certs from the view this node last prepared the seq in.
func (n *Node) prepareAlreadyVerified(p core.PrepareMsgSig) bool {
	slot, ok := n.consensusLog.GetLogEntry(p.PrepareMsg.SeqNum)
	if !ok {
		return false
	}
	if slot.view.Equal(p.PrepareMsg.View) {
		if held, ok := slot.prepares[p.PrepareMsg.From]; ok && held.PrepareMsg == p.PrepareMsg && bytes.Equal(held.Signature, p.Signature) {
			return true
		}
	}
	if cert := slot.preparedProof; cert != nil && slot.preparedView.Equal(p.PrepareMsg.View) {
		if held, ok := cert.PrepareLog[p.PrepareMsg.From]; ok && held.PrepareMsg == p.PrepareMsg && bytes.Equal(held.Signature, p.Signature) {
			return true
		}
	}
	return false
}

func (n *Node) verifyPrepareLog(prepareLog map[int]core.PrepareMsgSig, view core.ViewID, seq int64, digest [32]byte) bool {
	return n.verifyPrepareLogNeed(prepareLog, view, seq, digest, n.QuorumSize()-1)
}

// verifyPrepareLogNeed checks signatures in prepareLog until need of them are valid
// prepares for (view, seq, digest) from the node their map key names.
func (n *Node) verifyPrepareLogNeed(prepareLog map[int]core.PrepareMsgSig, view core.ViewID, seq int64, digest [32]byte, need int) bool {
	if need <= 0 {
		return true
	}
	if len(prepareLog) < need {
		n.log.Error("not enough prepare messages to verify: have=%d need=%d view=(%d,%d) seq=%d", len(prepareLog), need, view.Generation, view.Counter, seq)
		return false
	}

	validCount := 0
	for from, prepareMsgSig := range prepareLog {
		if prepareMsgSig.PrepareMsg == (core.PrepareMsg{}) {
			continue
		}

		prepareMsg := prepareMsgSig.PrepareMsg
		if prepareMsg.View.NotEqual(view) || prepareMsg.SeqNum != seq || prepareMsg.Digest != digest {
			continue
		}
		if prepareMsg.From != from {
			n.log.Error("prepare sender mismatch: map key=%d payload from=%d", from, prepareMsg.From)
			continue
		}

		senderPubKey, exists := n.encryptionKeyStore.GetPublicKey(from)
		if !exists {
			n.log.Error("public key not found for prepare sender node ID: %d", from)
			continue
		}

		payloadBytes, err := marshalDeterministic(transportpb.PrepareToPB(prepareMsg))
		if err != nil {
			n.log.Error("prepare payload marshal failed: err=%v", err)
			continue
		}
		if !crypto.VerifySignatureEd25519(payloadBytes, prepareMsgSig.Signature, senderPubKey) {
			n.log.Error("prepare signature verification failed for node ID: %d", from)
			continue
		}

		validCount++
		if validCount >= need {
			return true
		}
	}

	n.log.Error("not enough valid prepare messages: valid=%d need=%d view=(%d,%d) seq=%d", validCount, need, view.Generation, view.Counter, seq)
	return false
}

func (n *Node) verifyCertChecks(checks []certCheck) bool {
	for _, c := range checks {
		if !n.verifyPrepareLogNeed(c.prepares, c.view, c.seq, c.digest, c.need) {
			return false
		}
	}
	return true
}

// verifyCertChecksParallel is the parallel counterpart to verifyCertChecks. Each
// check only reads its own prepares, the read-only public keys and the logger, so
// the checks can be fanned out across a worker pool.
func (n *Node) verifyCertChecksParallel(checks []certCheck) bool {
	if len(checks) == 0 {
		return true
	}

	workers := runtime.NumCPU()
	if workers > len(checks) {
		workers = len(checks)
	}
	if workers < 1 {
		workers = 1
	}
	chunkSize := (len(checks) + workers - 1) / workers
	// ceil(a/b) = (a + b - 1) / b
	// 10/3 gives 3 but we want 4
	// if a evenly divides by b then adding b-1 doesnt change quotient

	var wg sync.WaitGroup
	var failed atomic.Bool

	for start := 0; start < len(checks); start += chunkSize {
		end := start + chunkSize
		if end > len(checks) {
			end = len(checks)
		}
		wg.Add(1)
		go func(chunk []certCheck) {
			defer wg.Done()
			for _, c := range chunk {
				if failed.Load() {
					return
				}
				if !n.verifyPrepareLogNeed(c.prepares, c.view, c.seq, c.digest, c.need) {
					failed.Store(true)
					return
				}
			}
		}(checks[start:end])
	}
	wg.Wait()

	return !failed.Load()
}

func (n *Node) verifyPreprepare(preprepareMsg core.PreprepareMsgSig) (bool, core.ViewID, int64, [32]byte) {
	view := preprepareMsg.PreprepareMsgMini.View
	seq := preprepareMsg.PreprepareMsgMini.SeqNum
	digest := preprepareMsg.PreprepareMsgMini.DigestClientMsg
	return true, view, seq, digest // need a fix when leader from election

	//code at bottom needs update for viewid style

	// from := n.leaderForView(view)
	// if from == 0 {
	// 	n.log.Error("leader not found for preprepare verification: view=%d", view)
	// 	return false, 0, 0, [32]byte{}
	// }

	// senderPubKey, exists := n.encryptionKeyStore.GetPublicKey(from)
	// if !exists {
	// 	n.log.Error("public key not found for preprepare sender node ID: %d", from)
	// 	return false, 0, 0, [32]byte{}
	// }
	// payload := preprepareSignPayload(preprepareMsg.PreprepareMsgMini.View, preprepareMsg.PreprepareMsgMini.SeqNum, preprepareMsg.PreprepareMsgMini.DigestClientMsg[:])
	// // payload := &transportpb.PreprepareSignPayload{
	// // 	View:            view,
	// // 	SeqNum:          seq,
	// // 	DigestClientMsg: digest[:],
	// // }
	// payloadBytes, err := marshalDeterministic(payload)
	// if err != nil {
	// 	n.log.Error("preprepare payload marshal failed: err=%v", err)
	// 	return false, 0, 0, [32]byte{}
	// }

	// if !crypto.VerifySignatureEd25519(payloadBytes, preprepareMsg.Signature, senderPubKey) {
	// 	n.log.Error("preprepare signature verification failed for node ID: %d", from)
	// 	return false, 0, 0, [32]byte{}
	// }
	// return true, view, seq, digest
}

// verifyOSet checks the primary's O-set against the one recomputed from the same
// ViewChanges: every entry must match, and none may be missing or repeated. A
// primary that left a prepared seq out would leave that slot with no pre-prepare in
// the new view, so it could never commit.
func verifyOSet(Ocreated map[int64]core.PreprepareMsgSig, Oreceived []core.PreprepareMsgSig) bool {
	if len(Oreceived) != len(Ocreated) {
		return false
	}
	seen := make(map[int64]struct{}, len(Oreceived))
	for _, preprepareMsgSig := range Oreceived {
		if _, dup := seen[preprepareMsgSig.PreprepareMsgMini.SeqNum]; dup {
			return false
		}
		seen[preprepareMsgSig.PreprepareMsgMini.SeqNum] = struct{}{}
		if o, exists := Ocreated[preprepareMsgSig.PreprepareMsgMini.SeqNum]; !exists {
			return false
		} else {
			if o.PreprepareMsgMini.View != preprepareMsgSig.PreprepareMsgMini.View ||
				o.PreprepareMsgMini.SeqNum != preprepareMsgSig.PreprepareMsgMini.SeqNum ||
				o.PreprepareMsgMini.DigestClientMsg != preprepareMsgSig.PreprepareMsgMini.DigestClientMsg {
				return false
			}
		}
	}
	return true
}

func (n *Node) leaderForView(view core.ViewID) int {
	return 1
	// if view <= 0 {
	// 	return 0
	// }
	// if leaderID, exists := n.leaderIdForView[view]; exists {
	// 	return leaderID
	// }
	// if n.vcType == core.VCTypeRoundRobin {
	// 	return n.primaryForView(view, -1)
	// }
	// return 0
}

// in election new view timer start when vote in 100ms leader collect vote msgs and process his new view and sends to replica before the new view timer expire

func (n *Node) newview() {
	timeStart := time.Now()
	oldView := n.GetViewID()

	view := n.GetForViewID()
	n.SetViewID(view)
	n.leaderId = n.GetNodeID()
	n.leaderIdForView[view] = n.leaderId
	n.viewChangeRunning = false

	n.log.Info("Became leader for new view (%d,%d) and my id is %d", view.Generation, view.Counter, n.GetNodeID())

	O, maxSeq, latestStableCheckpoint, checkpointProof, checkpointBalances := n.createO(n.viewChangeMsgsLog[view], view, oldView)
	mylatestStableCheckpointSeq := n.GetLastStableCheckpointSeq()
	if latestStableCheckpoint.seq > mylatestStableCheckpointSeq {
		n.log.Debug("stable checkpoint %d ahead of my last stable checkpoint %d; stabilizing checkpoint at new-view primary", latestStableCheckpoint.seq, mylatestStableCheckpointSeq)
		n.fastPathStablizeCheckpointviaVC(latestStableCheckpoint, checkpointProof, checkpointBalances, "primary")

	} else if latestStableCheckpoint.seq < mylatestStableCheckpointSeq {
		n.log.Debug("my stable checkpoint %d is ahead of the latest stable checkpoint %d in new view primary", mylatestStableCheckpointSeq, latestStableCheckpoint.seq)
		// if maxSeq < mylatestStableCheckpointSeq {
		// 	n.log.Error("my stable checkpoint %d is ahead of the latest stable checkpoint %d and maxSeq %d is less than my stable checkpoint in new view primary", mylatestStableCheckpointSeq, latestStableCheckpoint.seq, maxSeq)
		// }
		n.assert(maxSeq >= mylatestStableCheckpointSeq, "maxseq %d is less than my stable checkpoint %d in new view primary", maxSeq, mylatestStableCheckpointSeq)
		// if maxseq eq lateststable checkpoint then no change needed to log
	}
	n.assert(n.consensusLog.low == n.GetLastStableCheckpointSeq()+1, "consensus log low %d is not equal to last stable checkpoint seq + 1 %d in new view primary", n.consensusLog.low, n.GetLastStableCheckpointSeq()+1)
	n.assert(maxSeq <= n.consensusLog.high, "maxseq %d is greater than consensus log high %d in new view primary", maxSeq, n.consensusLog.high)
	for _, preprepareMsg := range O {
		if preprepareMsg.PreprepareMsgMini.SeqNum < n.consensusLog.low {
			// n.log.Debug("preprepare seq %d is less than consensus log low %d, skipping", preprepareMsg.PreprepareMsgMini.SeqNum, n.consensusLog.low)
			continue
		}
		slot := n.consensusLog.ResetPerViewState(preprepareMsg.PreprepareMsgMini.SeqNum, view)
		n.slotPreprepare(slot, &preprepareMsg.PreprepareMsgMini, preprepareMsg.Signature, true)
		slot.view = view
		// check if after all the flow if order of actual message same as digest of individual client messages in preprepare message
		// without carry_state the O-set has no bodies; they are already in the pool from HandlePrePrepare
		if n.cfg.CarryState {
			n.pool.AddBatch(preprepareMsg.ActualMsg, preprepareMsg.PreprepareMsgMini.DigestIndividualClientMsgs, preprepareMsg.PreprepareMsgMini.SeqNum, view)
		}

	}
	retained, committedAbove := n.consensusLog.RemoveLogEntriesAboveSeq(maxSeq, view)
	n.warnRetainedDurableSlots(retained, committedAbove, maxSeq, "primary", view)
	// max seq number in log is prepareseq number in all nodes
	n.sequenceNumber = maxSeq

	n.pilotTraceStart(maxSeq, view)
	maxRecentThroughput := n.newviewUpdatePerf(maxSeq, view)

	newViewMsg := core.NewViewMsg{
		NewViewNumber: view,
		Action:        n.GetCurrAction(),
		From:          n.GetNodeID(),
		PreprepareLog: O,
		// the same ViewChanges createO used, named by digest; kept for fetches
		ViewChangeRefs: n.recordNewViewSent(view, n.viewChangeMsgsLog[view]),
		Throughput:     maxRecentThroughput,
		AwareRows:      n.awareRowsForMessage(n.GetCurrAction()),
	}
	payloadBytes, err := marshalDeterministic(transportpb.NewViewToPB(newViewMsg))
	if err != nil {
		n.log.Error("Failed to marshal NewView message for signing: %v", err)
	}
	signature := crypto.SignMessageEd25519(payloadBytes, n.encryptionKeyStore.GetPrivateKey())
	// size from the bytes just signed; proto.Size would walk the message again
	n.log.Info("NEWVIEW sent view=(%d,%d) refs=%d o_set=%d size_bytes=%d size_mib=%.3f",
		view.Generation, view.Counter, len(newViewMsg.ViewChangeRefs), len(O), len(payloadBytes), float64(len(payloadBytes))/(1024*1024))
	n.asyncBroadCast(core.MsgNewViewMessage, newViewMsg, signature)
	n.pruneViewChangeState(view)
	duration := time.Since(timeStart)
	n.log.Info("Time taken to process new view is %d ms", duration.Milliseconds())

	// n.acceptNewViewTimers()
	// shouldnt have anything to replay as not released event loop

	n.replayBufferedMessagesForView(view)

	// maxseq == ladtStableCheckpoint.seq means no suffix, O len zero

	// loss in this queue is fine ig
	// n.acceptNewViewTimers()
	n.acceptNewViewTimersLeader() // experimental dont start progress timer on leader
	n.resetPendingForNewView(O)
	n.leaderSince = time.Now() // for logging
}

// buildPrepareMsgsForNewView pre-computes and signs the Prepare message for
// every entry in a NewView's PreprepareLog, in parallel, ahead of the
// serialized per-slot loop in HandleNewView (that loop must stay single-
// threaded: it mutates consensusLog, pool, and broadcasts). Each entry only
// depends on the fixed view, this node's own id, and the read-only private
// key, so it's safe to compute concurrently. Returns one core.PrepareMsgSig
// per entry at the same index as preprepareLog, so the caller can index in
// directly instead of building+signing inline.
func (n *Node) buildPrepareMsgsForNewView(preprepareLog []core.PreprepareMsgSig, view core.ViewID) []core.PrepareMsgSig {
	if len(preprepareLog) == 0 {
		return nil
	}

	results := make([]core.PrepareMsgSig, len(preprepareLog))

	workers := runtime.NumCPU()
	if workers > len(preprepareLog) {
		workers = len(preprepareLog)
	}
	if workers < 1 {
		workers = 1
	}
	chunkSize := (len(preprepareLog) + workers - 1) / workers

	var wg sync.WaitGroup
	for start := 0; start < len(preprepareLog); start += chunkSize {
		end := start + chunkSize
		if end > len(preprepareLog) {
			end = len(preprepareLog)
		}
		wg.Add(1)
		go func(lo, hi int) {
			defer wg.Done()
			for i := lo; i < hi; i++ {
				preprepareMsg := preprepareLog[i]
				msg := core.PrepareMsg{
					View:   view,
					SeqNum: preprepareMsg.PreprepareMsgMini.SeqNum,
					Digest: preprepareMsg.PreprepareMsgMini.DigestClientMsg,
					From:   n.GetNodeID(),
				}
				pbMsg := transportpb.PrepareToPB(msg)
				payloadBytes, err := marshalDeterministic(pbMsg)
				if err != nil {
					n.log.Error("Failed to marshal Prepare message for signing: %v", err)
					// to be decided
				}
				signature := crypto.SignMessageEd25519(payloadBytes, n.encryptionKeyStore.GetPrivateKey())
				results[i] = core.PrepareMsgSig{
					PrepareMsg: msg,
					Signature:  signature,
				}
			}
		}(start, end)
	}
	wg.Wait()

	return results
}

func (n *Node) HandleNewView(newViewMsg core.NewViewMsg, _ []byte) {
	timestart := time.Now()
	view := n.GetViewID()
	forView := n.GetForViewID()
	if newViewMsg.NewViewNumber.LessThanOrEqual(view) {

		return
	}
	if newViewMsg.NewViewNumber.LessThan(forView) {

		n.log.Error("Received new view message for view (%d,%d) which is less than my for view (%d,%d), ignoring", newViewMsg.NewViewNumber.Generation, newViewMsg.NewViewNumber.Counter, forView.Generation, forView.Counter)
		return
	}
	// The NewView's signature was checked in Deliver. Its ViewChanges are named by
	// digest and resolved against the ones this node verified itself, so a primary
	// cannot alter or invent one (node/newviewrefs.go).
	resolved, missing, err := n.resolveViewChangeRefs(newViewMsg)
	if err != nil {
		n.log.Error("NEWVIEW REJECTED reason=refs view=(%d,%d) from=%d: %v", newViewMsg.NewViewNumber.Generation, newViewMsg.NewViewNumber.Counter, newViewMsg.From, err)
		return
	}
	if len(missing) > 0 {
		n.setPendingNewView(newViewMsg, missing)
		return
	}
	n.notePendingNewViewResolved(newViewMsg.NewViewNumber)

	// TODO(safety): nothing checks that newViewMsg.From is this view's primary; the
	// signature only proves who sent it, so a Byzantine non-primary could install
	// itself as leader. RoundRobin could check primaryForView, Election
	// electionCandidateForView with electionExcludedForGeneration; Aware cannot yet
	// (its leader depends on matrix state replicas may not share during catch-up).
	// On a generation jump newViewMsg.Action would need f+1 resolved ViewChanges
	// carrying it, or a non-primary could claim a policy that skips the check.
	// Cannot fire in the testbed without a Byzantine node.

	Oset, maxSeq, latestStableCheckpoint, checkpointProof, checkpointBalances := n.createOReplica(resolved, newViewMsg.NewViewNumber)

	verifiedOsets := verifyOSet(Oset, newViewMsg.PreprepareLog)
	if !verifiedOsets {
		n.log.Error("NEWVIEW REJECTED reason=oset view=(%d,%d) from=%d: O-set does not match the one recomputed from its ViewChanges", newViewMsg.NewViewNumber.Generation, newViewMsg.NewViewNumber.Counter, newViewMsg.From)
		return
	}
	n.log.Info("Received and accepted new view message for view %d and from node %d at replica", newViewMsg.NewViewNumber, newViewMsg.From)

	if newViewMsg.NewViewNumber.Equal(forView) {
		n.assert(newViewMsg.Action == n.GetCurrAction(), "new view message action %v does not match current action %v for view (%d,%d) at replica", newViewMsg.Action, n.GetCurrAction(), newViewMsg.NewViewNumber.Generation, newViewMsg.NewViewNumber.Counter)
	}
	// oldView := n.view
	if newViewMsg.NewViewNumber.Generation > forView.Generation+1 {
		// Not expected in practice (a crashed node still advances generations).
		// incrementGeneration moves one generation only, so the generation number,
		// the decider and the epoch grid are all left one generation behind.
		n.log.Error("NewView for (%d,%d) is %d generations ahead of my for view (%d,%d); multi-generation catch-up is not handled",
			newViewMsg.NewViewNumber.Generation, newViewMsg.NewViewNumber.Counter,
			newViewMsg.NewViewNumber.Generation-forView.Generation, forView.Generation, forView.Counter)
	}
	if newViewMsg.NewViewNumber.Generation > forView.Generation {
		// multiple gen jump not handled
		// increment function only add by 1 even if here multijump
		// so candidate in awareinside increment gen update for current g+1
		// so maybeadopt aware also update candidate again for multi jump G
		// TODO: handle multiple generation jumps properly
		n.incrementGeneration(newViewMsg.Action)
		// TODO(safety): catch-up trusts the rows carried by this one new view.
		n.maybeAdoptAwareRows(newViewMsg.NewViewNumber.Generation, newViewMsg.Action, newViewMsg.AwareRows)
	}
	n.SetViewID(newViewMsg.NewViewNumber)
	n.SetForViewID(newViewMsg.NewViewNumber)
	view = n.GetViewID()
	n.leaderId = newViewMsg.From
	n.leaderIdForView[newViewMsg.NewViewNumber] = newViewMsg.From
	n.viewChangeRunning = false

	mylatestStableCheckpointSeq := n.GetLastStableCheckpointSeq()
	if latestStableCheckpoint.seq > mylatestStableCheckpointSeq {
		n.log.Debug("stable checkpoint %d ahead of my last stable checkpoint%d will be stabalising checkpoint at new view replica", latestStableCheckpoint.seq, mylatestStableCheckpointSeq)
		n.fastPathStablizeCheckpointviaVC(latestStableCheckpoint, checkpointProof, checkpointBalances, "replica")

	} else if latestStableCheckpoint.seq < mylatestStableCheckpointSeq {
		n.log.Debug("my stable checkpoint %d is ahead of the latest stable checkpoint %d in new view replica", mylatestStableCheckpointSeq, latestStableCheckpoint.seq)
		// if maxSeq < mylatestStableCheckpointSeq {
		// 	n.log.Error("my stable checkpoint %d is ahead of the latest stable checkpoint %d and maxSeq %d is less than my stable checkpoint in new view primary", mylatestStableCheckpointSeq, latestStableCheckpoint.seq, maxSeq)
		// }
		n.assert(maxSeq >= mylatestStableCheckpointSeq, "maxseq %d is less than my stable checkpoint %d in new view replica", maxSeq, mylatestStableCheckpointSeq)
		// if maxseq eq lateststable checkpoint then no change needed to log
		// impossible that my stable cp ahead and maxseq not cover it
	}
	// if fast path stabalize or not low should always be + 1 of last stable checkpoint
	n.assert(n.consensusLog.low == n.GetLastStableCheckpointSeq()+1, "consensus log low %d is not equal to last stable checkpoint seq + 1 %d in new view replica", n.consensusLog.low, n.GetLastStableCheckpointSeq()+1)
	n.assert(maxSeq <= n.consensusLog.high, "maxseq %d is greater than consensus log high %d in new view replica", maxSeq, n.consensusLog.high)

	prepareMsgs := n.buildPrepareMsgsForNewView(newViewMsg.PreprepareLog, view)
	for i, preprepareMsg := range newViewMsg.PreprepareLog {
		if preprepareMsg.PreprepareMsgMini.SeqNum < n.consensusLog.low {
			// we dont help in running consensus if our stable cp ahead in vc path and even in normal path
			// n.log.Debug("preprepare seq %d is less than consensus log low %d, skipping", preprepareMsg.PreprepareMsgMini.SeqNum, n.consensusLog.low)
			continue
		}
		// all in O range reset their per view state
		slot := n.consensusLog.ResetPerViewState(preprepareMsg.PreprepareMsgMini.SeqNum, view)
		n.slotPreprepare(slot, &preprepareMsg.PreprepareMsgMini, preprepareMsg.Signature, true)
		slot.view = view

		// signing already done in parallel above; just use the precomputed result
		msgForLog := prepareMsgs[i]
		msg := msgForLog.PrepareMsg
		signature := msgForLog.Signature
		slot.prepareSent = true

		slot.prepares[n.GetNodeID()] = msgForLog
		n.asyncBroadCast(core.MsgPrepareMessage, msg, signature)

		// check if after all the flow if order of actual message same as digest of individual client messages in preprepare message
		// without carry_state the O-set has no bodies; they are already in the pool from HandlePrePrepare
		if n.cfg.CarryState {
			n.pool.AddBatch(preprepareMsg.ActualMsg, preprepareMsg.PreprepareMsgMini.DigestIndividualClientMsgs, preprepareMsg.PreprepareMsgMini.SeqNum, view)
		}

	}
	// we not remove prepared entries , but i hope usually nothing to retain
	// committed above should always be empty
	retained, committedAbove := n.consensusLog.RemoveLogEntriesAboveSeq(maxSeq, view)
	n.warnRetainedDurableSlots(retained, committedAbove, maxSeq, "replica", view)
	n.sequenceNumber = maxSeq
	n.pruneViewChangeState(view)
	n.pilotTraceStart(maxSeq, view)
	n.handleNewViewUpdatePerf(maxSeq, view, newViewMsg.Throughput)
	n.resetPendingForNewView(nil)
	// here we may have buffer
	// buffer probably emptied to channel
	n.replayBufferedMessagesForView(view)
	go n.sendLeaderIdUpdate(n.leaderId, view, n.GetCurrAction())
	duration := time.Since(timestart)
	n.log.Info("New view (%d,%d) processing completed in %v", view.Generation, view.Counter, duration)
	n.acceptNewViewTimers()

}

func (n *Node) createO(vcMsgSigs []*core.ViewChangeMsgSig, view core.ViewID, oldView core.ViewID) ([]core.PreprepareMsgSig, int64, checkpoint, []core.CheckpointMsgSig, map[string]*big.Int) {
	O := make([]core.PreprepareMsgSig, 0)
	preprepareLog := make(map[int64]core.PreprepareMsgSig)
	// n.checkpointMu.Lock()
	// minS := n.lastStableCheckpoint.seq + 1
	minS := vcMsgSigs[0].ViewChangeMsg.CheckpointSeqNumber + 1
	// myStableCheckpoint := n.lastStableCheckpoint
	latestStableCheckpoint := checkpoint{
		seq:    vcMsgSigs[0].ViewChangeMsg.CheckpointSeqNumber,
		digest: vcMsgSigs[0].ViewChangeMsg.CheckpointDigest,
	}
	// will be nil if laststable is 0
	checkpointProof := vcMsgSigs[0].ViewChangeMsg.CheckpointProof
	checkpointBalances := vcMsgSigs[0].ViewChangeMsg.CheckpointBalances
	// checkpointNeedsSync := false
	for _, vcMsgSig := range vcMsgSigs {
		if vcMsgSig.ViewChangeMsg.CheckpointSeqNumber > latestStableCheckpoint.seq {
			// n.log.Error("missing the latest stable checkpoint at o primary") // would need to pass digest and application state in vc message for sync
			// n.lastStableCheckpoint = checkpoint{
			// 	seq: vcMsgSig.ViewChangeMsg.CheckpointSeqNumber,
			// }
			latestStableCheckpoint = checkpoint{
				seq:    vcMsgSig.ViewChangeMsg.CheckpointSeqNumber,
				digest: vcMsgSig.ViewChangeMsg.CheckpointDigest,
			}
			checkpointProof = vcMsgSig.ViewChangeMsg.CheckpointProof
			checkpointBalances = vcMsgSig.ViewChangeMsg.CheckpointBalances
			minS = latestStableCheckpoint.seq + 1

		}
	}
	// n.fastPathStablizeCheckpointviaVC(latestStableCheckpoint, checkpointProof, "primary")

	// if latestStableCheckpoint.seq > n.lastExecuted {
	// 	// n.lastExecuted = latestStableCheckpoint.seq // unsafe checkpoint forwarding
	// 	// n.log.Error("updating last executed to stable checkpoint seq %d", n.lastExecuted)

	// } else if latestStableCheckpoint.seq < n.lastExecuted {
	// 	n.log.Debug("my stable checkpoint seq %d is less than my last executed %d, this should not happen", latestStableCheckpoint.seq, n.lastExecuted)
	// }

	maxS := minS - 1
	for _, viewChangeMsg := range vcMsgSigs {
		for seqNumber, pm := range viewChangeMsg.ViewChangeMsg.PreparedCerts {
			if pm == nil || seqNumber < minS {
				continue
			}
			candidate := pm.PreprepareMsg
			candidateView := candidate.PreprepareMsgMini.View

			if candidateView.GreaterThanOrEqual(view) {
				n.log.Error(
					"prepared cert seq %d has view (%d,%d) ahead of my new view (%d,%d) in createO at Primary",
					seqNumber, candidateView.Generation, candidateView.Counter, view.Generation, view.Counter,
				)
				continue
			}

			if seqNumber > maxS {
				maxS = seqNumber

			}
			existing, exists := preprepareLog[seqNumber]
			if !exists ||
				candidateView.GreaterThan(existing.PreprepareMsgMini.View) {
				preprepareLog[seqNumber] = candidate
			}
			// if pm.PreprepareMsg.PreprepareMsgMini.View < oldView {
			// 	n.log.Error("preprepare message has an older view number createO at Primary")
			// }
			// preprepareLog[seqNumber] = pm.PreprepareMsg

		}
	}

	if maxS < minS {
		n.log.Debug("no suffix at o primary")
		return O, minS - 1, latestStableCheckpoint, checkpointProof, checkpointBalances
	}
	if n.cfg.ParallelWorkers {
		O = n.buildOSetParallel(minS, maxS, view, preprepareLog)
	} else {
		O = n.buildOSet(minS, maxS, view, preprepareLog)
	}
	// O = n.buildOSet(minS, maxS, view, preprepareLog)
	return O, maxS, latestStableCheckpoint, checkpointProof, checkpointBalances

}

// buildOSet constructs the O-set for a NewView message: one freshly-signed
// preprepare per sequence number from minS..maxS, reusing the prepared cert
// carried in preprepareLog when one exists for that seq, and a signed no-op
// ("dummy") preprepare when none does (Castro-Liskov O-set rule). Extracted
// from createO for clean separation.
func (n *Node) buildOSet(minS, maxS int64, view core.ViewID, preprepareLog map[int64]core.PreprepareMsgSig) []core.PreprepareMsgSig {
	O := make([]core.PreprepareMsgSig, 0, maxS-minS+1)
	for seq := minS; seq <= maxS; seq++ {
		if preprepare, exists := preprepareLog[seq]; exists {
			preprepare.PreprepareMsgMini.View = view
			// pbMsg := transportpb.PreprepareMiniToPB2(preprepare.PreprepareMsgMini)
			payloadBytes, err := marshalDeterministic(preprepareSignPayload(preprepare.PreprepareMsgMini.View, preprepare.PreprepareMsgMini.SeqNum, preprepare.PreprepareMsgMini.DigestClientMsg[:]))
			if err != nil {
				// handle error, maybe skip this preprepare
				continue
			}
			signature := crypto.SignMessageEd25519(payloadBytes, n.encryptionKeyStore.GetPrivateKey())
			if n.cfg.CarryState {
				O = append(O, core.PreprepareMsgSig{
					PreprepareMsgMini: preprepare.PreprepareMsgMini,
					Signature:         signature,
					ActualMsg:         preprepare.ActualMsg,
				})
			} else {
				O = append(O, core.PreprepareMsgSig{
					PreprepareMsgMini: preprepare.PreprepareMsgMini,
					Signature:         signature,
				})
			}

		} else {
			n.log.Info("No prepared cert for seq %d in view change messages, creating dummy preprepare the min and max seq are %d and %d", seq, minS, maxS)
			dummyPreprepare := core.PreprepareMsgMini{
				View:            view,
				SeqNum:          seq,
				DigestClientMsg: [32]byte{},
			}
			// pbMsg := transportpb.PreprepareMiniToPB2(dummyPreprepare)
			payloadBytes, err := marshalDeterministic(preprepareSignPayload(dummyPreprepare.View, dummyPreprepare.SeqNum, dummyPreprepare.DigestClientMsg[:]))
			if err != nil {
				// handle error, maybe skip this preprepare
				continue
			}
			signature := crypto.SignMessageEd25519(payloadBytes, n.encryptionKeyStore.GetPrivateKey())
			O = append(O, core.PreprepareMsgSig{
				PreprepareMsgMini: dummyPreprepare,
				Signature:         signature,
			})
		}
	}
	return O
}

// buildOSetParallel is the parallel counterpart to buildOSet, independent of
// it so both can be swapped in interchangeably behind cfg.ParallelWorkers.
// Each seq's entry only touches per-item local data plus the read-only
// private key, so it's safe to build concurrently. Each worker appends to its
// OWN local slice for its contiguous seq range rather than a shared O
// (concurrent append into one shared slice would race); the chunk slices are
// concatenated afterward, in increasing-seq-range order, reproducing the same
// seq-ascending order buildOSet produces. Not wired to any call site yet.
func (n *Node) buildOSetParallel(minS int64, maxS int64, view core.ViewID, preprepareLog map[int64]core.PreprepareMsgSig) []core.PreprepareMsgSig {
	if maxS < minS {
		return make([]core.PreprepareMsgSig, 0)
	}
	total := maxS - minS + 1

	workers := int64(runtime.NumCPU())
	if workers > total {
		workers = total
	}
	if workers < 1 {
		workers = 1
	}
	chunkSize := (total + workers - 1) / workers
	// have indiviudal slice for each worker because we dont have input for each index some are skipped, either we could have fixed index for everything like in handlenewview parallelism then we could use one share slice
	chunkResults := make([][]core.PreprepareMsgSig, workers)
	var wg sync.WaitGroup

	for i := int64(0); i < workers; i++ {
		chunkStart := minS + i*chunkSize
		chunkEnd := chunkStart + chunkSize - 1
		if chunkEnd > maxS {
			chunkEnd = maxS
		}
		if chunkStart > chunkEnd {
			continue // fewer remaining items than workers
		}
		wg.Add(1)
		go func(idx, start, end int64) {
			defer wg.Done()
			chunk := make([]core.PreprepareMsgSig, 0, end-start+1)
			for seq := start; seq <= end; seq++ {
				if preprepare, exists := preprepareLog[seq]; exists {
					preprepare.PreprepareMsgMini.View = view
					payloadBytes, err := marshalDeterministic(preprepareSignPayload(preprepare.PreprepareMsgMini.View, preprepare.PreprepareMsgMini.SeqNum, preprepare.PreprepareMsgMini.DigestClientMsg[:]))
					if err != nil {
						// handle error, maybe skip this preprepare
						continue
					}
					signature := crypto.SignMessageEd25519(payloadBytes, n.encryptionKeyStore.GetPrivateKey())
					if n.cfg.CarryState {
						chunk = append(chunk, core.PreprepareMsgSig{
							PreprepareMsgMini: preprepare.PreprepareMsgMini,
							Signature:         signature,
							ActualMsg:         preprepare.ActualMsg,
						})
					} else {
						chunk = append(chunk, core.PreprepareMsgSig{
							PreprepareMsgMini: preprepare.PreprepareMsgMini,
							Signature:         signature,
						})
					}

				} else {
					n.log.Info("No prepared cert for seq %d in view change messages, creating dummy preprepare the min and max seq are %d and %d", seq, minS, maxS)
					dummyPreprepare := core.PreprepareMsgMini{
						View:            view,
						SeqNum:          seq,
						DigestClientMsg: [32]byte{},
					}
					payloadBytes, err := marshalDeterministic(preprepareSignPayload(dummyPreprepare.View, dummyPreprepare.SeqNum, dummyPreprepare.DigestClientMsg[:]))
					if err != nil {
						// handle error, maybe skip this preprepare
						continue
					}
					signature := crypto.SignMessageEd25519(payloadBytes, n.encryptionKeyStore.GetPrivateKey())
					chunk = append(chunk, core.PreprepareMsgSig{
						PreprepareMsgMini: dummyPreprepare,
						Signature:         signature,
					})
				}
			}
			chunkResults[idx] = chunk
		}(i, chunkStart, chunkEnd)
	}
	wg.Wait()

	O := make([]core.PreprepareMsgSig, 0, total)
	for _, chunk := range chunkResults {
		O = append(O, chunk...)
	}
	return O
}

func (n *Node) createOReplica(vcMsgSigs []*core.ViewChangeMsgSig, view core.ViewID) (map[int64]core.PreprepareMsgSig, int64, checkpoint, []core.CheckpointMsgSig, map[string]*big.Int) {

	preprepareLog := make(map[int64]core.PreprepareMsgSig)
	// n.checkpointMu.Lock()
	// minS := n.lastStableCheckpoint.seq + 1
	minS := vcMsgSigs[0].ViewChangeMsg.CheckpointSeqNumber + 1
	// myStableCheckpoint := n.lastStableCheckpoint
	latestStableCheckpoint := checkpoint{
		seq:    vcMsgSigs[0].ViewChangeMsg.CheckpointSeqNumber,
		digest: vcMsgSigs[0].ViewChangeMsg.CheckpointDigest,
	}
	// will be nil if laststable is 0
	checkpointProof := vcMsgSigs[0].ViewChangeMsg.CheckpointProof
	checkpointBalances := vcMsgSigs[0].ViewChangeMsg.CheckpointBalances
	// checkpointNeedsSync := false
	for _, vcMsgSig := range vcMsgSigs {
		if vcMsgSig.ViewChangeMsg.CheckpointSeqNumber > latestStableCheckpoint.seq {
			// n.log.Error("missing the latest stable checkpoint at o primary") // would need to pass digest and application state in vc message for sync
			// n.lastStableCheckpoint = checkpoint{
			// 	seq: vcMsgSig.ViewChangeMsg.CheckpointSeqNumber,
			// }
			latestStableCheckpoint = checkpoint{
				seq:    vcMsgSig.ViewChangeMsg.CheckpointSeqNumber,
				digest: vcMsgSig.ViewChangeMsg.CheckpointDigest,
			}
			checkpointProof = vcMsgSig.ViewChangeMsg.CheckpointProof
			checkpointBalances = vcMsgSig.ViewChangeMsg.CheckpointBalances
			minS = latestStableCheckpoint.seq + 1

		}
	}
	// n.fastPathStablizeCheckpointviaVC(latestStableCheckpoint, checkpointProof, "primary")

	// if latestStableCheckpoint.seq > n.lastExecuted {
	// 	// n.lastExecuted = latestStableCheckpoint.seq // unsafe checkpoint forwarding
	// 	// n.log.Error("updating last executed to stable checkpoint seq %d", n.lastExecuted)

	// } else if latestStableCheckpoint.seq < n.lastExecuted {
	// 	n.log.Debug("my stable checkpoint seq %d is less than my last executed %d, this should not happen", latestStableCheckpoint.seq, n.lastExecuted)
	// }

	maxS := minS - 1
	for _, viewChangeMsg := range vcMsgSigs {
		for seqNumber, pm := range viewChangeMsg.ViewChangeMsg.PreparedCerts {
			if pm == nil || seqNumber < minS {
				continue
			}
			candidate := pm.PreprepareMsg
			candidateView := candidate.PreprepareMsgMini.View

			if candidateView.GreaterThanOrEqual(view) {
				n.log.Error(
					"prepared cert seq %d has view (%d,%d) ahead of my new view (%d,%d) in createO at Replica",
					seqNumber, candidateView.Generation, candidateView.Counter, view.Generation, view.Counter,
				)
				continue
			}

			if seqNumber > maxS {
				maxS = seqNumber

			}
			existing, exists := preprepareLog[seqNumber]
			if !exists ||
				candidateView.GreaterThan(existing.PreprepareMsgMini.View) {
				preprepareLog[seqNumber] = candidate
			}
			// if pm.PreprepareMsg.PreprepareMsgMini.View < oldView {
			// 	n.log.Error("preprepare message has an older view number createO at Primary")
			// }
			// preprepareLog[seqNumber] = pm.PreprepareMsg

		}
	}

	if maxS < minS {
		n.log.Debug("no suffix at o replica")
		return preprepareLog, minS - 1, latestStableCheckpoint, checkpointProof, checkpointBalances
	}
	for seq := minS; seq <= maxS; seq++ {
		if preprepare, exists := preprepareLog[seq]; exists {
			preprepare.PreprepareMsgMini.View = view
			// // pbMsg := transportpb.PreprepareMiniToPB2(preprepare.PreprepareMsgMini)
			// payloadBytes, err := marshalDeterministic(preprepareSignPayload(preprepare.PreprepareMsgMini.View, preprepare.PreprepareMsgMini.SeqNum, preprepare.PreprepareMsgMini.DigestClientMsg[:]))
			// if err != nil {
			// 	// handle error, maybe skip this preprepare
			// 	continue
			// }
			// signature := crypto.SignMessageEd25519(payloadBytes, n.encryptionKeyStore.GetPrivateKey())
			preprepareLog[seq] = preprepare

		} else {

			dummyPreprepare := core.PreprepareMsgMini{
				View:            view,
				SeqNum:          seq,
				DigestClientMsg: [32]byte{},
			}
			// pbMsg := transportpb.PreprepareMiniToPB2(dummyPreprepare)
			// payloadBytes, err := marshalDeterministic(preprepareSignPayload(dummyPreprepare.View, dummyPreprepare.SeqNum, dummyPreprepare.DigestClientMsg[:]))
			// if err != nil {
			// 	// handle error, maybe skip this preprepare
			// 	continue
			// }
			// signature := crypto.SignMessageEd25519(payloadBytes, n.encryptionKeyStore.GetPrivateKey())
			preprepareLog[seq] = core.PreprepareMsgSig{
				PreprepareMsgMini: dummyPreprepare,
				Signature:         nil,
			}
		}
	}
	return preprepareLog, maxS, latestStableCheckpoint, checkpointProof, checkpointBalances

}
