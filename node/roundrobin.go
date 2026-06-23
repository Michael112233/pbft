package node

import (
	"time"

	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/crypto"
	"github.com/michael112233/pbft/transportpb"
)

func (n *Node) roundRobinVCTimeout() {
	// reqVote := false
	// n.log.Info("Round Robin VC timeout triggered for view %d", n.forView)
	grantVote := false

	n.checkpointMu.Lock()
	// n.log.Info("Inside lock")
	checkpointSeq := n.lastStableCheckpoint.seq
	checkpointDigest := n.lastStableCheckpoint.digest
	n.log.Info("Stable checkpoint which will be used for round robin vc is seq %d", checkpointSeq)
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
	// timeStart := time.Now()
	preparedCerts := n.createVCContent(checkpointSeq)
	// n.log.FeatureInfo("Time taken to create prepared certs for round robin vc is %d ms", time.Since(timeStart).Milliseconds())

	vcPayload := core.ViewChangeMsg{
		ViewNumber:          n.forView,
		CheckpointSeqNumber: checkpointSeq,
		CheckpointDigest:    checkpointDigest,
		CheckpointProof:     checkpointProof,
		From:                n.GetNodeID(),
		PreparedCerts:       preparedCerts,
		Type:                core.VCTypeRoundRobin,
		RoundRobinData: &core.RoundRobinVCData{
			GrantVote: grantVote,
		},
	}
	// n.viewMu.Unlock()
	timeStart := time.Now()
	pbMsg := transportpb.ViewChangeToPB(vcPayload)
	payloadBytes, err := marshalDeterministic(pbMsg)
	if err != nil {
		n.log.Error("Failed to marshal ViewChange message for signing: %v", err)
		// return
	}
	signature := crypto.SignMessageEd25519(payloadBytes, n.encryptionKeyStore.GetPrivateKey())
	n.log.FeatureInfo("Time taken to marshal and sign round robin vc message is %d ms", time.Since(timeStart).Milliseconds())
	// n.viewMu.Lock()

	vcMsg := &core.ViewChangeMsgSig{
		ViewChangeMsg: vcPayload,
		Signature:     signature,
	}
	n.viewChangeMsgsLog[n.forView] = append(n.viewChangeMsgsLog[n.forView], vcMsg)

	n.broadcastViewChange(vcPayload, signature)

	if len(n.viewChangeMsgsLog[n.forView]) == 2*n.fNodes+1 {
		// timer start
		n.log.Info("Starting new view timer from timeout dummy %d", n.forView)
		n.pbftTimerManager.startNewViewTimer(n)
		expectedLeader := n.primaryForView(n.forView, n.view)
		if expectedLeader == n.GetNodeID() {
			n.log.Info("I am the new leader for view %d in round robin vc, starting new view immediately (leader=%d) from timeout dummy", n.forView, expectedLeader)
			n.newView()
		}
	}
}

func (n *Node) HandleViewChangeRoundRobin(viewChange core.ViewChangeMsg, signature []byte) {
	n.log.FeatureInfo("Received vc msg from node %d for view %d", viewChange.From, viewChange.ViewNumber)
	n.viewMu.RLock()

	if viewChange.ViewNumber <= n.view {
		n.viewMu.RUnlock()
		return
	}
	n.log.Info("received vc as round robin type from node %d for view %d", viewChange.From, viewChange.ViewNumber)
	verifiedVC := n.verifyVC(viewChange)
	if !verifiedVC {
		n.viewMu.RUnlock()
		return
	}
	n.viewMu.RUnlock()
	n.viewMu.Lock()
	defer n.viewMu.Unlock()

	if viewChange.ViewNumber <= n.view {
		// n.viewMu.RUnlock()
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
		expectedLeader := n.primaryForView(n.forView, -1)
		if len(n.viewChangeMsgsLog[viewChange.ViewNumber]) == 2*n.fNodes+1 && expectedLeader == n.GetNodeID() {
			n.log.Info("Node %d is the round robin leader for view %d; starting new view immediately", expectedLeader, n.forView)

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
		if viewChange.ViewNumber == n.forView+1 && ((n.cfg.PerformanceTrigger && len(n.viewChangeMsgsLog[viewChange.ViewNumber]) == 1) || (!n.cfg.PerformanceTrigger && len(n.viewChangeMsgsLog[viewChange.ViewNumber]) == n.fNodes+1)) {
			n.pbftTimerManager.forceStopPBFTTimer()
			n.pbftTimerManager.stopNewViewTimer()
			n.log.Info(" Round Robin Triggering dummy view-change due to receiving higher view change message for view %d and my for view %d", viewChange.ViewNumber, n.forView)
			n.handleViewChangeTimeoutDummy()
		}
	} else {

		n.log.Error("Received view change for view %d which is lower than my for view %d, just adding to log", viewChange.ViewNumber, n.forView)
	}
	n.log.FeatureInfo("Done with vc msg from node %d for view %d", viewChange.From, viewChange.ViewNumber)

	// if > than for then and f+1 then start newview
	// if < than do nothing just add
}
