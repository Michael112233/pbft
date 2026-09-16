package node

import (
	"time"

	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/crypto"
	"github.com/michael112233/pbft/transportpb"
)

// func (n *Node) PathString() string {
// 	path := ""
// 	if n.vcType == core.VCTypeElection {
// 		path = "election-VC"
// 	} else if n.vcType == core.VCTypeRoundRobin {
// 		path = "round-robin-VC"
// 	}
// 	return path
// }

// forView would be updated when enter here
func (n *Node) VC(forView, view core.ViewID, currAction core.Action) {
	checkpoint, proof, balances := n.GetLastStableCheckpointwithProofandBalances()

	n.log.Info("Stable checkpoint which will be used for vc is seq %d", checkpoint.seq)
	// Do something with the stable checkpoint and its proof
	preparedCerts := n.createVCContent(checkpoint.seq, forView, view)

	vcPayload := core.ViewChangeMsg{
		ViewNumber:          forView,
		CheckpointSeqNumber: checkpoint.seq,
		CheckpointDigest:    checkpoint.digest,
		CheckpointProof:     proof,
		CheckpointBalances:  balances,
		From:                n.GetNodeID(),
		PreparedCerts:       preparedCerts,
		Action:              currAction,
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
	// if n.vcType == core.VCTypeRoundRobin {
	// 	n.maybeHandleViewChangeQuorum(n.forView, "round-robin-VC")
	// } else if n.vcType == core.VCTypeElection {
	// 	n.ElectionLogic(n.forView)
	// }
	n.maybeHandleViewChangeQuorum(forView, view, currAction, "enter-VC-PATH")
}

func (n *Node) HandleViewChangeRoundRobin(viewChange core.ViewChangeMsg, signature []byte) {
	view := n.GetViewID()
	forView := n.GetForViewID()
	if viewChange.ViewNumber.LessThanOrEqual(view) {
		return
	}
	// path := n.PathString()
	// n.log.Info("received vc as %s from node %d for view %d", path, viewChange.From, viewChange.ViewNumber)
	verifiedVC := n.verifyVC(viewChange)
	if !verifiedVC {
		n.log.Error("Failed to verify view change message from node %d for view (%d,%d)", viewChange.From, viewChange.ViewNumber.Generation, viewChange.ViewNumber.Counter)
		return
	}

	if !n.appendViewChangeIfNew(&core.ViewChangeMsgSig{
		ViewChangeMsg: viewChange,
		Signature:     signature,
	}) {
		n.log.Debug("Ignoring duplicate view change from node %d for view (%d,%d)", viewChange.From, viewChange.ViewNumber.Generation, viewChange.ViewNumber.Counter)
		return
	}

	viewChangeCount := n.uniqueViewChangeCount(viewChange.ViewNumber)

	if forView.Equal(viewChange.ViewNumber) {
		// if viewChange.Type == core.VCTypeRoundRobin {
		// 	n.maybeHandleViewChangeQuorum(viewChange.ViewNumber, "round-robin-HandleVC")
		// } else if viewChange.Type == core.VCTypeElection {
		// 	n.ElectionLogic(viewChange.ViewNumber)
		// }
		n.assert(viewChange.Action == n.currAction, "Received view change with action %v which does not match current action %v", viewChange.Action, n.currAction)
		n.maybeHandleViewChangeQuorum(forView, view, n.currAction, "HandleVC")

	} else if forView.LessThan(viewChange.ViewNumber) { // can be less in generation or counter , generation happens slow so +2 there unlikely
		n.log.Info("Received view change for view (%d,%d), which is higher than my for view (%d,%d)", viewChange.ViewNumber.Generation, viewChange.ViewNumber.Counter, forView.Generation, forView.Counter)
		if viewChange.ViewNumber.Counter == forView.Counter+1 && viewChangeCount == n.fNodes+1 {
			n.log.Info("Entering view change after receiving f+1 view-change messages for view (%d,%d) which have a plus 1 counter", viewChange.ViewNumber.Generation, viewChange.ViewNumber.Counter)
			n.enterViewChange(true, n.currAction)

		} else if viewChange.ViewNumber.Generation > forView.Generation && viewChangeCount == n.fNodes+1 {
			n.assert(viewChange.ViewNumber.Generation == forView.Generation+1, "Received view change for view (%d,%d) which is more than one ahead in generation of my for view (%d,%d)", viewChange.ViewNumber.Generation, viewChange.ViewNumber.Counter, forView.Generation, forView.Counter)
			n.log.Info("Entering view change after receiving f+1 view-change messages for view (%d,%d) which have a higher generation", viewChange.ViewNumber.Generation, viewChange.ViewNumber.Counter)
			n.enterViewChange(false, viewChange.Action) // pass higher gen to catchup
		} else if viewChange.ViewNumber.Counter > forView.Counter+1 {
			n.log.Warn("Received view change for view (%d,%d) which is more than one ahead in counter of my for view (%d,%d)", viewChange.ViewNumber.Generation, viewChange.ViewNumber.Counter, forView.Generation, forView.Counter)
		}
	} else {
		n.log.Error("Received view change for view (%d,%d) which is lower than my for view (%d,%d), just adding to log", viewChange.ViewNumber.Generation, viewChange.ViewNumber.Counter, forView.Generation, forView.Counter)

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

func (n *Node) uniqueViewChangeCount(view core.ViewID) int {

	return len(n.viewChangeMsgsLog[view])
}

func (n *Node) maybeHandleViewChangeQuorum(forView, view core.ViewID, currAction core.Action, path string) {

	if n.uniqueViewChangeCount(forView) == n.QuorumSize() {
		n.log.Info("Received 2f+1 view-change messages for view (%d,%d); and path: %s and policy: %s", forView, view, path, currAction.Policy)
		switch currAction.Policy {
		case core.PolicyRoundRobin:
			n.SelectRoundRobin(forView, view, currAction, path)
		case core.PolicyElection:
			// n.ElectionLogic(forView, view, currAction, path)
			n.SelectElection(forView, view, currAction, path)
		default:
			n.log.Error("Unknown policy %s for view change quorum handling", currAction.Policy)
		}
	}
}

func (n *Node) SelectRoundRobin(forView, view core.ViewID, currAction core.Action, path string) {

	n.log.Info("In select round robin from path %s and starting new view timer for view (%d,%d)", path, forView.Generation, forView.Counter)
	n.startNewViewTimer()

	expectedLeader := n.primaryForView(forView.Counter)
	if expectedLeader == n.GetNodeID() {
		n.log.Info("Node %d is the round robin leader for view (%d,%d); starting new view in %s", expectedLeader, forView.Generation, forView.Counter, path)
		n.newview()
	}
}

func (n *Node) SelectElection(forView, view core.ViewID, currAction core.Action, path string) {
	n.log.Info("In select election from path %s and starting new view timer for view (%d,%d)", path, forView.Generation, forView.Counter)
	time.Sleep(100 * time.Millisecond) // Simulate election
	n.startNewViewTimer()
	expectedLeader := 3
	if expectedLeader == n.GetNodeID() {
		n.log.Info("Node %d is the election leader for view (%d,%d); starting new view in %s", expectedLeader, forView.Generation, forView.Counter, path)
		n.newview()
	}
}

func (n *Node) primaryForView(counter uint64) int {

	if counter == 0 {
		return 1
	}
	return int((counter-1)%uint64(n.cfg.NodeNum)) + 1
}
