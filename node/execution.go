package node

import (
	"fmt"
	"math/big"
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
	postActions, periodicTrigger, periodInterval, checkpointTrigger, performanceTrigger, checkpointDigest, checkpointSeq := n.collectReadyExecutions(seq, slot, msg, noOp, missingData)
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
		go n.periodicVC(periodInterval)
	}
	if checkpointTrigger {
		n.log.Info("Checkpoint trigger for seq %d with digest %x", checkpointSeq, checkpointDigest)
		// go n.checkpointVC(checkpointSeq, checkpointDigest)
	}
	if performanceTrigger {
		// n.log.Info("Performance trigger for seq %d", seq)
		go n.perfVC()
	}

}

func (n *Node) collectReadyExecutions(seq int64, slot *consensusSlot, msg core.ClientMsg, noOp bool, missingData bool) ([]executionPostAction, bool, int64, bool, bool, [32]byte, int64) {
	n.viewMu.RLock()
	periodInterval := n.periodInterval
	view := n.view
	leaderId := n.leaderId
	n.viewMu.RUnlock()
	n.executionMu.Lock()
	defer n.executionMu.Unlock()

	if seq <= n.lastExecuted {
		return nil, false, periodInterval, false, false, [32]byte{}, 0
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
	checkpointTrigger := 0
	performanceTrigger := 0
	var checkpointDigest [32]byte
	var copyOfBalances map[string]*big.Int
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
			if !result.Success {
				n.log.Error("Execution failed for seq %d with digest %x: %s", nextSeq, pending.slot.prePrepare.DigestClientMsg, result.Error)
			}
		} else {
			n.log.Info("noop in execution for seq %d messes up periodic trigger", nextSeq)
			n.noOpsExecuted.Add(1)
		}

		pending.slot.executionPending = true
		pending.slot.executed = true
		digestClientMsg := pending.slot.prePrepare.DigestClientMsg
		pending.slot.mu.Unlock()

		n.lastExecuted = nextSeq
		if n.cfg.Performance {
			performanceTriggert := n.observeExecutedSlotForThroughput(n.lastExecuted, time.Now(), view, leaderId)
			if performanceTriggert {
				performanceTrigger += 1
			}
		}
		// period := int64(9*CHECKPOINT_INTERVAL) / 2
		if n.lastExecuted == periodInterval {
			periodicTrigger = true
		}

		if n.lastExecuted%CHECKPOINT_INTERVAL == 0 {
			var err error
			checkpointDigest, copyOfBalances, err = n.executionMachine.CheckpointDigest()
			if err != nil {
				n.log.Error("Failed to get checkpoint material for seq %d: %v", nextSeq, err)
			} else {
				n.checkpointUpdateConditionLocal(core.CheckpointMsg{
					SeqNum: nextSeq,
					Digest: checkpointDigest,
					From:   n.GetNodeID(),
				}, copyOfBalances, true)
				checkpointTrigger += 1
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
	checkpointTriggered := checkpointTrigger > 0
	performanceTriggered := performanceTrigger > 0
	if checkpointTrigger > 1 {
		n.log.Info("Multiple checkpoint triggers for seq %d, checkpointTrigger count %d", checkpointSeq, checkpointTrigger)
	}
	if performanceTrigger > 1 {
		n.log.Info("Multiple performance triggers for seq %d, performanceTrigger count %d", checkpointSeq, performanceTrigger)
	}
	return postActions, periodicTrigger, periodInterval, checkpointTriggered, performanceTriggered, checkpointDigest, checkpointSeq
}

func (n *Node) observeExecutedSlotForThroughput(seq int64, now time.Time, view int64, leaderId int) bool {
	if seq <= 0 {
		return false
	}

	// isCheckpointBoundary := seq%CHECKPOINT_INTERVAL == 0

	n.throughputMu.Lock()
	defer n.throughputMu.Unlock()

	// if n.throughputIntervalStart.IsZero() { // too big of a luck for just seq number one
	// 	n.throughputIntervalStart = now
	// 	n.throughputIntervalStartSeq = seq - 1
	// 	n.targetThroughput = defaultTargetThroughput
	// }
	// if !isCheckpointBoundary { // seq 1 case
	// 	return false
	// }
	if seq == n.throughputIntervalStartSeq {
		n.log.Info("Throughput interval start seq %d is equal to current seq %d, starting timing", n.throughputIntervalStartSeq, seq)
		n.throughputIntervalStart = now
		n.throughputObservationStarted = true
		return false
	}

	if seq%CHECKPOINT_INTERVAL != 0 || !n.throughputObservationStarted {
		if seq%CHECKPOINT_INTERVAL == 0 && !n.throughputObservationStarted {
			n.log.Info("Throughput observation not started yet, but seq %d is a checkpoint boundary, starting timing and n.throughputstartinterval is %d", seq, n.throughputIntervalStartSeq)
		}
		return false
	}

	executedSlots := seq - n.throughputIntervalStartSeq
	elapsedSeconds := now.Sub(n.throughputIntervalStart).Seconds()
	throughput := 0.0
	if elapsedSeconds > 0 {
		throughput = float64(executedSlots) / elapsedSeconds
		if elapsedSeconds > 0 {
			n.emitThroughputMeasurement(throughputMeasurement{
				MeasurementTime: now,
				View:            view,
				LeaderID:        leaderId,
				Seq:             seq,
				ExecutedSlots:   executedSlots,
				ElapsedSeconds:  elapsedSeconds,
				Throughput:      throughput,
			})
		}
		if throughput < 100 {
			n.log.Warn(" Grace Period as throughput less than 100 for view %d and seq %d is %.2f with elapsed time %.2f seconds, executed slots %d", view, seq, throughput, elapsedSeconds, executedSlots)
			// return false
		}
	} else { // grace period
		n.log.Warn("In grace period as elapsed time is zero for view %d and seq %d, executed slots %d", view, seq, executedSlots)
		return false

	}

	belowTarget := false
	if elapsedSeconds > 3 {
		belowTarget = throughput <= n.targetThroughput-5
		if belowTarget {
			n.log.Info("Elapsed secs greater than 4 and Throughput %.2f is below target %.2f for view %d and seq %d, elapsed time %.2f seconds, executed slots %d", throughput, n.targetThroughput, view, seq, elapsedSeconds, executedSlots)
		} else {
			n.log.Info("Elapsed secs greater than 4 and Throughput %.2f is above target %.2f for view %d and seq %d, elapsed time %.2f seconds, executed slots %d", throughput, n.targetThroughput, view, seq, elapsedSeconds, executedSlots)
			oldtput := n.targetThroughput
			n.targetThroughput *= 1.01
			n.log.Info("Increasing target throughput from %.2f to %.2f for view %d as observed throughput %.2f is above target", oldtput, n.targetThroughput, view, throughput)
		}

	} else {
		n.log.Info("Elapsed secs less than 4 doing nothing, the measured throughput is %.2f for view %d and seq %d, elapsed time %.2f seconds, executed slots %d", throughput, view, seq, elapsedSeconds, executedSlots)
	}

	n.checkpointThroughputs[view] = append(n.checkpointThroughputs[view], throughput)

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

func (n *Node) maxRecentViewFinalThroughputLocked(currentView int64) float64 {
	window := int64(3*n.fNodes + 1)
	startView := currentView - window
	if startView < 1 {
		startView = 1
	}

	maxThroughput := 0.0
	found := false
	concatStr := ""
	for view := startView; view < currentView; view++ {
		throughputs := n.checkpointThroughputs[view]
		if len(throughputs) == 0 {
			concatStr += fmt.Sprintf("view %d: no throughputs recorded; ", view)
			continue
		}
		throughput := throughputs[len(throughputs)-1]
		concatStr += fmt.Sprintf("view %d: final checkpoint throughput=%.2f; ", view, throughput)
		if !found || throughput > maxThroughput {
			maxThroughput = throughput
			found = true
		}
	}
	if !found {
		return defaultTargetThroughput
	}
	n.log.Info("Recent checkpoint throughputs for views [%d, %d): %s", startView, currentView, concatStr)
	return maxThroughput
}

func (n *Node) CurrentViewThroughput(currentView int64) float64 {
	n.throughputMu.RLock()
	defer n.throughputMu.RUnlock()

	throughputs, exists := n.checkpointThroughputs[currentView]
	if !exists || len(throughputs) == 0 {
		n.log.Error("No throughputs recorded for current view %d, returning 0", currentView)
		return 0.0
	}
	return throughputs[len(throughputs)-1]
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

func (n *Node) ThroughputListFromVC(vcMsgs []*core.ViewChangeMsgSig) []float64 {
	throughputs := make([]float64, 0)
	for _, vcMsgSig := range vcMsgs {
		if vcMsgSig == nil || vcMsgSig.ViewChangeMsg.WRRData == nil {
			continue
		}
		throughputs = append(throughputs, vcMsgSig.ViewChangeMsg.WRRData.Throughput)
	}
	n.log.Info("Extracted throughputs from view change messages: %v", throughputs)
	return throughputs
}
