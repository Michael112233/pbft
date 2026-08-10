package node

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"math/big"
	"sort"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/crypto"
	"github.com/michael112233/pbft/transportpb"
)

func (n *Node) HandleCheckpoint(checkpointMsg core.CheckpointMsg, signature []byte) {
	if checkpointMsg.SeqNum == 0 || checkpointMsg.SeqNum%CHECKPOINT_INTERVAL != 0 {
		return
	}
	n.log.Test("Received checkpoint message from node %d for seq %d with digest %x", checkpointMsg.From, checkpointMsg.SeqNum, checkpointMsg.Digest)

	needStateTransfer := false
	n.checkpointMu.Lock()
	key := checkpoint{
		seq:    checkpointMsg.SeqNum,
		digest: checkpointMsg.Digest,
	}

	checkpointData, exists := n.checkpoints[key]
	if !exists {
		checkpointData = CheckpointData{
			votes:    make(map[int]core.CheckpointMsgSig),
			balances: nil,
		}
		n.checkpoints[key] = checkpointData
	}
	checkpointData.votes[checkpointMsg.From] = core.CheckpointMsgSig{
		CheckpointMsg: checkpointMsg,
		Signature:     signature,
	}
	n.checkpoints[key] = checkpointData

	if len(checkpointData.votes) == 2*n.fNodes+1 {
		if key.seq > n.lastStableCheckpoint.seq && checkpointData.balances != nil {
			n.lastStableCheckpoint = key
			go n.gcConsensusState(key.seq)
			go n.gcCheckpoints(key)
			// garbage collect old checkpoints
			if n.lastStableCheckpoint.seq%1000 == 0 {
				n.log.Info("Stable checkpoint advanced on receiving remote checkpoint seq=%d digest=%x votes=%d", key.seq, key.digest, len(checkpointData.votes))
			}
		} else if key.seq > n.lastStableCheckpoint.seq && checkpointData.balances == nil {
			needStateTransfer = true
		}
	}
	n.checkpointMu.Unlock()

	if needStateTransfer {
		// n.log.Info("Requesting state transfer for seq %d digest %x due to checkpoint quorum", key.seq, key.digest)
		n.RequestStateTransfer(key.seq, key.digest, false)
	}
}

func (n *Node) RequestStateTransfer(seq int64, digest [32]byte, fromVc bool) { // only from vc dont need it with every checkpoint
	// time.Sleep(200 * time.Millisecond)
	if !fromVc {
		// time.Sleep(600 * time.Millisecond)
		return
	} else {
		// time.Sleep(100 * time.Millisecond)
	}
	clientMissingStateTransferring := n.missingClientStateManager.isMissingClientStateTransferring()
	if clientMissingStateTransferring && !fromVc { // can block forever if client one doesnt complete, fromvc one always go through
		n.log.Warn("Client missing state is transferring, skipping checkpointstate transfer request")
		return
	}

	n.checkpointMu.Lock()
	if seq <= n.lastStableCheckpoint.seq {
		n.log.Info("State transfer request for seq %d digest %x aborted, already stable checkpoint", seq, digest)
		n.checkpointMu.Unlock()
		return
	}
	if seq <= n.stateRequestTransferInProgress {
		n.log.Info("State transfer request for > seq %d digest %x already in progress", seq, digest)
		n.checkpointMu.Unlock()
		return
	}
	n.stateRequestTransferInProgress = seq
	n.checkpointMu.Unlock()

	msg := core.RequestStateTransferMsg{
		SeqNum: seq,
		Digest: digest,
		From:   n.GetNodeID(),
	}
	pbMsg := transportpb.RequestStateTransferToPB(msg)
	payloadBytes, err := marshalDeterministic(pbMsg)
	if err != nil {
		n.log.Error("Failed to marshal RequestStateTransfer message for signing: %v", err)
		return
	}
	signature := crypto.SignMessageEd25519(payloadBytes, n.encryptionKeyStore.GetPrivateKey())
	n.log.Warn("xRequesting state transfer for seq %d digest %x", seq, digest)

	for _, otherIP := range config.NodeAddr {
		if otherIP == n.GetAddr() {
			continue
		}
		go n.messageHub.Send(core.MsgRequestStateTransfer, otherIP, msg, signature)
	}
}

