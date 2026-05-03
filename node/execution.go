package node

import (
	"time"

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
	postActions, periodicTrigger, checkpointTrigger, performanceTrigger, checkpointDigest, checkpointSeq := n.collectReadyExecutions(seq, slot, msg, noOp, missingData)
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
	if performanceTrigger {
		n.log.Info("Performance trigger for seq %d", seq)
	}

}

func (n *Node) collectReadyExecutions(seq int64, slot *consensusSlot, msg core.ClientMsg, noOp bool, missingData bool) ([]executionPostAction, bool, bool, bool, [32]byte, int64) {
	n.executionMu.Lock()
	defer n.executionMu.Unlock()

	if seq <= n.lastExecuted {
		return nil, false, false, false, [32]byte{}, 0
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
	performanceTrigger := false
	var checkpointDigest [32]byte
	var checkpointSeq int64
	for {
		nextSeq := n.lastExecuted + 1
		pending, exists := n.pendingExecutions[nextSeq]
		if !exists {
			break
		}
		pending.slot.mu.Lock()
		if !pending.noOp && pending.missingData {
			clientPoolMsg, clientPoolMsgExists, clientPoolMsgExecuted := n.pool.Get(pending.slot.prePrepare.DigestClientMsg)
			if clientPoolMsgExists {
				if clientPoolMsgExecuted {
					n.log.Error("Client message for seq %d with digest %x already executed 1, skipping execution", nextSeq, pending.slot.prePrepare.DigestClientMsg)

				}
				pending.msg = clientPoolMsg.Data

			} else {
				if clientPoolMsgExecuted {
					n.log.Error("Client message for seq %d with digest %x already executed 2, skipping execution", nextSeq, pending.slot.prePrepare.DigestClientMsg)

				}
				pending.slot.mu.Unlock()
				break
			}
		}
		delete(n.pendingExecutions, nextSeq)
		result := execution.Result{}
		if !pending.noOp {
			result = n.executionMachine.Apply(pending.msg)
		} else {
			n.log.Error("noop in execution for seq %d messes up periodic trigger", nextSeq)
		}

		pending.slot.executionPending = true
		pending.slot.executed = true
		digestClientMsg := pending.slot.prePrepare.DigestClientMsg
		pending.slot.mu.Unlock()

		n.lastExecuted = nextSeq
		if n.lastExecuted == 1 || n.lastExecuted%CHECKPOINT_INTERVAL == 0 {
			performanceTrigger = n.observeExecutedSlotForThroughput(n.lastExecuted, time.Now())
		}
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

	return postActions, periodicTrigger, checkpointTrigger, performanceTrigger, checkpointDigest, checkpointSeq
}

func (n *Node) observeExecutedSlotForThroughput(seq int64, now time.Time) bool {
	if seq <= 0 {
		return false
	}

	var view int64
	isCheckpointBoundary := seq%CHECKPOINT_INTERVAL == 0
	if isCheckpointBoundary {
		n.viewMu.RLock()
		view = n.view
		n.viewMu.RUnlock()
	}

	n.throughputMu.Lock()
	defer n.throughputMu.Unlock()

	if n.throughputIntervalStart.IsZero() { // too big of a luck for just seq number one
		n.throughputIntervalStart = now
		n.throughputIntervalStartSeq = seq - 1
		n.targetThroughput = defaultTargetThroughput
	}
	if !isCheckpointBoundary { // seq 1 case
		return false
	}

	executedSlots := seq - n.throughputIntervalStartSeq
	elapsedSeconds := now.Sub(n.throughputIntervalStart).Seconds()
	throughput := 0.0
	if elapsedSeconds > 0 {
		throughput = float64(executedSlots) / elapsedSeconds
	} else { // grace period
		n.log.Info("In grace period after VC, not measuring throughput for seq %d", seq)
		return false

	}

	belowTarget := throughput < n.targetThroughput
	if throughput > n.targetThroughput {
		n.targetThroughput *= 1.01
	}

	n.checkpointThroughputs[view] = append(n.checkpointThroughputs[view], throughput)
	n.throughputIntervalStart = now
	n.throughputIntervalStartSeq = seq
	if n.log != nil {
		n.log.Info("Checkpoint throughput view=%d seq=%d throughput=%.2f slots_per_sec", view, seq, throughput)
	}
	return belowTarget
}

func (n *Node) maxRecentViewThroughputLocked(currentView int64) float64 {
	window := int64(3*n.fNodes + 1)
	startView := currentView - window
	if startView < 1 {
		startView = 1
	}

	maxThroughput := 0.0
	found := false
	for view := startView; view < currentView; view++ {
		for _, throughput := range n.checkpointThroughputs[view] {
			if !found || throughput > maxThroughput {
				maxThroughput = throughput
				found = true
			}
		}
	}
	if !found {
		return defaultTargetThroughput
	}
	return maxThroughput
}

func (n *Node) CheckpointThroughputsSnapshot() map[int64][]float64 {
	n.throughputMu.RLock()
	defer n.throughputMu.RUnlock()

	snapshot := make(map[int64][]float64, len(n.checkpointThroughputs))
	for view, throughputs := range n.checkpointThroughputs {
		snapshot[view] = append([]float64(nil), throughputs...)
	}
	return snapshot
}
