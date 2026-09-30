package node

import (
	"github.com/michael112233/pbft/core"
)

// SelectAware is the Aware policy's view-change quorum hook, mirroring
// SelectRoundRobin: arm the new-view timer, and if this node is the Aware leader
// of forView, build the NewView.
func (n *Node) SelectAware(forView, view core.ViewID, currAction core.Action, path string) {
	n.log.Info("In select aware from path %s and starting new view timer for view (%d,%d)", path, forView.Generation, forView.Counter)
	n.startNewViewTimer()

	expectedLeader := n.awareLeader(forView)
	if expectedLeader == n.GetNodeID() {
		n.log.Info("Node %d is the aware leader for view (%d,%d); starting new view in %s", expectedLeader, forView.Generation, forView.Counter, path)
		n.newview()
	}
}

func (n *Node) awareLeader(view core.ViewID) int {
	return n.aware.leader(view, int(n.cfg.NodeNum))
}

// stores g-1 and wait to switch to g to apply g-1
// storeAwareAggregate holds the RTT vectors carried in the epoch aggregate for
// generation gen until the node leaves gen (applyPendingAwareAggregate). They are
// not applied on arrival: the node is still in gen until its learning-agent
// decision arrives, and applying now would put next-generation rows and
// candidates into this generation's ViewChange / NewView messages.
func (n *Node) storeAwareAggregate(gen uint64, sigs []core.EpochDataMsgSig) {
	rows := make([]core.AwareRow, 0, len(sigs))
	for _, sig := range sigs {
		msg := sig.EpochDataMsg
		// in fcrash crash node vector not received
		rows = append(rows, core.AwareRow{Node: msg.From, RTTms: msg.RTTms})
	}
	n.aware.pending[gen] = rows
}

// applyPendingAwareAggregate runs when the node leaves generation fromGen (from
// incrementGeneration, on every path into the next generation). It applies the
// stored aggregate for fromGen with Gen = fromGen+1 and derives candidates for
// fromGen+1. With no stored aggregate (it never arrived, or arrived after the
// node had already moved on and was dropped) it does nothing and catch-up from
// the ViewChange / NewView that pulled the node forward fills the gap.
func (n *Node) applyPendingAwareAggregate(fromGen uint64) {
	// aggregate of g-1 is taken from pending
	// pending called with g-1 before increasing gen in increment gen
	rows, ok := n.aware.takePending(fromGen)
	if !ok {
		return
	}
	gen := fromGen + 1
	for i := range rows {
		rows[i].Gen = gen
	}
	// create rows g and matrix g now and not at agg
	n.aware.applyRows(rows)
	// from rows dead now row is not gone but but its g become stale when we do matrix check
	n.computeAwareCandidates(gen, "aggregate")
}

func (n *Node) computeAwareCandidates(gen uint64, source string) {
	D, scores, cands := n.aware.computeCandidates(gen, int(n.cfg.NodeNum), n.fNodes, n.cfg.AwareStaleness(), n.cfg.AwareAlphaFraction(), n.cfg.AwareEpsilon())
	n.log.Info("AWARE: source=%s gen=%d rows=%v D=%v scores=%v candidates=%v", source, gen, n.aware.snapshotRows(), D, scores, cands)
}

// awareRowsForMessage returns the rows to attach to an outgoing ViewChange /
// NewView so a node pulled into the next generation can catch up.
func (n *Node) awareRowsForMessage(action core.Action) []core.AwareRow {
	// if action.Policy != core.PolicyAware {
	// 	return nil
	// }
	return n.aware.snapshotRows()
}

// maybeAdoptAwareRows lets a node that jumped to generation targetGen (f+1
// amplification or a higher-generation NewView) adopt the matrix it missed and
// derive the same candidate list the others computed for targetGen. The sender
// is in targetGen, so its rows are all <= targetGen and are exactly what its own
// candidates[targetGen] was built from.
//
// TODO(safety): trusting rows from a single message; a Byzantine sender can
// hand a lagging node an arbitrary matrix. Require f+1 matching rows or a
// committed marker before this is used outside the pilot.
func (n *Node) maybeAdoptAwareRows(targetGen uint64, action core.Action, rows []core.AwareRow) {
	if len(rows) == 0 || targetGen == 0 {
		return
	}
	// this called after increment gen
	// so inc gen calls apply and if have g-1 agg then already applied
	// so in catchup paths this become redundant
	// but in catchup path if dont have agg g-1 then then use this and sender row to build row and cand
	if _, ok := n.aware.candidates[targetGen]; ok {
		return
	} // got agg g-1 before the vc / nv that pulled us to g: incrementGeneration already applied it and built cand[g], so skip
	// agg g-1 not received (or dropped for arriving after the jump): no cand[g], so adopt the sender's rows and build cand[g] from them
	// both paths now build cand[g] from rows stamped <= g with the same gen, so they agree

	// multijump nv not handled correctly their both increment gen and maybe adopt can update candidates
	n.aware.applyRows(rows)
	n.computeAwareCandidates(targetGen, "catch-up")
}

// catchup path never used in pilot so far
