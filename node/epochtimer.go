package node

import (
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/crypto"
	"github.com/michael112233/pbft/transportpb"
)

// epochAggregatorNodeID is the node every other node sends its epoch data to.
// Experiment scaffolding, like scenarioNetemNodeID. Defined in config so the
// NetworkDelayFCrash validation that refuses to crash it stays in step.
const epochAggregatorNodeID = config.EpochAggregatorNodeID

// startEpochTimer starts the epoch timer at seq 1 and, with timer.epoch_grid,
// records the grid anchor. It is only ever touched from the node event loop, so
// no locking is needed.
func (n *Node) startEpochTimer() {
	gen := n.GetForViewID().Generation
	n.epochAnchor = time.Now()
	n.epochAnchorGen = gen
	n.armEpochTimer(gen)
}

// resetEpochTimer arms the epoch timer for gen, the generation the node is
// entering (called from incrementGeneration).
func (n *Node) resetEpochTimer(gen uint64) {
	n.armEpochTimer(gen)
}

func (n *Node) armEpochTimer(gen uint64) {
	period := n.cfg.EpochTimer()
	if !n.cfg.Timer.EpochGrid || n.epochAnchor.IsZero() {
		n.epochTimerCh = resetOneShotTimer(&n.epochTimer, period)
		return
	}
	delay, late := epochGridDelay(n.epochAnchor, n.epochAnchorGen, gen, period, time.Now())
	if late > 0 {
		// Only if a decision took longer than a whole epoch: fire once now, so this
		// generation is short and the next one is back on the grid.
		n.log.Warn("EPOCH GRID: deadline for generation %d missed by %v; firing now", gen, late)
	}
	n.epochTimerCh = resetOneShotTimer(&n.epochTimer, delay)
}

// epochGridDelay returns how long from now until generation gen's epoch timer is
// due on the grid anchored at (anchor, anchorGen): the anchor generation fires one
// period after the anchor, each later generation one period after that. A
// deadline already past gives delay 0 and how late it is.
func epochGridDelay(anchor time.Time, anchorGen, gen uint64, period time.Duration, now time.Time) (delay, late time.Duration) {
	if gen < anchorGen { // generations never decrease; guards the unsigned subtraction
		gen = anchorGen
	}
	deadline := anchor.Add(time.Duration(gen-anchorGen+1) * period)
	delay = deadline.Sub(now)
	if delay < 0 {
		return 0, -delay
	}
	return delay, 0
}

func (n *Node) stopEpochTimer() {
	stopOneShotTimer(n.epochTimer)
	n.epochTimerCh = nil
}

func (n *Node) handleEpochTimerTimeout() {
	n.stopEpochTimer()
	forView := n.GetForViewID()
	n.log.Info("Epoch timer fired for view (%d,%d), sending epoch data message", forView.Generation, forView.Counter)
	epochMsg := n.CreateEpochMsg(forView.Generation)
	if n.cfg.LatencyProbe {
		epochMsg.RTTms = n.rttVec.snapshot(int(n.cfg.NodeNum), n.GetNodeID(), time.Now())
		n.log.Info("AWARE: sending rtt vector gen=%d rtt_ms=%v", forView.Generation, epochMsg.RTTms)
	}
	payloadBytes, err := marshalDeterministic(transportpb.EpochDataMsgToPB(*epochMsg))
	if err != nil {
		n.log.Error("Failed to marshal epoch data message for signing: %v", err)
		return
	}
	signature := crypto.SignMessageEd25519(payloadBytes, n.encryptionKeyStore.GetPrivateKey())

	// The aggregator hands its own epoch data straight to the epoch manager rather
	// than sending it to itself over gRPC. This runs on the event loop, the same
	// goroutine the network path would have delivered on, so the handler sees no
	// difference. Going over the network meant the aggregator had to dial itself,
	// and that first-time dial cost a full connection setup under netem delay -
	// long enough for the grace timer to fire and drop the aggregator's own row
	// from the first generation's aggregate.
	if n.GetNodeID() == epochAggregatorNodeID {
		n.HandleEpochDataMsg(*epochMsg, signature)
		return
	}

	target, ok := config.NodeAddr[epochAggregatorNodeID]
	if !ok || target == "" {
		n.log.Error("Cannot send epoch data message: node %d address is not configured", epochAggregatorNodeID)
		return
	}
	go n.messageHub.Send(core.MsgEpochDataMessage, target, *epochMsg, signature)
}
