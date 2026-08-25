package node

import (
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/crypto"
	"github.com/michael112233/pbft/transportpb"
	"github.com/michael112233/pbft/vr"
)

type electionVDFResult struct {
	view       int64
	seed       []byte
	delaySteps uint64
	vrfProof   []byte
	beta       []byte
	y          []byte
	vdfProof   []byte
	err        error
}

type ElectionManager struct {
	votedFor           map[int64]int
	reqVoteBuffer      map[int64][]core.RequestVoteMsg
	electionVDFWorkers sync.WaitGroup
	collectVotes       map[int64]map[int]struct{} // view -> nodeID -> struct{}
}

func NewElectionManager() *ElectionManager {
	return &ElectionManager{
		votedFor:      make(map[int64]int),
		reqVoteBuffer: make(map[int64][]core.RequestVoteMsg),
		collectVotes:  make(map[int64]map[int]struct{}),
	}
}

func (n *Node) ElectionLogic(view int64) {
	if n.forView != view {
		return
	}
	if n.uniqueViewChangeCount(view) != n.QuorumSize() {
		return
	}
	if _, alreadyVoted := n.electionManager.votedFor[view]; alreadyVoted {
		// this can happen if req vote come after for view equal and have f+1 vc
		n.log.Warn("Its fine that already voted for but reordering happend before 2f+1 threshold we voted for view %d", view)
		return
	}
	// if req vote received after f+1 then we may have voted but if even before f+1 it sits in buffer and we extract at 2f+1
	// or maybe in buffer due to way beyon f+1
	if n.electionManager.reqVoteBuffer[view] != nil {
		n.log.Warn("Processing buffered request vote messages for view %d", view)
		reqVote := n.electionManager.reqVoteBuffer[view][0]
		n.electionManager.reqVoteBuffer[view] = n.electionManager.reqVoteBuffer[view][1:]
		success := n.HandleRequestVoteMsg(reqVote, nil, "local buffered")
		if !success {
			n.log.Error("Failed to process buffered request vote message for view %d", view)
		} else {
			return
		}

	}

	seed := []byte(fmt.Sprintf("view-%d", view))
	// Eventually this seed will be the threshold signature assembled from the
	// secret shares carried by the view-change messages.
	privateVRFKey := n.encryptionKeyStore.GetPrivateVRFKey()
	vrfProof, beta, err := vr.CreateProofAndBeta(privateVRFKey, seed)
	if err != nil {
		n.log.Error("Error creating proof and beta for view %d: %v", view, err)
		return
	}

	randNumber, err := vr.NumberFromBeta(beta, n.MinVDFDelay(), n.MaxVDFDelay())
	if err != nil {
		n.log.Error("Failed to generate VDF delay for view %d: %v", view, err)
		return
	}

	delaySteps := uint64(randNumber)
	modulus := n.encryptionKeyStore.GetVDFModulus()

	n.electionManager.electionVDFWorkers.Add(1)

	go n.evalElectionVDF(view, seed, delaySteps, modulus, vrfProof, beta)
}

func (n *Node) evalElectionVDF(
	view int64,
	seed []byte,
	delaySteps uint64,
	modulus *big.Int,
	vrfProof []byte,
	beta []byte,
) {
	defer n.electionManager.electionVDFWorkers.Done()
	timeStart := time.Now()
	y, vdfProof, err := vr.EvalVDF(seed, modulus, delaySteps)
	timeElapsed := time.Since(timeStart)
	n.log.Debug("VDF evaluation for view %d completed in %s", view, timeElapsed)
	result := electionVDFResult{
		view:       view,
		seed:       append([]byte(nil), seed...),
		delaySteps: delaySteps,
		vrfProof:   append([]byte(nil), vrfProof...),
		beta:       append([]byte(nil), beta...),
		y:          y,
		vdfProof:   vdfProof,
		err:        err,
	}

	select {
	case n.electionVDFResultCh <- result:
	case <-n.eventLoopStopCh:
	}
}

