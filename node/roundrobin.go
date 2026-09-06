package node

import (
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/crypto"
	"github.com/michael112233/pbft/transportpb"
)

func (n *Node) PathString() string {
	path := ""
	if n.vcType == core.VCTypeElection {
		path = "election-VC"
	} else if n.vcType == core.VCTypeRoundRobin {
		path = "round-robin-VC"
	}
	return path
}

func (n *Node) VC() {
	checkpoint, proof, balances := n.GetLastStableCheckpointwithProofandBalances()
	path := n.PathString()

	n.log.Info("Stable checkpoint which will be used for %s vc is seq %d", path, checkpoint.seq)
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
		Type:                n.vcType,
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
	n.appendViewChangeIfNew(vcMsg)
	// when ente here due to f+1 then can hit 2f+1 by adding ours
	if n.vcType == core.VCTypeRoundRobin {
		n.maybeHandleViewChangeQuorum(n.forView, "round-robin-VC")
	} else if n.vcType == core.VCTypeElection {
		n.ElectionLogic(n.forView)
	}
}

func (n *Node) HandleViewChangeRoundRobin(viewChange core.ViewChangeMsg, signature []byte) {
	if viewChange.ViewNumber <= n.GetView() {
		return
	}
	// path := n.PathString()
	// n.log.Info("received vc as %s from node %d for view %d", path, viewChange.From, viewChange.ViewNumber)
	verifiedVC := n.verifyVC(viewChange)
	if !verifiedVC {
		n.log.Error("Failed to verify view change message from node %d for view %d", viewChange.From, viewChange.ViewNumber)
		return
	}

	if !n.appendViewChangeIfNew(&core.ViewChangeMsgSig{
		ViewChangeMsg: viewChange,
		Signature:     signature,
	}) {
		n.log.Debug("Ignoring duplicate view change from node %d for view %d", viewChange.From, viewChange.ViewNumber)
		return
	}

	viewChangeCount := n.uniqueViewChangeCount(viewChange.ViewNumber)

	if n.forView == viewChange.ViewNumber {
		if viewChange.Type == core.VCTypeRoundRobin {
			n.maybeHandleViewChangeQuorum(viewChange.ViewNumber, "round-robin-HandleVC")
		} else if viewChange.Type == core.VCTypeElection {
			n.ElectionLogic(viewChange.ViewNumber)
		}

	} else if n.forView < viewChange.ViewNumber {
		n.log.Info("Received view change for view %d which is higher than my for view %d, ", viewChange.ViewNumber, n.forView)
		if viewChange.ViewNumber == n.forView+1 && viewChangeCount == n.fNodes+1 {
			n.log.Info("Entering view change after receiving f+1 view-change messages for view %d", viewChange.ViewNumber)
			n.enterViewChange()
		} else if viewChange.ViewNumber > n.forView+1 {
			n.log.Warn("Received view change for view %d which is more than one ahead of my for view %d", viewChange.ViewNumber, n.forView)
		}
	} else {

		n.log.Error("Received view change for view %d which is lower than my for view %d, just adding to log", viewChange.ViewNumber, n.forView)
	}

}

func (n *Node) appendViewChangeIfNew(viewChange *core.ViewChangeMsgSig) bool {
	view := viewChange.ViewChangeMsg.ViewNumber
	// from := viewChange.ViewChangeMsg.From
	// One ViewChange per sender per view: createO / createOReplica pick the
	// highest-view prepared cert per seq across these messages and count them toward
	// the 2f+1 quorum, so a duplicate sender would double-count and could skew the
	// O-set.
	// for _, existing := range n.viewChangeMsgsLog[view] {
	// 	if existing != nil && existing.ViewChangeMsg.From == from {
	// 		return false
	// 	}
	// }

	n.viewChangeMsgsLog[view] = append(n.viewChangeMsgsLog[view], viewChange)
	return true
}

func (n *Node) uniqueViewChangeCount(view int64) int {

	return len(n.viewChangeMsgsLog[view])
}

func (n *Node) maybeHandleViewChangeQuorum(view int64, path string) {
	// if n.newViewTimerCh != nil {

	// 	return
	// }
	if n.uniqueViewChangeCount(view) == n.QuorumSize() {

		n.log.Info("Received 2f+1 view-change messages for view %d; starting new view timer in %s", view, path)
		// if path == "round-robin-VC" {
		// 	n.log.Warn("This path shouldnt happen 2f+1 shouldnt be from round robin vc")
		// }
		n.startNewViewTimer()

		expectedLeader := n.primaryForView(view, n.view)
		if expectedLeader == n.GetNodeID() {
			n.log.Info("Node %d is the round robin leader for view %d; starting new view in %s", expectedLeader, view, path)
			n.newview()
		}
	}
}
