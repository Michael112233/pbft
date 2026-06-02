package node

func (n *Node) gcConsensusState(stableSeq int64) {
	if n.gc == false {
		return
	}
	n.consensusLog.slotsMu.Lock()
	removedSlots := 0
	lenOfConsensusLog := len(n.consensusLog.slots)
	for slot := range n.consensusLog.slots {
		if slot.SeqNum <= stableSeq-1000 {
			delete(n.consensusLog.slots, slot)
			removedSlots++
		}
	}
	n.consensusLog.slotsMu.Unlock()
	n.log.Info("Garbage collected %d consensus slots up to  stable seq %d and len of log was %d", removedSlots, stableSeq-100, lenOfConsensusLog)
	n.pool.GCDelMap(stableSeq - 1500)

}

func (n *Node) gcCheckpoints(key checkpoint) {
	if n.gc == false {
		return
	}
	keepFromSeq := key.seq - 30*CHECKPOINT_INTERVAL
	n.checkpointMu.Lock()
	removedCheckpoints := 0
	for cpKey := range n.checkpoints {
		if cpKey.seq < keepFromSeq {
			delete(n.checkpoints, cpKey)
			removedCheckpoints++
		}
	}
	n.checkpointMu.Unlock()
	n.log.Info("Garbage collected %d checkpoints below seq %d for stable checkpoint seq %d and digest %x", removedCheckpoints, keepFromSeq, key.seq, key.digest)
}

func (n *Node) gcViewChangeMsgs(view int64) {
	if n.gc == false {
		return
	}
	n.viewMu.Lock()
	removedVCMsgs := 0
	for v := range n.viewChangeMsgsLog {
		if v < view-3 {
			delete(n.viewChangeMsgsLog, v)
			removedVCMsgs++
		}
	}
	n.viewMu.Unlock()
	n.log.Info("Garbage collected %d view change messages up to view %d", removedVCMsgs, view-3)
}
