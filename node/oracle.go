package node

import (
	"context"
	"math/rand"
	"time"

	"github.com/michael112233/pbft/core"
)

// runOracleDecision stands in for the learning-agent RPC when oracle mode is
// enabled: it sleeps oracle_decision_delay_ms to mimic model latency and then
// feeds a locally computed decision straight into the node's learning-decision
// channel, so no real learning-agent process is needed.
func (n *Node) runOracleDecision(epoch uint64) {
	time.Sleep(n.cfg.OracleDecisionDelay())

	decision := core.LearningAgentDecision{
		NextProtocol: n.oracleAction(epoch),
		Generation:   epoch,
	}

	ctx, cancel := context.WithTimeout(context.Background(), learningAgentRPCTimeout)
	defer cancel()
	if err := n.ReceiveLearningAgentDecision(ctx, decision); err != nil {
		n.log.Error("Failed to queue oracle learning decision: %v", err)
	}
}

// oracleAction picks the next action for the given epoch out of the
// configured oracle_actions. In round-robin mode it cycles through the array
// one action per generation, indexing by epoch (not epoch-1) so the first
// decision skips index 0 - avoiding an immediate repeat of the node's
// FixedRoundRobin gen-1 default - while still cycling back to it once epoch
// is a multiple of len(actions). In random mode it draws from a source
// seeded by oracle_seed + epoch, so every node independently derives the
// same "random" pick for a given generation without any cross-node
// coordination.
func (n *Node) oracleAction(epoch uint64) core.Action {
	actions := n.cfg.OracleActionsEnum
	if len(actions) == 0 {
		return core.FixedRoundRobin
	}

	if n.cfg.OracleRandom {
		r := rand.New(rand.NewSource(n.cfg.OracleSeed + int64(epoch)))
		return actions[r.Intn(len(actions))]
	}

	return actions[epoch%uint64(len(actions))]
}
