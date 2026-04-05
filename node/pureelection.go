package node

import (
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/crypto"
	"github.com/michael112233/pbft/transportpb"
)

func (n *Node) electionVCTimeout() {
	reqVote := false
	grantVote := false

	if len(n.viewChangeMsgsLog[n.forView]) == 0 || n.split {
		n.log.Info("Length of view change log forView %d is len: %d and i am requesting vote and my current view is %d", n.forView, len(n.viewChangeMsgsLog[n.forView]), n.view)

		reqVote = true
		grantVote = true
		n.votedFor = n.GetNodeID()
		// n.viewChangeMsgsLog[n.forView] = make([]*core.ViewChangeMsgSig, 0)

		if _, exists := n.voteLog[n.forView]; !exists {
			n.voteLog[n.forView] = make([]int, 0)

		} else {
			n.log.Error("vote log already exists for view %d, this should not happen in dummy timeout handler", n.forView)
		}
		n.voteLog[n.forView] = append(n.voteLog[n.forView], n.GetNodeID())
	} else {
		n.log.Info("my forView is %d and my curr views is %d and first req vote is from node %d and last req vote is from node %d", n.forView, n.view, n.viewChangeMsgsLog[n.forView][0].ViewChangeMsg.From, n.viewChangeMsgsLog[n.forView][len(n.viewChangeMsgsLog[n.forView])-1].ViewChangeMsg.From)
		for _, vcMsgSig := range n.viewChangeMsgsLog[n.forView] {
			if vcMsgSig == nil {
				continue
			}
			if vcMsgSig.ViewChangeMsg.ElectionData.ReqVote {
				n.votedFor = vcMsgSig.ViewChangeMsg.From
				grantVote = true

				break
			}
		}
		if !grantVote {
			// will vote what if f first byz and dont send req vote
			n.log.Info("No req vote found in view change messages for view %d, not granting vote", n.forView)
		}

	}
	n.checkpointMu.Lock()
	checkpointSeq := n.lastStableCheckpoint.seq
	n.checkpointMu.Unlock()
	preparedCerts := n.createVCContent(checkpointSeq)

	vcPayload := core.ViewChangeMsg{
		ViewNumber:          n.forView,
		CheckpointSeqNumber: checkpointSeq,
		From:                n.GetNodeID(),
		PreparedCerts:       preparedCerts,
		Type:                core.VCTypeElection,
		ElectionData: &core.ElectionVCData{
			ReqVote:   reqVote,
			GrantVote: grantVote,
			GrantTo:   n.votedFor,
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
	}
}

func (n *Node) HandleViewChangeElection(viewChange core.ViewChangeMsg, signature []byte) {
	n.viewMu.Lock()
	defer n.viewMu.Unlock()

	if viewChange.ViewNumber <= n.view {
		return
	}
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
	verifiedvoteLog := false
	if n.votedFor == n.GetNodeID() && viewChange.ElectionData.GrantVote && viewChange.ElectionData.GrantTo == n.GetNodeID() {
		n.voteLog[viewChange.ViewNumber] = append(n.voteLog[viewChange.ViewNumber], viewChange.From)
		verifiedvoteLog = n.verifyVoteLog(n.voteLog[viewChange.ViewNumber])
	}

	if n.forView == viewChange.ViewNumber {
		if len(n.viewChangeMsgsLog[viewChange.ViewNumber]) == 2*n.fNodes+1 {

			// 	n.log.Info("Starting new view timer for view CHANGE %d", viewChange.ViewNumber)
			n.pbftTimerManager.startNewViewTimer(n)
		}
		if n.votedFor == n.GetNodeID() {
			if verifiedvoteLog {
				n.newView()
			}
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
	} else {
		if viewChange.ViewNumber == n.forView+1 && len(n.viewChangeMsgsLog[viewChange.ViewNumber]) >= n.fNodes+1 {
			n.pbftTimerManager.forceStopPBFTTimer()
			n.pbftTimerManager.stopNewViewTimer()
			n.handleViewChangeTimeoutDummy()
		}
		n.log.Info("Received view change for view %d which is lower than my for view %d, just adding to log", viewChange.ViewNumber, n.forView)
	}

	// if > than for then and f+1 then start newview
	// if < than do nothing just add
}
