package node

import (
	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/crypto"
	"github.com/michael112233/pbft/transportpb"
)

// need to add sig
func (n *Node) checkpointVC(seq int64, digest [32]byte) {
	msg := core.CheckpointMsg{
		SeqNum: seq,
		Digest: digest,
		From:   n.GetNodeID(),
	}

	pbMsg := transportpb.CheckpointToPB(msg)
	payloadBytes, err := marshalDeterministic(pbMsg)
	if err != nil {
		n.log.Error("Failed to marshal Checkpoint message for signing: %v", err)
		return
	}
	signature := crypto.SignMessageEd25519(payloadBytes, n.encryptionKeyStore.GetPrivateKey())

	for _, otherIP := range config.NodeAddr {
		if otherIP == n.GetAddr() {
			continue
		}
		go n.messageHub.Send(core.MsgCheckpointMessage, otherIP, msg, signature)
	}
}

func (n *Node) HandleCheckpoint(checkpointMsg core.CheckpointMsg) {
	if checkpointMsg.SeqNum == 0 || checkpointMsg.SeqNum%CHECKPOINT_INTERVAL != 0 {
		return
	}

	n.executionMu.Lock()
	localExecuted := checkpointMsg.SeqNum <= n.lastExecuted // in future replace by catching up with cp
	n.executionMu.Unlock()

	n.checkpointUpdateCondition(checkpointMsg, localExecuted)
}

func (n *Node) checkpointUpdateCondition(msg core.CheckpointMsg, allowStabilize bool) bool {
	key := checkpoint{
		seq:    msg.SeqNum,
		digest: msg.Digest,
	}

	n.checkpointMu.Lock()
	defer n.checkpointMu.Unlock()

	votes, exists := n.checkpoints[key]
	if !exists {
		votes = make(checkpointVotes)
		n.checkpoints[key] = votes
	}
	votes[msg.From] = struct{}{}

	if !allowStabilize || len(votes) < 2*n.fNodes+1 {
		return false
	}
	if key.seq <= n.lastStableCheckpoint.seq {
		return false
	}

	n.lastStableCheckpoint = key
	n.log.Info("Stable checkpoint advanced seq=%d digest=%x votes=%d", key.seq, key.digest, len(votes))
	return true
}