func (n *Node) handleElectionVDFResult(result electionVDFResult) {
	if result.err != nil {
		n.log.Error("VDF evaluation failed for view %d: %v", result.view, result.err)
		return
	}
	if result.view <= n.view {
		n.log.Debug("Already in view %d, ignoring completed VDF for election", result.view)
		return
		// defensive check because if moved to new view would have already voted
		// or received new view
	}
	if n.forView != result.view {
		n.assert(result.view < n.forView, "VDF result view %d should be less than current forView %d", result.view, n.forView)
		n.log.Debug("Ignoring completed VDF for stale view %d; current pending view is %d", result.view, n.forView)
		return
	}

	if _, alreadyVoted := n.electionManager.votedFor[result.view]; alreadyVoted {
		n.log.Debug("Already voted for view %d, ignoring completed VDF", result.view)
		return
	}
	n.electionManager.votedFor[result.view] = n.GetNodeID()
	n.electionManager.collectVotes[result.view] = make(map[int]struct{})
	n.electionManager.collectVotes[result.view][n.GetNodeID()] = struct{}{}
	n.startNewViewTimer()

	n.asyncBroadcastRequestVote(result)
}

func (n *Node) HandleRequestVoteMsg(reqVote core.RequestVoteMsg, signature []byte, path string) bool {
	if reqVote.ViewNumber <= n.GetView() || reqVote.ViewNumber < n.forView {
		return false
	}
	// we buffer wehn > for view which is too out of order
	// or buffer when when vcs not greater f+1 yet but thst buffer is hadnled when reach 2f+1
	if reqVote.ViewNumber > n.forView {
		// either i didnt collected f+1 vc so far or my timer hasnt expired
		n.log.Error("Received request vote for view %d which is higher than my for view %d, buffering and path is %s", reqVote.ViewNumber, n.forView, path)
		n.electionManager.reqVoteBuffer[reqVote.ViewNumber] = append(n.electionManager.reqVoteBuffer[reqVote.ViewNumber], reqVote)
		return false
	}
	// only equal to forview handled

	if len(n.viewChangeMsgsLog[reqVote.ViewNumber]) <= n.fNodes+1 {
		n.electionManager.reqVoteBuffer[reqVote.ViewNumber] = append(n.electionManager.reqVoteBuffer[reqVote.ViewNumber], reqVote)
		n.log.Error("Way to fast for request vote to happen and path is %s", path)
		return false
	}

	if _, voted := n.electionManager.votedFor[reqVote.ViewNumber]; voted {
		n.log.Debug("Already voted for view %d, ignoring request vote from node %d and path is %s", reqVote.ViewNumber, reqVote.From, path)
		return false
	}
	// can buffer hereif 2f+1 vc not complete and replay buffer from 2f+1 part but req vote is self contain so can grant before too
	verified := n.VerifyVRFVDF(reqVote)
	if !verified {
		n.log.Error("Failed to verify request vote message from node %d for view %d and path is %s", reqVote.From, reqVote.ViewNumber, path)
		return false
	}
	// grant vote
	n.electionManager.votedFor[reqVote.ViewNumber] = reqVote.From

	go n.asyncGrantVote(reqVote.ViewNumber, reqVote.From)
	n.startNewViewTimer() // start timer once grant vote or candidate
	return true

}