func (n *Node) HandleRequestStateTransfer(request core.RequestStateTransferMsg, signature []byte) {
	n.checkpointMu.Lock()

	key := checkpoint{
		seq:    request.SeqNum,
		digest: request.Digest,
	}
	if key.seq > n.lastStableCheckpoint.seq {
		n.checkpointMu.Unlock()
		n.log.Warn("Ignoring state transfer request for unstable checkpoint seq=%d digest=%x from=%d", key.seq, key.digest, request.From)
		return
	}
	checkpointData, exists := n.checkpoints[key]
	if !exists || checkpointData.balances == nil {

		if exists && checkpointData.balances == nil {
			n.log.Error("Received state transfer request for seq %d digest %x, but no balances available", key.seq, key.digest)
		}
		n.checkpointMu.Unlock()
		return
	}
	// balances := cloneBalances(checkpointData.balances)
	n.checkpointMu.Unlock()

	target, exists := config.NodeAddr[request.From]
	if !exists {
		n.log.Error("Received state transfer request from unknown node %d", request.From)
		return
	}
	stateTransferMsg := core.StateTransferMsg{
		SeqNum:   key.seq,
		Digest:   key.digest,
		From:     n.GetNodeID(),
		Balances: checkpointData.balances,
	}
	pbMsg := transportpb.StateTransferToPB(stateTransferMsg)
	payloadBytes, err := marshalDeterministic(pbMsg)
	if err != nil {
		n.log.Error("Failed to marshal StateTransfer message for signing: %v", err)
		return
	}
	responseSignature := crypto.SignMessageEd25519(payloadBytes, n.encryptionKeyStore.GetPrivateKey())
	n.log.Warn("Sending state transfer for seq %d digest %x to node %d", key.seq, key.digest, request.From)
	go n.messageHub.Send(core.MsgStateTransfer, target, stateTransferMsg, responseSignature)
}

type CheckpointCatchupState struct {
	seq      int64
	balances map[string]*big.Int
}

func (n *Node) HandleStateTransfer(stateTransferMsg core.StateTransferMsg, signature []byte) {
	n.checkpointMu.Lock()
	stateUpdated := false
	// can add small verfication against existing data
	// n.log.Warn("Received transfered state")
	key := checkpoint{
		seq:    stateTransferMsg.SeqNum,
		digest: stateTransferMsg.Digest,
	}

	checkpointData, exists := n.checkpoints[key]
	if !exists {
		n.checkpointMu.Unlock()
		n.log.Error("Received state transfer for unknown checkpoint seq=%d digest=%x", key.seq, key.digest)
		return
	}
	if len(checkpointData.votes) < 2*n.fNodes+1 {
		n.checkpointMu.Unlock()
		n.log.Error("Received state transfer for checkpoint seq=%d digest=%x without quorum votes", key.seq, key.digest)
		return
	}

	// restoredBalances := cloneBalances(stateTransferMsg.Balances)
	restoredBalances := stateTransferMsg.Balances
	if digestBalances(restoredBalances) != key.digest {
		n.checkpointMu.Unlock()
		n.log.Error("Received state transfer with invalid balances for checkpoint seq=%d digest=%x", key.seq, key.digest)
		return
	}
	if stateTransferMsg.SeqNum > n.lastStableCheckpoint.seq && checkpointData.balances == nil {
		checkpointData.balances = restoredBalances
		n.checkpoints[key] = checkpointData
		n.log.Warn("State transfer applied for checkpoint seq=%d digest=%x", key.seq, key.digest)
		n.lastStableCheckpoint = key
		go n.gcConsensusState(key.seq)
		go n.gcCheckpoints(key)
		stateUpdated = true
	} else if stateTransferMsg.SeqNum <= n.lastStableCheckpoint.seq && checkpointData.balances != nil {
		// n.log.Warn("Received state transfer for already stable checkpoint seq=%d digest=%x, ignoring or updated locally", key.seq, key.digest)
	}
	n.checkpointMu.Unlock()
	if stateUpdated {
		n.cpStateTransfer <- CheckpointCatchupState{
			seq:      stateTransferMsg.SeqNum,
			balances: restoredBalances,
		}

	}

}

func cloneBalances(balances map[string]*big.Int) map[string]*big.Int {
	out := make(map[string]*big.Int, len(balances))
	for account, balance := range balances {
		if balance == nil {
			out[account] = big.NewInt(0)
			continue
		}
		out[account] = new(big.Int).Set(balance)
	}
	return out
}

func digestBalances(balances map[string]*big.Int) [32]byte {
	accounts := make([]string, 0, len(balances))
	for account := range balances {
		accounts = append(accounts, account)
	}
	sort.Strings(accounts)

	var buf bytes.Buffer
	for _, account := range accounts {
		balance := balances[account]
		if balance == nil {
			balance = big.NewInt(0)
		}
		_, _ = fmt.Fprintf(&buf, "%s=%s\n", account, balance.String())
	}
	return sha256.Sum256(buf.Bytes())
}

