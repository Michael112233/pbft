package node

import (
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/crypto"
	"github.com/michael112233/pbft/transportpb"
)

// A NewView names its 2f+1 ViewChanges by (sender, digest) instead of carrying them
// (OSDI 2000 PBFT, "Proactive Recovery"). Every replica already received and
// verified those ViewChanges directly, so it resolves each ref against its own log.
// A replica missing one (still in transit, or the sender equivocated) asks the new
// primary, which forwards the sender's original signed message; a primary cannot
// forge one, so no VIEW-CHANGE-ACK round is needed as in the MAC-based paper.
//
// All of this runs on the event loop.

const (
	// maxViewChangeFetchAttempts bounds the fetches a replica sends for one pending
	// NewView: one after ViewChangeFetchDelay, one retry at twice that. The new-view
	// timer bounds everything after.
	maxViewChangeFetchAttempts = 2
	// maxViewChangeFetchReplies bounds how often the primary answers one requester
	// for one view, so a Byzantine requester cannot make it re-send 2f+1 large
	// ViewChanges without end.
	maxViewChangeFetchReplies = 2
)

type pendingNewView struct {
	msg      core.NewViewMsg
	missing  []int // senders whose referenced ViewChange this node lacks
	attempts int   // fetches sent so far
	since    time.Time
}

type newViewSentState struct {
	view    core.ViewID
	vcs     map[int]*core.ViewChangeMsgSig // the ViewChanges the NewView referenced
	replies map[int]int                    // fetches answered, per requester
}

func viewChangeDigest(signedBytes []byte) [32]byte {
	return sha256.Sum256(signedBytes)
}

// viewChangeFrom returns the ViewChange counted for sender in view, if any.
func (n *Node) viewChangeFrom(view core.ViewID, from int) *core.ViewChangeMsgSig {
	for _, vc := range n.viewChangeMsgsLog[view] {
		if vc != nil && vc.ViewChangeMsg.From == from {
			return vc
		}
	}
	return nil
}

// findViewChange returns a verified ViewChange of view from sender with digest,
// from the counted log or from the ones kept only for a pending NewView.
func (n *Node) findViewChange(view core.ViewID, from int, digest [32]byte) *core.ViewChangeMsgSig {
	if vc := n.viewChangeFrom(view, from); vc != nil && vc.Digest == digest {
		return vc
	}
	if vc, ok := n.viewChangeAlt[view][digest]; ok && vc.ViewChangeMsg.From == from {
		return vc
	}
	return nil
}

// resolveViewChangeRefs maps a NewView's refs to ViewChanges this node verified on
// receipt (signature in Deliver, prepared certs in HandleViewChangeRoundRobin). It
// returns them in ref order, which createO / createOReplica depend on for ties, and
// the senders it could not resolve. An error means the NewView is malformed.
func (n *Node) resolveViewChangeRefs(nv core.NewViewMsg) ([]*core.ViewChangeMsgSig, []int, error) {
	if len(nv.ViewChangeRefs) < n.QuorumSize() {
		return nil, nil, fmt.Errorf("%d view-change refs, need %d", len(nv.ViewChangeRefs), n.QuorumSize())
	}
	seen := make(map[int]struct{}, len(nv.ViewChangeRefs))
	resolved := make([]*core.ViewChangeMsgSig, 0, len(nv.ViewChangeRefs))
	var missing []int
	for _, ref := range nv.ViewChangeRefs {
		if ref.From < 1 || ref.From > n.NodeCount() {
			return nil, nil, fmt.Errorf("view-change ref from unknown node %d", ref.From)
		}
		if ref.Digest == ([32]byte{}) {
			return nil, nil, fmt.Errorf("view-change ref from node %d has no digest", ref.From)
		}
		if _, dup := seen[ref.From]; dup {
			return nil, nil, fmt.Errorf("two view-change refs from node %d", ref.From)
		}
		seen[ref.From] = struct{}{}
		if vc := n.findViewChange(nv.NewViewNumber, ref.From, ref.Digest); vc != nil {
			resolved = append(resolved, vc)
		} else {
			missing = append(missing, ref.From)
		}
	}
	return resolved, missing, nil
}

// pendingNewViewWants reports whether the pending NewView references exactly this
// ViewChange.
func (n *Node) pendingNewViewWants(view core.ViewID, from int, digest [32]byte) bool {
	p := n.pendingNewView
	if p == nil || !p.msg.NewViewNumber.Equal(view) {
		return false
	}
	for _, ref := range p.msg.ViewChangeRefs {
		if ref.From == from && ref.Digest == digest {
			return true
		}
	}
	return false
}

