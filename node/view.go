package node

import (
	// "bytes"
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

func (n *Node) incrementGeneration(action core.Action) core.ViewID {
	forView := n.GetForViewID()
	n.resetEpochTimer()
	n.SetCurrAction(action)
	n.SwitchTriggerMode(action.TriggerMode) // its is some what parallel state with curr action both update together onn generation update
	n.epochManager.GCBelow(forView.Generation + 1)
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
	// only written, never read (leaderForView is hardcoded); kept as a record of the
	// current view's leader only
	for view := range n.leaderIdForView {
		if view.LessThan(installed) {
			delete(n.leaderIdForView, view)
		}
	}
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

func (n *Node) verifyVC(vc core.ViewChangeMsg) bool {
	verifiedPreparedCerts := false
	if n.cfg.ParallelWorkers {
		verifiedPreparedCerts = n.verifyPreparedCertsParallel(vc.PreparedCerts)
	} else {
		verifiedPreparedCerts = n.verifyPreparedCerts(vc.PreparedCerts)
	}
	// verifiedPreparedCerts := n.verifyPreparedCerts(vc.PreparedCerts)
	// Additional checks can be added here, such as verifying the signature of the ViewChangeMsg itself.
	return verifiedPreparedCerts
}

func (n *Node) verifyPrepareLog(prepareLog map[int]core.PrepareMsgSig, view core.ViewID, seq int64, digest [32]byte) bool {
	required := n.QuorumSize() - 1
	if required <= 0 {
		return true
	}
	if len(prepareLog) < required {
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
		if validCount >= required {
			return true
		}
	}

	n.log.Error("not enough valid prepare messages: valid=%d required=%d view=%d seq=%d", validCount, required, view, seq)
	return false
}

func (n *Node) verifyPreparedCerts(preparedCerts map[int64]*core.PreparedCert) bool {
	for certSeq, cert := range preparedCerts {
		if cert == nil {
			return false
		}
		ok, view, seq, digest := n.verifyPreprepare(cert.PreprepareMsg)
		if !ok {
			return false
		}
		if seq != certSeq {
			n.log.Error("prepared cert seq mismatch: map key=%d preprepare seq=%d", certSeq, seq)
			return false
		}
		if !n.verifyPrepareLog(cert.PrepareLog, view, seq, digest) {
			return false
		}
	}
	return true
}

// verifyPreparedCertsParallel is the parallel counterpart to verifyPreparedCerts.
// Each cert's verification (verifyPreprepare + verifyPrepareLog) only reads
// immutable/read-only node state (encryption keys, leader-for-view lookups) plus
// pure crypto verification and logging, both safe for concurrent use, so it is
// safe to fan the map out across a worker pool. Not wired to any call site yet.
func (n *Node) verifyPreparedCertsParallel(preparedCerts map[int64]*core.PreparedCert) bool {
	if len(preparedCerts) == 0 {
		return true
	}

	type certEntry struct {
		seq  int64
		cert *core.PreparedCert
	}
	// cant index map like slices with [start:end] and keep these sliding windows sorted so thats why convert to slice
	entries := make([]certEntry, 0, len(preparedCerts))
	for certSeq, cert := range preparedCerts {
		entries = append(entries, certEntry{seq: certSeq, cert: cert})
	}

	workers := runtime.NumCPU()
	if workers > len(entries) {
		workers = len(entries)
	}
	if workers < 1 {
		workers = 1
	}
	chunkSize := (len(entries) + workers - 1) / workers
	// ceil(a/b) = (a + b - 1) / b
	// 10/3 gives 3 but we want 4
	// if a evenly divides by b then adding b-1 doesnt change quotient

	var wg sync.WaitGroup
	var failed atomic.Bool

	for start := 0; start < len(entries); start += chunkSize {
		end := start + chunkSize
		if end > len(entries) {
			end = len(entries)
		}
		wg.Add(1)
		go func(chunk []certEntry) {
			defer wg.Done()
			for _, e := range chunk {
				if failed.Load() {
					return
				}
				if e.cert == nil {
					failed.Store(true)
					return
				}
				ok, view, seq, digest := n.verifyPreprepare(e.cert.PreprepareMsg)
				if !ok {
					failed.Store(true)
					return
				}
				if seq != e.seq {
					n.log.Error("prepared cert seq mismatch: map key=%d preprepare seq=%d", e.seq, seq)
					failed.Store(true)
					return
				}
				if !n.verifyPrepareLog(e.cert.PrepareLog, view, seq, digest) {
					failed.Store(true)
					return
				}
			}
		}(entries[start:end])
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

func (n *Node) verifyNewView(newViewMsg core.NewViewMsg) bool {
	seenFrom := make(map[int]struct{}, len(newViewMsg.ViewChangeLog))
	viewChangeMsgsCached := n.viewChangeMsgsLog[newViewMsg.NewViewNumber]
	for _, vcMsgSig := range newViewMsg.ViewChangeLog {
		if vcMsgSig == nil {
			n.log.Error("nil view change message in new view message log for view (%d,%d)", newViewMsg.NewViewNumber.Generation, newViewMsg.NewViewNumber.Counter)
			return false
		}
		if _, exists := seenFrom[vcMsgSig.ViewChangeMsg.From]; exists {
			continue
		}

		foundInCache := false
		for _, cachedMsg := range viewChangeMsgsCached {
			if cachedMsg == nil {
				continue
			}
			if cachedMsg.ViewChangeMsg.From == vcMsgSig.ViewChangeMsg.From {
				// cachedPayloadBytes, err := marshalDeterministic(transportpb.ViewChangeToPB(cachedMsg.ViewChangeMsg))
				// if err != nil {
				// 	n.log.Error("failed to marshal cached view change message for view %d from node %d: %v", newViewMsg.NewViewNumber, cachedMsg.ViewChangeMsg.From, err)
				// 	continue
				// }
				// incomingPayloadBytes, err := marshalDeterministic(transportpb.ViewChangeToPB(vcMsgSig.ViewChangeMsg))
				// if err != nil {
				// 	n.log.Error("failed to marshal incoming view change message for view %d from node %d: %v", newViewMsg.NewViewNumber, vcMsgSig.ViewChangeMsg.From, err)
				// 	continue
				// }
				// if !bytes.Equal(cachedPayloadBytes, incomingPayloadBytes) {
				// 	n.log.Error("cached view change payload mismatch for view %d from node %d", newViewMsg.NewViewNumber, vcMsgSig.ViewChangeMsg.From)
				// 	continue
				// }
				foundInCache = true
				break
			}
		}
		if !foundInCache {
			payloadBytes, err := marshalDeterministic(transportpb.ViewChangeToPB(vcMsgSig.ViewChangeMsg))
			if err != nil {
				n.log.Error("failed to marshal view change message for view (%d,%d) from node %d: %v", newViewMsg.NewViewNumber.Generation, newViewMsg.NewViewNumber.Counter, vcMsgSig.ViewChangeMsg.From, err)
				continue
			}
			senderPubKey, exists := n.encryptionKeyStore.GetPublicKey(vcMsgSig.ViewChangeMsg.From)
			if !exists {
				n.log.Error("public key not found for view change sender node ID: %d", vcMsgSig.ViewChangeMsg.From)
				continue
			}
			if !crypto.VerifySignatureEd25519(payloadBytes, vcMsgSig.Signature, senderPubKey) {
				n.log.Error("signature verification failed for view change message for view (%d,%d) from node %d", newViewMsg.NewViewNumber.Generation, newViewMsg.NewViewNumber.Counter, vcMsgSig.ViewChangeMsg.From)
				continue
			}
			n.log.Info("verifying VC in new newview")
			verifiedVC := n.verifyVC(vcMsgSig.ViewChangeMsg)
			if verifiedVC {
				seenFrom[vcMsgSig.ViewChangeMsg.From] = struct{}{}
			}
		} else {
			seenFrom[vcMsgSig.ViewChangeMsg.From] = struct{}{}
		}
	}

	if len(seenFrom) >= 2*n.fNodes+1 {
		return true
	} else {
		n.log.Error("not enough unique view change messages in new view message log for view (%d,%d): unique=%d required=%d", newViewMsg.NewViewNumber.Generation, newViewMsg.NewViewNumber.Counter, len(seenFrom), 2*n.fNodes+1)
		return false
	}
}

func verifyOSet(Ocreated map[int64]core.PreprepareMsgSig, Oreceived []core.PreprepareMsgSig) bool {
	for _, preprepareMsgSig := range Oreceived {
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
		n.pool.AddBatch(preprepareMsg.ActualMsg, preprepareMsg.PreprepareMsgMini.DigestIndividualClientMsgs, preprepareMsg.PreprepareMsgMini.SeqNum, view)

	}
	retained, committedAbove := n.consensusLog.RemoveLogEntriesAboveSeq(maxSeq, view)
	n.warnRetainedDurableSlots(retained, committedAbove, maxSeq, "primary", view)
	// max seq number in log is prepareseq number in all nodes
	n.sequenceNumber = maxSeq

	maxRecentThroughput := n.newviewUpdatePerf(maxSeq, view)

	newViewMsg := core.NewViewMsg{
		NewViewNumber: view,
		Action:        n.GetCurrAction(),
		From:          n.GetNodeID(),
		PreprepareLog: O,
		ViewChangeLog: n.viewChangeMsgsLog[view], // even if delete in prune this ref isnot lost till we done with it
		Throughput:    maxRecentThroughput,
	}
	// pbMsg := transportpb.NewViewToPB(newViewMsg)
	// payloadBytes, err := marshalDeterministic(pbMsg)
	// if err != nil {
	// 	n.log.Error("Failed to marshal NewView message for signing: %v", err)
	// 	return
	// }
	// signature := crypto.SignMessageEd25519(payloadBytes, n.encryptionKeyStore.GetPrivateKey())
	n.asyncBroadCast(core.MsgNewViewMessage, newViewMsg, nil)
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
	n.pendingRequests.Reset()
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
	verifiedNewView := n.verifyNewView(newViewMsg)
	if !verifiedNewView {
		n.log.Error("Failed to verify new view message for view (%d,%d), ignoring at replica", newViewMsg.NewViewNumber.Generation, newViewMsg.NewViewNumber.Counter)
		return
	}

	Oset, maxSeq, latestStableCheckpoint, checkpointProof, checkpointBalances := n.createOReplica(newViewMsg.ViewChangeLog, newViewMsg.NewViewNumber)

	verifiedOsets := verifyOSet(Oset, newViewMsg.PreprepareLog)
	if !verifiedOsets {
		n.log.Error("O set verification failed for new view message for view %d at replica", newViewMsg.NewViewNumber)

		return
	}
	n.log.Info("Received and accepted new view message for view %d and from node %d at replica", newViewMsg.NewViewNumber, newViewMsg.From)

	if newViewMsg.NewViewNumber.Equal(forView) {
		n.assert(newViewMsg.Action == n.GetCurrAction(), "new view message action %v does not match current action %v for view (%d,%d) at replica", newViewMsg.Action, n.GetCurrAction(), newViewMsg.NewViewNumber.Generation, newViewMsg.NewViewNumber.Counter)
	}
	// oldView := n.view
	if newViewMsg.NewViewNumber.Generation > forView.Generation {
		n.incrementGeneration(newViewMsg.Action)
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
		n.pool.AddBatch(preprepareMsg.ActualMsg, preprepareMsg.PreprepareMsgMini.DigestIndividualClientMsgs, preprepareMsg.PreprepareMsgMini.SeqNum, view)

	}
	// we not remove prepared entries , but i hope usually nothing to retain
	// committed above should always be empty
	retained, committedAbove := n.consensusLog.RemoveLogEntriesAboveSeq(maxSeq, view)
	n.warnRetainedDurableSlots(retained, committedAbove, maxSeq, "replica", view)
	if n.cfg.Performance {
		n.throughputPerf.throughputIntervalStartSeq = maxSeq + THROUGHPUTINTERVAL_DELAY
		n.log.Info("Throughput interval start seq set to %d for new view (%d,%d)", n.throughputPerf.throughputIntervalStartSeq, view.Generation, view.Counter)
		n.throughputPerf.throughputObservationStarted = false
	}
	n.sequenceNumber = maxSeq
	n.pruneViewChangeState(view)
	n.handleNewViewUpdatePerf(maxSeq, view, newViewMsg.Throughput)
	n.pendingRequests.Reset()
	// here we may have buffer
	// buffer probably emptied to channel
	n.replayBufferedMessagesForView(view)
	go n.sendLeaderIdUpdate(n.leaderId, view)
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
