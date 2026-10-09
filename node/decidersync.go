package node

import "github.com/michael112233/pbft/core"

// Every generation's learning data must reach the decider (learning agent or
// oracle) exactly once and in order: the agent requires consecutive sequence ids
// and rejects everything after a gap. A node pulled into generation g+1 by f+1
// ViewChanges or a NewView before g's aggregate reached it drops that aggregate as
// stale, so without this it never sends g. That happened in
// results/old_multiscenario/run2new_20260924_022030: node 1 left generation 4 18 ms
// before gen 4's aggregate arrived, and its agent rejected all 709 later generations.

// sendEpochToDecider hands generation gen's learning data to the decider and
// records that gen has been sent. Loop-owned.
func (n *Node) sendEpochToDecider(gen uint64, action core.Action, d core.EpochData) {
	n.deciderSentGen = gen
	go n.SendLearningDataToAgent(gen, action, d.Throughput, d.ProposalInterval, d.VCRate, d.InactiveNodes)
}

// fillDeciderGap runs as the node leaves generation leaving (incrementGeneration).
// If leaving's learning data was never sent — the node caught up before its
// aggregate arrived — it sends placeholder data so the agent stays in sequence.
// The agent ignores the node's numbers (its state and reward are synthetic), and its
// decision for leaving reaches a node already in the next generation, which drops
// it (handleLearningAgentDecision). Loop-owned; the sends run in order on one
// goroutine.
func (n *Node) fillDeciderGap(leaving uint64, action core.Action) {
	gens := unsentGenerations(n.deciderSentGen, leaving)
	if len(gens) == 0 {
		return
	}
	n.log.Warn("DECIDER SYNC: leaving generation %d without having sent its learning data (caught up before its aggregate); sending placeholder data for generations %v", leaving, gens)
	n.deciderSentGen = leaving
	go func() {
		for _, g := range gens {
			n.SendLearningDataToAgent(g, action, 0, 0, 0, 0)
		}
	}()
}

// unsentGenerations lists the generations after sent up to and including leaving.
func unsentGenerations(sent, leaving uint64) []uint64 {
	var gens []uint64
	for g := sent + 1; g <= leaving; g++ {
		gens = append(gens, g)
	}
	return gens
}
