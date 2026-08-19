package node

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"math/big"
	"sort"

	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/crypto"
	"github.com/michael112233/pbft/logger"
	"github.com/michael112233/pbft/transportpb"
)

type checkpoint struct {
	seq    int64
	digest [32]byte
}

type CheckpointData struct {
	votes    map[int]core.CheckpointMsgSig
	balances map[string]*big.Int
}

type CheckpointManager struct {
	laststableCheckpoint checkpoint
	checkpoints          map[checkpoint]CheckpointData
	log                  *logger.Logger
	node                 CheckpointManagerNode
}

type CheckpointManagerNode interface {
	QuorumSize() int
	asyncBroadCast(msgType string, msg interface{}, signature []byte)
	GCLog(seq int64)
	PushExecutionMachine(balances map[string]*big.Int, seq int64)
}

func NewCheckpointManager(log *logger.Logger, node CheckpointManagerNode) *CheckpointManager {
	return &CheckpointManager{
		laststableCheckpoint: checkpoint{seq: 0, digest: [32]byte{}},
		checkpoints:          make(map[checkpoint]CheckpointData),
		log:                  log,
		node:                 node,
	}
}

func (cm *CheckpointManager) HandleCheckpoint(checkpointMsg core.CheckpointMsg, signature []byte) {
	if checkpointMsg.SeqNum == 0 || checkpointMsg.SeqNum%CHECKPOINT_INTERVAL != 0 {
		return
	}
	cm.log.Test("Received checkpoint message from node %d for seq %d with digest %x", checkpointMsg.From, checkpointMsg.SeqNum, checkpointMsg.Digest)
	key := checkpoint{
		seq:    checkpointMsg.SeqNum,
		digest: checkpointMsg.Digest,
	}

	checkpointData, exists := cm.checkpoints[key]
	if !exists {
		checkpointData = CheckpointData{
			votes:    make(map[int]core.CheckpointMsgSig),
			balances: nil,
		}
		cm.checkpoints[key] = checkpointData
	}
	checkpointData.votes[checkpointMsg.From] = core.CheckpointMsgSig{
		CheckpointMsg: checkpointMsg,
		Signature:     signature,
	}
	cm.checkpoints[key] = checkpointData

	if len(checkpointData.votes) == cm.node.QuorumSize() {
		if key.seq > cm.laststableCheckpoint.seq && checkpointData.balances != nil {
			cm.laststableCheckpoint = key
			cm.node.GCLog(key.seq)
			cm.gcCheckpoints(key)
			cm.log.Info("Stable checkpoint advanced on receiving remote checkpoint seq=%d digest=%x votes=%d", key.seq, key.digest, len(checkpointData.votes))
		}
	}

}

func (cm *CheckpointManager) HandleLocalCheckpoint(checkpointMsg core.CheckpointMsg, signature []byte, balances map[string]*big.Int) {
	key := checkpoint{
		seq:    checkpointMsg.SeqNum,
		digest: checkpointMsg.Digest,
	}

	checkpointData, exists := cm.checkpoints[key]
	if !exists {
		checkpointData = CheckpointData{
			votes:    make(map[int]core.CheckpointMsgSig),
			balances: nil,
		}
		cm.checkpoints[key] = checkpointData
	}
	cm.node.asyncBroadCast(core.MsgCheckpointMessage, checkpointMsg, signature)

	checkpointData.votes[checkpointMsg.From] = core.CheckpointMsgSig{
		CheckpointMsg: checkpointMsg,
		Signature:     signature,
	}
	checkpointData.balances = balances
	cm.checkpoints[key] = checkpointData

	if len(checkpointData.votes) >= cm.node.QuorumSize() {
		if key.seq > cm.laststableCheckpoint.seq {
			cm.laststableCheckpoint = key
			cm.node.GCLog(key.seq)
			cm.gcCheckpoints(key)
			// go cm.node.gcConsensusState(key.seq)
			// go cm.node.gcCheckpoints(key)

			cm.log.Info("Stable checkpoint advanced locally seq=%d digest=%x votes=%d", key.seq, key.digest, len(checkpointData.votes))

		}
	}

}

func (cm *CheckpointManager) gcCheckpoints(key checkpoint) {
	// if n.gc == false {
	// 	return
	// }
	keepFromSeq := key.seq - 10*CHECKPOINT_INTERVAL

	removedCheckpoints := 0
	for cpKey := range cm.checkpoints {
		if cpKey.seq < keepFromSeq {
			delete(cm.checkpoints, cpKey)
			removedCheckpoints++
		}
	}

	// n.log.Info("Garbage collected %d checkpoints below seq %d for stable checkpoint seq %d and digest %x", removedCheckpoints, keepFromSeq, key.seq, key.digest)
}

