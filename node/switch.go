package node

import "github.com/michael112233/pbft/core"

// handleLearningAgentDecision is the event-loop-owned decision handler. The
// protocol transition will be implemented here once the learning policy is
// wired into the node state machine.
func (n *Node) handleLearningAgentDecision(decision core.LearningAgentDecision) {
	if n.log != nil {
		n.log.Debug(
			"received learning-agent decision for generation %d: %s",
			decision.Generation,
			core.ActiontoString(decision.NextProtocol),
		)
	}
}
