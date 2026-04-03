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
	postActions := n.collectReadyExecutions(seq, slot, msg, noOp, missingData)
	for _, action := range postActions {
		n.log.Info("Executed request for seq %d success=%t", action.seq, action.result.Success)
		if !action.noOp {
			go n.sendReply(action.msg, action.result, action.seq)
			n.pool.Delete(action.digest)
			n.pbftTimerManager.onRequestExecuted(action.msg, n)
		}
	}
}

func (n *Node) collectReadyExecutions(seq int64, slot *consensusSlot, msg core.ClientMsg, noOp bool, missingData bool) []executionPostAction {
	n.executionMu.Lock()
	defer n.executionMu.Unlock()

	if seq <= n.lastExecuted {
		return nil
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
		}

		pending.slot.mu.Lock()
		pending.slot.executionPending = true
		pending.slot.executed = true
		digestClientMsg := pending.slot.prePrepare.DigestClientMsg
		pending.slot.mu.Unlock()

		n.lastExecuted = nextSeq
		postActions = append(postActions, executionPostAction{
			msg:    pending.msg,
			result: result,
			seq:    nextSeq,
			digest: digestClientMsg,
			noOp:   pending.noOp,
		})
	}

	return postActions
}
