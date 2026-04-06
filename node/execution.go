package node

import (
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/execution"
)

type pendingExecution struct {
	slot        *consensusSlot
	msg         core.ClientMsg
	noOp        bool
	missingData bool
}

type executionPostAction struct {
	msg    core.ClientMsg
	result execution.Result
	seq    int64
	digest [32]byte
	noOp   bool
}

func (n *Node) queueCommittedExecution(seq int64, slot *consensusSlot, msg core.ClientMsg, noOp bool, missingData bool) {
	postActions, periodicTrigger, checkpointTrigger, checkpointDigest, checkpointSeq := n.collectReadyExecutions(seq, slot, msg, noOp, missingData)
	for _, action := range postActions {
		n.log.Test("Executed request for seq %d success=%t", action.seq, action.result.Success)
		if !action.noOp {
			// go n.sendReply(action.msg, action.result, action.seq)
			n.pool.Delete(action.digest)
			n.pbftTimerManager.onRequestExecuted(action.msg, n) // resets timer and periodic vc stop timer
		}
	}
	if periodicTrigger && checkpointTrigger {
		n.log.Info("Both periodic and checkpoint trigger checkpointDigest=%x", checkpointDigest)
	}
	if periodicTrigger && n.periodic {
		go n.periodicVC()
	}
	if checkpointTrigger {
		n.log.Info("Checkpoint trigger for seq %d with digest %x", checkpointSeq, checkpointDigest)
		go n.checkpointVC(checkpointSeq, checkpointDigest)
	}

}

func (n *Node) collectReadyExecutions(seq int64, slot *consensusSlot, msg core.ClientMsg, noOp bool, missingData bool) ([]executionPostAction, bool, bool, [32]byte, int64) {
	n.executionMu.Lock()
	defer n.executionMu.Unlock()

	if seq <= n.lastExecuted {
		return nil, false, false, [32]byte{}, 0
	}
	if _, exists := n.pendingExecutions[seq]; !exists {
		n.pendingExecutions[seq] = pendingExecution{
			slot:        slot,
			msg:         msg,
			noOp:        noOp,
			missingData: missingData,
		}
	}

	postActions := make([]executionPostAction, 0)
	periodicTrigger := false
	checkpointTrigger := false
	var checkpointDigest [32]byte
	var checkpointSeq int64
	for {
		nextSeq := n.lastExecuted + 1
		pending, exists := n.pendingExecutions[nextSeq]
		if !exists || pending.missingData {
			break
		}
		delete(n.pendingExecutions, nextSeq)
		result := execution.Result{}
		if !pending.noOp {
			result = n.executionMachine.Apply(pending.msg)
		} else {
			n.log.Error("noop in execution for seq %d messes up periodic trigger", nextSeq)
		}

		pending.slot.mu.Lock()
		pending.slot.executionPending = true
		pending.slot.executed = true
		digestClientMsg := pending.slot.prePrepare.DigestClientMsg
		pending.slot.mu.Unlock()

		n.lastExecuted = nextSeq
		// period := int64(9*CHECKPOINT_INTERVAL) / 2
		if n.lastExecuted%n.cfg.Period == 0 {
			periodicTrigger = true
		}

		if n.lastExecuted%CHECKPOINT_INTERVAL == 0 {
			var err error
			checkpointDigest, err = n.executionMachine.CheckpointDigest()
			if err != nil {
				n.log.Error("Failed to get checkpoint material for seq %d: %v", nextSeq, err)
			} else {
				n.checkpointUpdateCondition(core.CheckpointMsg{
					SeqNum: nextSeq,
					Digest: checkpointDigest,
					From:   n.GetNodeID(),
				}, true)
				checkpointTrigger = true
				checkpointSeq = nextSeq
			}

		}
		postActions = append(postActions, executionPostAction{
			msg:    pending.msg,
			result: result,
			seq:    nextSeq,
			digest: digestClientMsg,
			noOp:   pending.noOp,
		})
	}

	return postActions, periodicTrigger, checkpointTrigger, checkpointDigest, checkpointSeq
}
