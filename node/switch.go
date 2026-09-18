package node

import "github.com/michael112233/pbft/core"

// handleLearningAgentDecision is the event-loop-owned decision handler. The
// protocol transition will be implemented here once the learning policy is
// wired into the node state machine.
func (n *Node) handleLearningAgentDecision(decision core.LearningAgentDecision) {
	forView := n.GetForViewID()
	if decision.Generation != forView.Generation {
		n.assert(decision.Generation <= forView.Generation, "Received learning-agent decision for generation %d which is greater than my for view generation %d", decision.Generation, forView.Generation)
		if decision.Generation < forView.Generation {
			n.log.Info("Received learning-agent decision for generation %d which is less than my for view generation %d already caught up, ignoring", decision.Generation, forView.Generation)
		}
		return
	}

	n.log.Info(
		"received learning-agent decision for generation %d: %s",
		decision.Generation,
		core.ActiontoString(decision.NextProtocol),
	)
	forView = n.incrementGeneration(decision.NextProtocol)

	n.enterViewChange(forView)

}

// increment gen atomic increases gen, update action reset epoch timer
// so even if epoch timer expire it should be of last gen we shouldnt have premature epoch timer expiry for new gen
