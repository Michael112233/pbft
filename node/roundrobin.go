package node

import (
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/crypto"
	"github.com/michael112233/pbft/transportpb"
)

func (n *Node) roundRobinVC() {
	checkpoint, proof, balances := n.GetLastStableCheckpointwithProofandBalances()
	n.log.Info("Stable checkpoint which will be used for round robin vc is seq %d", checkpoint.seq)
	// Do something with the stable checkpoint and its proof
	preparedCerts := n.createVCContent(checkpoint.seq)
	grantVote := false
	vcPayload := core.ViewChangeMsg{
		ViewNumber:          n.forView,
		CheckpointSeqNumber: checkpoint.seq,
		CheckpointDigest:    checkpoint.digest,
		CheckpointProof:     proof,
		CheckpointBalances:  balances,
		From:                n.GetNodeID(),
		PreparedCerts:       preparedCerts,
		Type:                core.VCTypeRoundRobin,
		RoundRobinData: &core.RoundRobinVCData{
			GrantVote: grantVote,
		},
	}

	pbMsg := transportpb.ViewChangeToPB(vcPayload)
	payloadBytes, err := marshalDeterministic(pbMsg)
	if err != nil {
		n.log.Error("Failed to marshal ViewChange message for signing: %v", err)
		// return
	}
	signature := crypto.SignMessageEd25519(payloadBytes, n.encryptionKeyStore.GetPrivateKey())
	// n.log.FeatureInfo("Time taken to marshal and sign round robin vc message is %d ms", time.Since(timeStart).Milliseconds())
	// n.viewMu.Lock()

	vcMsg := &core.ViewChangeMsgSig{
		ViewChangeMsg: vcPayload,
		Signature:     signature,
	}
	n.asyncBroadCast(core.MsgViewChangeMessage, vcPayload, signature)
	n.viewChangeMsgsLog[n.forView] = append(n.viewChangeMsgsLog[n.forView], vcMsg)

	if len(n.viewChangeMsgsLog[n.forView]) == n.QuorumSize() {
		// this path shouldnt run with f+1 path
		n.log.Warn("Starting new view timer from roundrobin vc %d", n.forView)
		expectedLeader := n.primaryForView(n.forView, n.view)
		if expectedLeader == n.GetNodeID() {
			n.log.Info("I am the new leader for view %d in round robin vc, starting new view immediately (leader=%d) from timeout dummy", n.forView, expectedLeader)
			n.newview()
		}

	}
}

func (n *Node) HandleViewChangeRoundRobin(viewChange core.ViewChangeMsg, signature []byte) {
	if viewChange.ViewNumber <= n.GetView() {
		return
	}
	n.log.Info("received vc as round robin type from node %d for view %d", viewChange.From, viewChange.ViewNumber)
	verifiedVC := n.verifyVC(viewChange)
	if !verifiedVC {
		n.log.Error("Failed to verify view change message from node %d for view %d", viewChange.From, viewChange.ViewNumber)
		return
	}

	n.viewChangeMsgsLog[viewChange.ViewNumber] = append(n.viewChangeMsgsLog[viewChange.ViewNumber],
		&core.ViewChangeMsgSig{
			ViewChangeMsg: viewChange,
			Signature:     signature,
		})

	if n.forView == viewChange.ViewNumber {
		if len(n.viewChangeMsgsLog[viewChange.ViewNumber]) == n.QuorumSize() {

			n.log.Info("Starting new view timer for view CHANGE %d", viewChange.ViewNumber)

		}
		expectedLeader := n.primaryForView(n.forView, -1)
		if len(n.viewChangeMsgsLog[viewChange.ViewNumber]) == n.QuorumSize() && expectedLeader == n.GetNodeID() {
			n.log.Info("Node %d is the round robin leader for view %d; starting new view immediately from handle view change", expectedLeader, n.forView)

			n.newview()

		}

	} else if n.forView < viewChange.ViewNumber {
		n.log.Info("Received view change for view %d which is higher than my for view %d, ", viewChange.ViewNumber, n.forView)
		if viewChange.ViewNumber == n.forView+1 && ((n.cfg.PerformanceTrigger && len(n.viewChangeMsgsLog[viewChange.ViewNumber]) == 1) || (!n.cfg.PerformanceTrigger && len(n.viewChangeMsgsLog[viewChange.ViewNumber]) == n.fNodes+1)) {
			// timers
			n.log.Info(" Round Robin Triggering view-change timout dummy due to receiving higher view change message for view %d and my for view %d", viewChange.ViewNumber, n.forView)
			n.enterViewChange()
		}
	} else {

		n.log.Error("Received view change for view %d which is lower than my for view %d, just adding to log", viewChange.ViewNumber, n.forView)
	}

}