// storeAltViewChange keeps a second, different ViewChange from a sender that already
// has one in viewChangeMsgsLog. HandleViewChangeRoundRobin calls it only when the
// pending NewView references exactly that (sender, digest). The copy is never
// counted toward a quorum; only resolveViewChangeRefs (via findViewChange) reads it.
//
// Example: Byzantine node X equivocates in view (1,5), sending a signed VC_a to the
// primary and a signed VC_b to replica R.
//  1. R logs VC_b, so X counts once at R.
//  2. The primary builds its O-set from VC_a and its NewView references (X, H(VC_a)).
//  3. R finds X's logged VC has digest H(VC_b), not H(VC_a), so X is missing and
//     the NewView goes pending.
//  4. After view_change_fetch_delay_ms R fetches; the primary forwards VC_a, still
//     signed by X.
//  5. VC_a reaches HandleViewChangeRoundRobin. X already has VC_b, but the pending
//     NewView wants (X, H(VC_a)), so its certs are verified and it is stored here.
//  6. The retried NewView resolves X to VC_a, R recomputes the primary's O-set from
//     the same ViewChanges, and installs the view.
//
// Without it, R could never install a NewView built on X's other ViewChange; with
// it, an equivocating sender neither blocks the view nor counts twice.
func (n *Node) storeAltViewChange(vc *core.ViewChangeMsgSig) {
	view := vc.ViewChangeMsg.ViewNumber
	if n.viewChangeAlt[view] == nil {
		n.viewChangeAlt[view] = make(map[[32]byte]*core.ViewChangeMsgSig)
	}
	n.viewChangeAlt[view][vc.Digest] = vc
}

// setPendingNewView parks a NewView whose refs are not all resolved. Only one is
// kept, the highest view; a lower one than the pending one is dropped.
func (n *Node) setPendingNewView(nv core.NewViewMsg, missing []int) {
	view := nv.NewViewNumber
	if p := n.pendingNewView; p != nil {
		if view.LessThan(p.msg.NewViewNumber) {
			n.log.Warn("NEWVIEW PENDING dropped view=(%d,%d): already holding (%d,%d)", view.Generation, view.Counter, p.msg.NewViewNumber.Generation, p.msg.NewViewNumber.Counter)
			return
		}
		if view.Equal(p.msg.NewViewNumber) {
			// Retried after a ViewChange arrived: keep the fetch schedule.
			// Same view as the pending one: a retry (a VC of this view arrived, late
			// from its sender or forwarded after a fetch) that still has refs missing,
			// or a second NewView for this view. Update msg and missing but keep since,
			// attempts and the fetch timer, so arrivals don't push the fetch back.
			p.msg, p.missing = nv, missing
			return
		}
	}
	n.pendingNewView = &pendingNewView{msg: nv, missing: missing, since: time.Now()}
	n.vcFetchTimerCh = resetOneShotTimer(&n.vcFetchTimer, n.cfg.ViewChangeFetchDelay())
	n.log.Info("NEWVIEW PENDING view=(%d,%d) from=%d missing=%v", view.Generation, view.Counter, nv.From, missing)
}

func (n *Node) clearPendingNewView() {
	n.pendingNewView = nil
	n.stopViewChangeFetchTimer()
}

func (n *Node) stopViewChangeFetchTimer() {
	stopOneShotTimer(n.vcFetchTimer)
	n.vcFetchTimerCh = nil
}

// notePendingNewViewResolved clears the pending NewView of view once its refs all
// resolve, whatever the install outcome: a NewView that then fails its O-set check
// is bad and must not be retried on every later ViewChange.
func (n *Node) notePendingNewViewResolved(view core.ViewID) {
	p := n.pendingNewView
	if p == nil || !p.msg.NewViewNumber.Equal(view) {
		return
	}
	n.log.Info("NEWVIEW RESOLVED view=(%d,%d) after=%v fetches=%d", view.Generation, view.Counter, time.Since(p.since), p.attempts)
	n.clearPendingNewView()
}

// retryPendingNewView re-runs the pending NewView of view after a ViewChange of
// that view arrived.
func (n *Node) retryPendingNewView(view core.ViewID) {
	p := n.pendingNewView
	if p == nil || !p.msg.NewViewNumber.Equal(view) {
		return
	}
	n.HandleNewView(p.msg, nil)
}

func (n *Node) handleViewChangeFetchTimeout() {
	n.vcFetchTimerCh = nil
	p := n.pendingNewView
	if p == nil {
		return
	}
	_, missing, err := n.resolveViewChangeRefs(p.msg)
	// Not expected: every path that stores a VC retries the pending NewView, and
	// malformed refs are rejected before a NewView goes pending. Kept so a missed
	// retry installs here instead of fetching VCs this node already has.
	if err != nil || len(missing) == 0 {
		n.HandleNewView(p.msg, nil)
		return
	}
	p.missing = missing
	p.attempts++
	n.sendViewChangeFetch(p.msg.NewViewNumber, p.msg.From, missing)
	if p.attempts < maxViewChangeFetchAttempts {
		n.vcFetchTimerCh = resetOneShotTimer(&n.vcFetchTimer, 2*n.cfg.ViewChangeFetchDelay())
	}
}