func (n *Node) checkpointUpdateConditionLocal(msg core.CheckpointMsg, copyOfBalances map[string]*big.Int, allowStabilize bool) {
	key := checkpoint{
		seq:    msg.SeqNum,
		digest: msg.Digest,
	}

	n.checkpointMu.Lock()
	defer n.checkpointMu.Unlock()

	checkpointData, exists := n.checkpoints[key]
	if !exists {
		checkpointData = CheckpointData{
			votes:    make(map[int]core.CheckpointMsgSig),
			balances: nil,
		}
		n.checkpoints[key] = checkpointData
	}
	msg = core.CheckpointMsg{
		SeqNum: msg.SeqNum,
		Digest: msg.Digest,
		From:   msg.From,
	}
	pbMsg := transportpb.CheckpointToPB(msg)
	payloadBytes, err := marshalDeterministic(pbMsg)
	if err != nil {
		n.log.Error("Failed to marshal Checkpoint message for signing: %v", err)
		return
	}
	signature := crypto.SignMessageEd25519(payloadBytes, n.encryptionKeyStore.GetPrivateKey())
	// n.log.Info("Broadcasting checkpoint message for seq %d with digest %x", msg.SeqNum, msg.Digest)
	for _, otherIP := range config.NodeAddr {
		if otherIP == n.GetAddr() {
			continue
		}
		go n.messageHub.Send(core.MsgCheckpointMessage, otherIP, msg, signature)
	}
	checkpointData.votes[msg.From] = core.CheckpointMsgSig{
		CheckpointMsg: msg,
		Signature:     signature,
	}
	checkpointData.balances = copyOfBalances
	n.checkpoints[key] = checkpointData

	if len(checkpointData.votes) >= 2*n.fNodes+1 {
		if key.seq > n.lastStableCheckpoint.seq {
			n.lastStableCheckpoint = key
			go n.gcConsensusState(key.seq)
			go n.gcCheckpoints(key)
			if n.lastStableCheckpoint.seq%1000 == 0 {
				n.log.Info("Stable checkpoint advanced locally seq=%d digest=%x votes=%d", key.seq, key.digest, len(checkpointData.votes))
			}
		}
	}

}

func (n *Node) fastPathStablizeCheckpointviaVC(latestStableCheckpoint checkpoint, checkpointProof []core.CheckpointMsgSig, path string) {
	n.checkpointMu.Lock()
	myStableCheckpoint := n.lastStableCheckpoint
	if latestStableCheckpoint.seq > myStableCheckpoint.seq {
		n.log.Debug("missing the latest stable checkpoint at o %s", path)
		checkpointData, exists := n.checkpoints[latestStableCheckpoint]
		if !exists {
			checkpointData = CheckpointData{
				votes:    make(map[int]core.CheckpointMsgSig),
				balances: nil,
			}
			n.checkpoints[latestStableCheckpoint] = checkpointData
		}
		for _, checkpointMsgSig := range checkpointProof { // some time some vote already exist so rewrite them
			checkpointData.votes[checkpointMsgSig.CheckpointMsg.From] = checkpointMsgSig
		}
		if len(checkpointData.votes) < 2*n.fNodes+1 {
			n.log.Error("checkpoint from vc does not have enough votes, should not happen at o %s", path)
		}
		n.checkpoints[latestStableCheckpoint] = checkpointData // len votes should always be 2f+1 since checkpoint from vc verified
		if checkpointData.balances == nil {
			n.log.Info("requesting state transfer from creat 0 at %s", path)
			go n.RequestStateTransfer(latestStableCheckpoint.seq, latestStableCheckpoint.digest, true)
		} else { // had balances but not enough votes
			n.lastStableCheckpoint = latestStableCheckpoint // unsafe checkpoint forwarding
			go n.gcConsensusState(latestStableCheckpoint.seq)
			go n.gcCheckpoints(latestStableCheckpoint)
		}

		// } else if exists && len(checkpointData.votes) < 2*n.fNodes+1 { // this path is mainly when i have executed and have balances but need more votes to stabalize
		// 	for _, checkpointMsgSig := range checkpointProof {
		// 		n.checkpoints[latestStableCheckpoint].votes[checkpointMsgSig.CheckpointMsg.From] = checkpointMsgSig
		// 	} // if not exists then copy proof, if exists may replace some with incoming we will verify checkpoint before when receive vc
		// 	if checkpointData.balances == nil { // most likely this case not run
		// 		n.log.Info("requesting state transfer from creat 0 primary")
		// 		go n.RequestStateTransfer(latestStableCheckpoint.seq, latestStableCheckpoint.digest, true)
		// 	} else {
		// 		n.lastStableCheckpoint = latestStableCheckpoint // unsafe checkpoint forwarding
		// 		go n.gcConsensusState(latestStableCheckpoint.seq)
		// 		go n.gcCheckpoints(latestStableCheckpoint)
		// 	}
		// } else {
		// 	// if votess >= then from cehckpoint receive path should have called state transfer
		// 	n.log.Error("should not come here for checkpoint in create O at primary")
		// }

	}
	n.checkpointMu.Unlock()
}