func (cm *CheckpointManager) GetLastStableCheckpointwithProofandBalances() (checkpoint, []core.CheckpointMsgSig, map[string]*big.Int) {
	checkpointSeq := cm.laststableCheckpoint.seq
	checkpointDigest := cm.laststableCheckpoint.digest

	key := checkpoint{
		seq:    checkpointSeq,
		digest: checkpointDigest,
	}
	if checkpointSeq == 0 {
		return key, nil, nil
	}
	checkpointData, exists := cm.checkpoints[key]
	if !exists {
		cm.log.Error("No checkpoint data found for stable checkpoint seq %d and digest %x", checkpointSeq, checkpointDigest)
	}
	checkpointProof := make([]core.CheckpointMsgSig, 0, len(checkpointData.votes))
	for _, msg := range checkpointData.votes {
		checkpointProof = append(checkpointProof, msg)
	}
	return key, checkpointProof, checkpointData.balances
}

func (cm *CheckpointManager) GetLastStableCheckpointSeq() int64 {
	return cm.laststableCheckpoint.seq
}

func (cm *CheckpointManager) fastPathStablizeCheckpointviaVC(latestStableCheckpoint checkpoint, checkpointProof []core.CheckpointMsgSig, balances map[string]*big.Int, path string) {

	myStableCheckpoint := cm.laststableCheckpoint
	if latestStableCheckpoint.seq > myStableCheckpoint.seq {
		cm.log.Debug("missing the latest stable checkpoint at o %s", path)
		checkpointData, exists := cm.checkpoints[latestStableCheckpoint]
		if !exists {
			checkpointData = CheckpointData{
				votes:    make(map[int]core.CheckpointMsgSig),
				balances: nil,
			}
			cm.checkpoints[latestStableCheckpoint] = checkpointData
		}
		for _, checkpointMsgSig := range checkpointProof { // some time some vote already exist so rewrite them
			checkpointData.votes[checkpointMsgSig.CheckpointMsg.From] = checkpointMsgSig
		}
		if len(checkpointData.votes) < cm.node.QuorumSize() {
			cm.log.Error("checkpoint from vc does not have enough votes, should not happen at o %s", path)
		}
		cm.checkpoints[latestStableCheckpoint] = checkpointData // len votes should always be 2f+1 since checkpoint from vc verified
		if checkpointData.balances == nil {
			checkpointData.balances = balances
			cm.checkpoints[latestStableCheckpoint] = checkpointData
			cm.laststableCheckpoint = latestStableCheckpoint
			cm.node.GCLog(latestStableCheckpoint.seq)
			cm.gcCheckpoints(latestStableCheckpoint)
			cm.log.Warn("Moving forward execution machine may execute req because of cp transfer")
			cm.node.PushExecutionMachine(balances, latestStableCheckpoint.seq)

		} else {
			cm.laststableCheckpoint = latestStableCheckpoint
			cm.node.GCLog(latestStableCheckpoint.seq)
			cm.gcCheckpoints(latestStableCheckpoint)
		}
		// } else { // had balances but not enough votes
		// 	n.lastStableCheckpoint = latestStableCheckpoint // unsafe checkpoint forwarding
		// 	go n.gcConsensusState(latestStableCheckpoint.seq)
		// 	go n.gcCheckpoints(latestStableCheckpoint)
		// }

	}
}
func (n *Node) fastPathStablizeCheckpointviaVC(latestStableCheckpoint checkpoint, checkpointProof []core.CheckpointMsgSig, checkpointBalances map[string]*big.Int, path string) {
	n.checkpointManager.fastPathStablizeCheckpointviaVC(latestStableCheckpoint, checkpointProof, checkpointBalances, path)
}

func (n *Node) GetLastStableCheckpointSeq() int64 {
	return n.checkpointManager.GetLastStableCheckpointSeq()
}

func (n *Node) GetLastStableCheckpointwithProofandBalances() (checkpoint, []core.CheckpointMsgSig, map[string]*big.Int) {
	return n.checkpointManager.GetLastStableCheckpointwithProofandBalances()
}

func (n *Node) HandleCheckpoint(checkpointMsg core.CheckpointMsg, signature []byte) {
	n.checkpointManager.HandleCheckpoint(checkpointMsg, signature)
}

// in previous impl only copy of balance was on execution path now digest and signature too

func (n *Node) HandleLocalCheckpoint(copyOfBalances map[string]*big.Int, seq int64) {
	cpDigest := digestBalances(copyOfBalances)
	cpMsg := core.CheckpointMsg{
		SeqNum: seq,
		Digest: cpDigest,
		From:   n.GetNodeID(),
	}
	pbMsg := transportpb.CheckpointToPB(cpMsg)
	payloadBytes, err := marshalDeterministic(pbMsg)
	if err != nil {
		n.log.Error("Failed to marshal Checkpoint message for signing: %v", err)
		return
	}
	signature := crypto.SignMessageEd25519(payloadBytes, n.encryptionKeyStore.GetPrivateKey())
	n.checkpointManager.HandleLocalCheckpoint(cpMsg, signature, copyOfBalances)
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