func (n *Node) sendViewChangeFetch(view core.ViewID, primary int, wanted []int) {
	addr, ok := config.NodeAddr[primary]
	if !ok {
		n.log.Error("VC FETCH: no address for node %d", primary)
		return
	}
	msg := core.ViewChangeFetchMsg{ViewNumber: view, From: n.GetNodeID(), Wanted: append([]int(nil), wanted...)}
	payload, err := marshalDeterministic(transportpb.ViewChangeFetchToPB(msg))
	if err != nil {
		n.log.Error("VC FETCH: marshal failed: %v", err)
		return
	}
	signature := crypto.SignMessageEd25519(payload, n.encryptionKeyStore.GetPrivateKey())
	n.log.Info("VC FETCH sent view=(%d,%d) to=%d wanted=%v", view.Generation, view.Counter, primary, wanted)
	go n.messageHub.Send(core.MsgViewChangeFetchMessage, addr, msg, signature)
}

// handleViewChangeFetch is the primary's side: forward the referenced ViewChanges a
// replica lacks, with their original senders' signatures.
func (n *Node) handleViewChangeFetch(req core.ViewChangeFetchMsg) {
	addr, ok := config.NodeAddr[req.From]
	if !ok {
		return
	}
	served := n.viewChangesToServe(req)
	senders := make([]int, 0, len(served))
	for _, vc := range served {
		senders = append(senders, vc.ViewChangeMsg.From)
		go n.messageHub.ForwardViewChange(addr, vc)
	}
	if served != nil {
		n.log.Info("VC FETCH served view=(%d,%d) to=%d senders=%v", req.ViewNumber.Generation, req.ViewNumber.Counter, req.From, senders)
	}
}

// viewChangesToServe picks the ViewChanges to forward for req: only for this node's
// latest NewView, only ones it referenced, and at most maxViewChangeFetchReplies
// times per requester. nil means the request is not answered.
func (n *Node) viewChangesToServe(req core.ViewChangeFetchMsg) []*core.ViewChangeMsgSig {
	sent := n.newViewSent
	if sent == nil || !sent.view.Equal(req.ViewNumber) {
		n.log.Info("VC FETCH ignored from=%d view=(%d,%d): not my latest NewView", req.From, req.ViewNumber.Generation, req.ViewNumber.Counter)
		return nil
	}
	if sent.replies[req.From] >= maxViewChangeFetchReplies {
		n.log.Warn("VC FETCH ignored from=%d view=(%d,%d): already answered %d times", req.From, req.ViewNumber.Generation, req.ViewNumber.Counter, sent.replies[req.From])
		return nil
	}
	sent.replies[req.From]++
	served := make([]*core.ViewChangeMsgSig, 0, len(req.Wanted))
	for _, from := range req.Wanted {
		if vc, ok := sent.vcs[from]; ok {
			served = append(served, vc)
		}
	}
	return served
}

// recordNewViewSent keeps the ViewChanges this primary's NewView referenced, since
// pruneViewChangeState drops them from the log right after the NewView goes out.
func (n *Node) recordNewViewSent(view core.ViewID, vcs []*core.ViewChangeMsgSig) []core.ViewChangeRef {
	refs := make([]core.ViewChangeRef, 0, len(vcs))
	sent := &newViewSentState{
		view:    view,
		vcs:     make(map[int]*core.ViewChangeMsgSig, len(vcs)),
		replies: make(map[int]int),
	}
	for _, vc := range vcs {
		n.assert(vc.Digest != [32]byte{}, "view change from node %d for view (%d,%d) has no digest", vc.ViewChangeMsg.From, view.Generation, view.Counter)
		refs = append(refs, core.ViewChangeRef{From: vc.ViewChangeMsg.From, Digest: vc.Digest})
		sent.vcs[vc.ViewChangeMsg.From] = vc
	}
	n.newViewSent = sent
	return refs
}

// pruneNewViewRefState drops the alternate ViewChanges and the pending NewView for
// every view the predicate rejects. Called with the same rules as the log prune.
func (n *Node) pruneNewViewRefState(stale func(core.ViewID) bool) {
	for view := range n.viewChangeAlt {
		if stale(view) {
			delete(n.viewChangeAlt, view)
		}
	}
	if p := n.pendingNewView; p != nil && stale(p.msg.NewViewNumber) {
		n.clearPendingNewView()
	}
}

// for replica side vieachange alt and pending new view
// newviewsent is primary side and node pruned as someone might ask for fetc