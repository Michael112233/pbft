package node

import (
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/crypto"
	"github.com/michael112233/pbft/transportpb"
)

const epochTimerInterval = 45 * time.Second

// epochAggregatorNodeID is the node every other node sends its epoch data to.
// Experiment scaffolding, like scenarioNetemNodeID. Defined in config so the
// NetworkDelayFCrash validation that refuses to crash it stays in step.
const epochAggregatorNodeID = config.EpochAggregatorNodeID

// startEpochTimer starts the epoch timer. It is only ever touched from the
// node event loop, so no locking is needed.
func (n *Node) startEpochTimer() {
	n.epochTimerCh = resetOneShotTimer(&n.epochTimer, epochTimerInterval)
}

func (n *Node) resetEpochTimer() {
	n.epochTimerCh = resetOneShotTimer(&n.epochTimer, epochTimerInterval)
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