func (n *Node) VerifyVRFVDF(reqVote core.RequestVoteMsg) bool {

	publicVRFKey, exists := n.encryptionKeyStore.GetPublicVRFKey(reqVote.From)
	if !exists {
		n.log.Error("Public VRF key for node %d not found", reqVote.From)
		return false
	}

	verifiedBeta, err := vr.VerifyProof(publicVRFKey, reqVote.Seed, reqVote.VRFProof)
	if err != nil {
		n.log.Error("Error verifying VRF proof from node %d: %v", reqVote.From, err)
		return false
	}

	randNumber, err := vr.NumberFromBeta(verifiedBeta, n.MinVDFDelay(), n.MaxVDFDelay())
	if err != nil {
		n.log.Error("Failed to generate VDF delay from beta for node %d: %v", reqVote.From, err)
		return false
	}
	delaySteps := uint64(randNumber)
	if delaySteps != reqVote.DelaySteps {
		n.log.Error("Delay steps mismatch for node %d: expected %d, got %d", reqVote.From, delaySteps, reqVote.DelaySteps)
		return false
	}

	modulus := n.encryptionKeyStore.GetVDFModulus()
	valid, err := vr.ValidateVDF(reqVote.Seed, reqVote.Y, reqVote.VDFProof, modulus, reqVote.DelaySteps)
	if err != nil {
		n.log.Error("Error validating VDF proof from node %d: %v", reqVote.From, err)
		return false
	}
	if !valid {
		n.log.Error("Invalid VDF proof from node %d", reqVote.From)
		return false
	}

	return true
}

func (n *Node) asyncGrantVote(view int64, toNode int) {
	msg := core.GrantVoteMsg{
		ViewNumber: view,
		From:       n.GetNodeID(),
	}

	pbMsg := transportpb.GrantVoteToPB(msg)
	payloadBytes, err := marshalDeterministic(pbMsg)
	if err != nil {
		n.log.Error("Failed to marshal GrantVote message for view %d: %v", view, err)
		return
	}

	signature := crypto.SignMessageEd25519(
		payloadBytes,
		n.encryptionKeyStore.GetPrivateKey(),
	)

	toIP, exists := config.NodeAddr[toNode]
	if !exists || toIP == "" {
		n.log.Error("Address for GrantVote target node %d not found", toNode)
		return
	}

	n.messageHub.Send(core.MsgGrantVoteMessage, toIP, msg, signature)
}

func (n *Node) asyncBroadcastRequestVote(vdfResult electionVDFResult) {
	reqVote := core.RequestVoteMsg{
		From:       n.GetNodeID(),
		ViewNumber: vdfResult.view,
		Seed:       append([]byte(nil), vdfResult.seed...),
		DelaySteps: vdfResult.delaySteps,
		Y:          append([]byte(nil), vdfResult.y...),
		VDFProof:   append([]byte(nil), vdfResult.vdfProof...),
		VRFProof:   append([]byte(nil), vdfResult.vrfProof...),
	}

	pbMsg := transportpb.RequestVoteToPB(reqVote)
	payloadBytes, err := marshalDeterministic(pbMsg)
	if err != nil {
		n.log.Error("Failed to marshal RequestVote message for view %d: %v", vdfResult.view, err)
		return
	}

	signature := crypto.SignMessageEd25519(
		payloadBytes,
		n.encryptionKeyStore.GetPrivateKey(),
	)

	for nodeID, nodeIP := range config.NodeAddr {
		if nodeID == n.GetNodeID() {
			continue
		}
		if nodeIP == "" {
			n.log.Error("Address for RequestVote target node %d not found", nodeID)
			continue
		}

		go n.messageHub.Send(core.MsgRequestVoteMessage, nodeIP, reqVote, signature)
	}
}

func (n *Node) HandleGrantVoteMsg(grantVote core.GrantVoteMsg, signature []byte) {
	if grantVote.ViewNumber != n.forView {
		n.log.Debug("Ignoring GrantVote for view %d; current pending view is %d", grantVote.ViewNumber, n.forView)
		return
	}
	if n.electionManager.votedFor[grantVote.ViewNumber] != n.GetNodeID() {
		n.log.Debug("Ignoring GrantVote for view %d; this node did not vote for itself", grantVote.ViewNumber)
		return
	}

	if _, exists := n.electionManager.collectVotes[grantVote.ViewNumber]; !exists {
		n.electionManager.collectVotes[grantVote.ViewNumber] = make(map[int]struct{})
	}
	n.electionManager.collectVotes[grantVote.ViewNumber][grantVote.From] = struct{}{}

	if len(n.electionManager.collectVotes[grantVote.ViewNumber]) == n.QuorumSize() {
		n.assert(n.view < n.forView, "View should be less than forView when entering new view")
		n.newview()
	}
}
