package node

import (
	"math/big"
	"time"

	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/execution"
)

type executionPostAction struct {
	msg    core.ClientMsg
	result execution.Result
	noOp   bool
	seq    int64
}

func (n *Node) exeLoop() {
	postActions := make([]executionPostAction, 0)
	view := n.GetView()
	leaderId := n.GetLeaderId()
	previousExecuted := n.GetLastExecuted()
	for {
		slot, exists := n.consensusLog.GetLogEntry(n.lastExecuted + 1)
		if !exists || !slot.committed {
			break
		}
		if slot.executed {
			n.log.Error("slot already executed, this should not happen")
			break
		}

		batchreqs, exists := n.pool.GetBatch(slot.preprepare.DigestIndividualClientMsgs)
		if !exists {
			n.log.Error("some req for batch not found")
			break
		}
		results := make([]execution.Result, len(batchreqs))
		for i, req := range batchreqs {
			result := n.executionMachine.Apply(req.Data)
			results[i] = result
			if !result.Success {
				n.log.Error("Execution failed for seq %d with error: %s", n.lastExecuted+1, result.Error)
			}
			postActions = append(postActions, executionPostAction{
				msg:    req.Data,
				result: result,
				seq:    n.lastExecuted + 1,
				noOp:   false,
			})
		}
		// for optimise can comment this out of marking executed lest see if need to mark it
		err := n.pool.MarkExecuted(slot.preprepare.DigestIndividualClientMsgs)
		if err != nil {
			n.log.Error("Error marking batch as executed: %v", err)
			break
		}

		slot.executed = true
		n.lastExecuted++
		if n.lastExecuted == 1 {
			n.resetLeaderProgressTimer()
		}
		// n.resetLeaderProgressTimer()
		if n.cfg.Performance {
			_ = n.observeExecutedSlotForThroughput(n.lastExecuted, time.Now(), view, leaderId)
			// if performanceTriggert {
			// 	performanceTrigger += 1
			// }
		}
		if n.lastExecuted%CHECKPOINT_INTERVAL == 0 {
			copyOfBalances := n.executionMachine.CheckpointSnapshot()
			n.HandleLocalCheckpoint(copyOfBalances, n.lastExecuted)
		}

		// of full batch
		n.RecordEndTime(slot.preprepare.DigestClientMsg, time.Now())
	}
	if n.GetLastExecuted() > previousExecuted {
		n.tryPropose(true)
	}
	go n.postActions(postActions)

}

func (n *Node) postActions(actions []executionPostAction) {

	for _, action := range actions {
		n.log.Test("Executed request for seq %d success=%t", action.seq, action.result.Success)
		if !action.noOp {
			n.sendCommitTps(action.msg)

		}
	}
}

func (n *Node) PushExecutionMachine(balances map[string]*big.Int, seq int64) {
	n.lastExecuted = seq
	n.executionMachine.RestoreCheckpoint(balances)
}
