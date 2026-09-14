package node

import (
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/crypto"
	"github.com/michael112233/pbft/transportpb"
)

const epochTimerInterval = 60 * time.Second

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
	n.log.Info("Epoch timer fired for view %d", n.view)
	epochMsg := n.CreateEpochMsg()
	payloadBytes, err := marshalDeterministic(transportpb.EpochDataMsgToPB(*epochMsg))
	if err != nil {
		n.log.Error("Failed to marshal epoch data message for signing: %v", err)
		return
	}
	signature := crypto.SignMessageEd25519(payloadBytes, n.encryptionKeyStore.GetPrivateKey())

	target, ok := config.NodeAddr[4]
	if !ok || target == "" {
		n.log.Error("Cannot send epoch data message: node 4 address is not configured")
		return
	}
	go n.messageHub.Send(core.MsgEpochDataMessage, target, *epochMsg, signature)
}
