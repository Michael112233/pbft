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

// applyAwareAggregate folds the RTT vectors carried in an epoch aggregate for
// generation gen into the matrix, then derives the candidate list for gen+1.
func (n *Node) applyAwareAggregate(gen uint64, sigs []core.EpochDataMsgSig) {
	rows := make([]core.AwareRow, 0, len(sigs))
	for _, sig := range sigs {
		msg := sig.EpochDataMsg
		rows = append(rows, core.AwareRow{Node: msg.From, Gen: gen, RTTms: msg.RTTms})
	}
	n.aware.applyRows(rows)
	n.computeAwareCandidates(gen+1, gen, "aggregate")
}

func (n *Node) computeAwareCandidates(targetGen, dataGen uint64, source string) {
	D, scores, cands := n.aware.computeCandidates(targetGen, dataGen, int(n.cfg.NodeNum), n.fNodes, n.cfg.AwareStaleness(), n.cfg.AwareAlphaFraction(), n.cfg.AwareEpsilon())
	n.log.Info("AWARE: source=%s gen=%d rows=%v D=%v scores=%v candidates=%v", source, targetGen, n.aware.snapshotRows(), D, scores, cands)
}

// awareRowsForMessage returns the rows to attach to an outgoing ViewChange /
// NewView so a node pulled into the next generation can catch up.
func (n *Node) awareRowsForMessage(action core.Action) []core.AwareRow {
	if action.Policy != core.PolicyAware {
		return nil
	}
	return n.aware.snapshotRows()
}

// maybeAdoptAwareRows lets a node that jumped to generation targetGen (f+1
// amplification or a higher-generation NewView) adopt the matrix it missed and
// derive the same candidate list the others computed from generation targetGen-1.
//
// TODO(safety): trusting rows from a single message; a Byzantine sender can
// hand a lagging node an arbitrary matrix. Require f+1 matching rows or a
// committed marker before this is used outside the pilot.
func (n *Node) maybeAdoptAwareRows(targetGen uint64, action core.Action, rows []core.AwareRow) {
	if action.Policy != core.PolicyAware || len(rows) == 0 || targetGen == 0 {
		return
	}
	if _, ok := n.aware.candidates[targetGen]; ok {
		return
	}
	n.aware.applyRows(rows)
	n.computeAwareCandidates(targetGen, targetGen-1, "catch-up")
}
