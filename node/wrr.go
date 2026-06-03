package node

import (
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/crypto"
	"github.com/michael112233/pbft/transportpb"
)

func (n *Node) WRRVCTimeout() {
	// reqVote := false

	n.checkpointMu.Lock()
	checkpointSeq := n.lastStableCheckpoint.seq
	checkpointDigest := n.lastStableCheckpoint.digest
	n.log.Info("Stable checkpoint which will be used for wrr vc is seq %d", checkpointSeq)
	key := checkpoint{
		seq:    checkpointSeq,
		digest: checkpointDigest,
	}
	checkpointData, exists := n.checkpoints[key]
	if !exists {
		n.log.Error("No checkpoint data found for stable checkpoint seq %d and digest %x", checkpointSeq, checkpointDigest)
	}
	checkpointProof := make([]core.CheckpointMsgSig, 0, len(checkpointData.votes))
	for _, msg := range checkpointData.votes {
		checkpointProof = append(checkpointProof, msg)
	}
	n.checkpointMu.Unlock()
	preparedCerts := n.createVCContent(checkpointSeq)
	throughput := n.CurrentViewThroughput(n.view)
	if throughput <= 0 {
		n.log.Error("Throughput is non-positive/zero for n.view %d and n.forView %d", n.view, n.forView)
	}

	vcPayload := core.ViewChangeMsg{
		ViewNumber:          n.forView,
		CheckpointSeqNumber: checkpointSeq,
		CheckpointDigest:    checkpointDigest,
		CheckpointProof:     checkpointProof,
		From:                n.GetNodeID(),
		PreparedCerts:       preparedCerts,
		Type:                core.VCTypeWRR,
		WRRData: &core.WRRVCData{
			Throughput: throughput,
		},
	}
	pbMsg := transportpb.ViewChangeToPB(vcPayload)
	payloadBytes, err := marshalDeterministic(pbMsg)
	if err != nil {
		n.log.Error("Failed to marshal ViewChange message for signing: %v", err)
		// return
	}
	signature := crypto.SignMessageEd25519(payloadBytes, n.encryptionKeyStore.GetPrivateKey())

	vcMsg := &core.ViewChangeMsgSig{
		ViewChangeMsg: vcPayload,
		Signature:     signature,
	}
	n.viewChangeMsgsLog[n.forView] = append(n.viewChangeMsgsLog[n.forView], vcMsg)

	go n.broadcastViewChange(vcPayload, signature)

	if len(n.viewChangeMsgsLog[n.forView]) == 2*n.fNodes+1 {
		// timer start
		n.log.Info("Starting new view timer from timeout dummy %d", n.forView)
		n.pbftTimerManager.startNewViewTimer(n)
		expectedLeader := n.primaryForView(n.forView, n.view)
		if expectedLeader == n.GetNodeID() {
			n.log.Info("I am the new leader for view %d in wrr vc, starting new view immediately (leader=%d)", n.forView, expectedLeader)
			n.newView()
		}
	}
}

func (n *Node) HandleViewChangeWRR(viewChange core.ViewChangeMsg, signature []byte) {
	n.viewMu.Lock()
	defer n.viewMu.Unlock()

	if viewChange.ViewNumber <= n.view {
		return
	}
	n.log.Info("received vc as wrr type from node %d for view %d and throughput %f", viewChange.From, viewChange.ViewNumber, viewChange.WRRData.Throughput)
	verifiedVC := n.verifyVC(viewChange)
	if !verifiedVC {
		return
	}

	// check for dup attack in queue
	n.viewChangeMsgsLog[viewChange.ViewNumber] = append(n.viewChangeMsgsLog[viewChange.ViewNumber],
		&core.ViewChangeMsgSig{
			ViewChangeMsg: viewChange,
			Signature:     signature,
		})

	if n.forView == viewChange.ViewNumber {
		if len(n.viewChangeMsgsLog[viewChange.ViewNumber]) == 2*n.fNodes+1 {

			n.log.Info("Starting new view timer for view CHANGE %d", viewChange.ViewNumber)
			n.pbftTimerManager.startNewViewTimer(n)
		}
		expectedLeader := n.primaryForView(n.forView, n.view)
		if len(n.viewChangeMsgsLog[viewChange.ViewNumber]) == 2*n.fNodes+1 && expectedLeader == n.GetNodeID() {
			n.log.Info("I node id %d is the wrr leader for view %d; starting new view immediately", expectedLeader, n.forView)

			n.newView()

		}

		// } else {
		// 	n.log.Info(" couldnt get enough votes for view change view %d, starting new view timer", viewChange.ViewNumber)
		// 	n.pbftTimerManager.startNewViewTimer(n)
		// }
		// } else {
		// 	// start timer
		// 	n.log.Info("Starting new view timer for view CHANGE %d", viewChange.ViewNumber)
		// 	n.pbftTimerManager.startNewViewTimer(n)
		// }

	} else if n.forView < viewChange.ViewNumber {
		n.log.Info("Received view change for view %d which is higher than my for view %d, ", viewChange.ViewNumber, n.forView)
		if viewChange.ViewNumber == n.forView+1 && len(n.viewChangeMsgsLog[viewChange.ViewNumber]) == 1 {
			n.pbftTimerManager.forceStopPBFTTimer()
			n.pbftTimerManager.stopNewViewTimer()
			n.log.Info(" wrr Triggering dummy view-change due to receiving higher view change message for view %d and my for view %d", viewChange.ViewNumber, n.forView)
			n.handleViewChangeTimeoutDummy()
		}
	} else {

		n.log.Info("Received view change for view %d which is lower than my for view %d, just adding to log", viewChange.ViewNumber, n.forView)
	}

	// if > than for then and f+1 then start newview
	// if < than do nothing just add
}
