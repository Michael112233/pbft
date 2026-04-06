package node

import (
	"fmt"
	"slices"
	"sync"

	"github.com/michael112233/pbft/core"
)

type slotKey struct {
	View   int64
	SeqNum int64
}

type consensusSlot struct {
	mu sync.Mutex

	view int64 // view this log entry belongs to

	// PrePrepare (nil until received/created)
	prePrepare    *core.PreprepareMsg
	prePrepareSig []byte
	missingData   bool
	// digest        [32]byte

	// Vote sets — key is sender's NodeID, value is the digest they voted for.
	// This defends against equivocation: a Byzantine leader can send different
	// digests to different replicas, so we must only count votes matching our
	// accepted PrePrepare digest at quorum-check time.
	prepares map[int]*core.PrepareMsgSig
	commits  map[int][32]byte

	// One-shot flags so we broadcast exactly once per phase transition
	prepareSent      bool // did *this* node already broadcast Prepare
	commitSent       bool // did *this* node already broadcast Commit
	executionPending bool // committed-local and waiting for ordered execution
	executed         bool // already delivered to application
}

type ConsensusLog struct {
	slotsMu sync.RWMutex
	slots   map[slotKey]*consensusSlot
}

func NewConsensusLog() ConsensusLog {
	return ConsensusLog{
		slots: make(map[slotKey]*consensusSlot),
	}
}

func (log *ConsensusLog) PrintSlot(randSeqs []int64, view int64) {
	for _, seqNum := range randSeqs {
		if key, val := log.slots[slotKey{View: view, SeqNum: seqNum}]; val {
			slot := key
			fmt.Printf("SeqNum: %d, View: %d, PrepareVotes: %d, CommitVotes: %d, PrepareSent: %t, CommitSent: %t, ExecutionPending: %t, Executed: %t, MissingData: %t\n",
				seqNum, slot.view, len(slot.prepares), len(slot.commits), slot.prepareSent, slot.commitSent, slot.executionPending, slot.executed, slot.missingData)
			fmt.Printf("Message details %d\n, data %v", slot.prePrepare.ClientMsg.Data.Id, slot.prePrepare.ClientMsg.Data)
		} else {
			fmt.Printf("No slot found for SeqNum %d in view %d\n", seqNum, view)
		}
	}

}

func (log *ConsensusLog) PrintExecutedSlots(currView int64) {
	type executedSlotSnapshot struct {
		seq              int64
		view             int64
		digest           [32]byte
		dataID           int64
		hasClientData    bool
		prepareVotes     int
		commitVotes      int
		prepareSent      bool
		commitSent       bool
		executionPending bool
		executed         bool
		missingData      bool
	}

	warningSeqs := make(map[int64]struct{})
	warningCount := 0
	recordWarning := func(seq int64, format string, args ...interface{}) {
		fmt.Printf(format, args...)
		warningCount++
		warningSeqs[seq] = struct{}{}
	}

	log.slotsMu.RLock()
	candidates := make([]executedSlotSnapshot, 0, len(log.slots))
	for key, slot := range log.slots {
		if key.View < 1 || key.View > currView || slot == nil {
			continue
		}

		slot.mu.Lock()
		if !slot.executed {
			slot.mu.Unlock()
			continue
		}

		snapshot := executedSlotSnapshot{
			seq:              key.SeqNum,
			view:             slot.view,
			prepareVotes:     len(slot.prepares),
			commitVotes:      len(slot.commits),
			prepareSent:      slot.prepareSent,
			commitSent:       slot.commitSent,
			executionPending: slot.executionPending,
			executed:         slot.executed,
			missingData:      slot.missingData,
		}
		if slot.prePrepare != nil {
			snapshot.digest = slot.prePrepare.DigestClientMsg
			snapshot.dataID = slot.prePrepare.ClientMsg.Data.Id
			snapshot.hasClientData = slot.prePrepare.DigestClientMsg != [32]byte{} || !slot.missingData
		}
		slot.mu.Unlock()

		if snapshot.view != key.View {
			recordWarning(key.SeqNum, "Warning: inconsistent view for seq %d: key view %d, slot view %d\n", key.SeqNum, key.View, snapshot.view)
		}
		candidates = append(candidates, snapshot)
	}
	log.slotsMu.RUnlock()

	slices.SortFunc(candidates, func(a, b executedSlotSnapshot) int {
		if a.view != b.view {
			if a.view < b.view {
				return -1
			}
			return 1
		}
		if a.seq < b.seq {
			return -1
		}
		if a.seq > b.seq {
			return 1
		}
		return 0
	})

	collected := make(map[int64]executedSlotSnapshot, len(candidates))
	for _, candidate := range candidates {
		existing, exists := collected[candidate.seq]
		if !exists {
			collected[candidate.seq] = candidate
			continue
		}
		if existing.digest != candidate.digest {
			recordWarning(candidate.seq, "Warning: executed seq %d appears in multiple views with different digests: kept view %d digest %x, skipped view %d digest %x\n",
				candidate.seq, existing.view, existing.digest, candidate.view, candidate.digest)
			continue
		}
	}

	if len(collected) == 0 {
		fmt.Printf("No executed slots found up to view %d\n", currView)
		fmt.Printf("Total warnings: %d, SeqNums: []\n", warningCount)
		fmt.Printf("Total executed slots: 0\n")
		return
	}

	sortedSeqNums := make([]int64, 0, len(collected))
	for seq := range collected {
		sortedSeqNums = append(sortedSeqNums, seq)
	}
	slices.Sort(sortedSeqNums)

	var prevSeq int64
	for i, seq := range sortedSeqNums {
		slot := collected[seq]
		if i > 0 && seq != prevSeq+1 {
			recordWarning(prevSeq, "Warning: missing executed slots between seq %d and seq %d\n", prevSeq, seq)
			warningSeqs[seq] = struct{}{}
		}

		if slot.hasClientData {
			fmt.Printf("SeqNum: %d, View: %d, Data ID: %d, Digest: %x, PrepareVotes: %d, CommitVotes: %d, PrepareSent: %t, CommitSent: %t, ExecutionPending: %t, Executed: %t, MissingData: %t\n",
				slot.seq, slot.view, slot.dataID, slot.digest, slot.prepareVotes, slot.commitVotes, slot.prepareSent, slot.commitSent, slot.executionPending, slot.executed, slot.missingData)
		} else {
			fmt.Printf("SeqNum: %d, View: %d, Data ID: <unavailable>, Digest: %x, PrepareVotes: %d, CommitVotes: %d, PrepareSent: %t, CommitSent: %t, ExecutionPending: %t, Executed: %t, MissingData: %t\n",
				slot.seq, slot.view, slot.digest, slot.prepareVotes, slot.commitVotes, slot.prepareSent, slot.commitSent, slot.executionPending, slot.executed, slot.missingData)
		}
		prevSeq = seq
	}

	warningSeqList := make([]int64, 0, len(warningSeqs))
	for seq := range warningSeqs {
		warningSeqList = append(warningSeqList, seq)
	}
	slices.Sort(warningSeqList)

	fmt.Printf("Total warnings: %d, SeqNums: %v\n", warningCount, warningSeqList)
	fmt.Printf("Total executed slots: %d\n", len(sortedSeqNums))
}

func (log *ConsensusLog) PrintDetails(view int64) {
	for i := int64(1); i <= view; i++ {
		fmt.Printf("-----------------------------------\n\n")
		fmt.Printf("View %d:\n", i)
		newmap := make(map[int64]*consensusSlot)
		for key, value := range log.slots {
			if key.View != i {
				continue
			}
			seqNum := key.SeqNum
			slot := value
			if key.View != slot.view {
				fmt.Printf("Inconsistent view for seq %d: key view %d, slot view %d", seqNum, key.View, slot.view)
			}
			// log.log.Info(fmt.Sprintf("SeqNum: %d, View: %d, Digest: %x, PrepareVotes: %d, CommitVotes: %d, PrepareSent: %t, CommitSent: %t, Executed: %t",
			// 	seqNum, slot.view, slot.digest, len(slot.prepares), len(slot.commits), slot.prepareSent, slot.commitSent, slot.executed))

			// for nodeID, prepareDigest := range slot.prepares {
			// 	log.log.Info(fmt.Sprintf("  Prepare Vote from Node %d: Digest %x", nodeID, prepareDigest))
			// }
			// for nodeID, commitDigest := range slot.commits {
			// 	log.log.Info(fmt.Sprintf("  Commit Vote from Node %d: Digest %x", nodeID, commitDigest))
			// }
			// return true
			newmap[seqNum] = slot

		}
		// sort the map by seqNum
		sortedSeqNums := make([]int64, 0, len(newmap))
		for seqNum := range newmap {
			sortedSeqNums = append(sortedSeqNums, seqNum)
		}
		slices.Sort(sortedSeqNums)
		// fmt.Printf("Total lenght of log: %d\n", len(sortedSeqNums))
		for _, seqNum := range sortedSeqNums {
			slot := newmap[seqNum]
			if !slot.executed || !slot.executionPending {
				fmt.Printf("SeqNum: %d, View: %d, Data ID: %d, PrepareVotes: %d, CommitVotes: %d, PrepareSent: %t, CommitSent: %t, ExecutionPending: %t, Executed: %t, MissingData: %t\n",
					seqNum, slot.view, slot.prePrepare.ClientMsg.Data.Id, len(slot.prepares), len(slot.commits), slot.prepareSent, slot.commitSent, slot.executionPending, slot.executed, slot.missingData)
				for nodeID, prepareDigest := range slot.prepares {
					fmt.Printf("  Prepare Vote from Node %d: Digest %x\n", nodeID, prepareDigest)
				}
				for nodeID, commitDigest := range slot.commits {
					fmt.Printf("  Commit Vote from Node %d: Digest %x\n", nodeID, commitDigest)
				}
			}
		}
	}

}

func (log *ConsensusLog) getOrCreateLog(seq int64, view int64) *consensusSlot {
	// if v, ok := log.slots.Load(seq); ok {
	// 	return v.(*consensusSlot)
	// }
	// entry := &consensusSlot{
	// 	digest:   [32]byte{},
	// 	view:     view,
	// 	prepares: make(map[int]*core.PrepareMsgSig),
	// 	commits:  make(map[int][32]byte),
	// }
	// actual, _ := log.slots.LoadOrStore(seq, entry)
	// return actual.(*consensusSlot)

	log.slotsMu.Lock()
	defer log.slotsMu.Unlock()

	if slot, exists := log.slots[slotKey{View: view, SeqNum: seq}]; exists {
		return slot
	}
	slot := &consensusSlot{

		view:     view,
		prepares: make(map[int]*core.PrepareMsgSig),
		commits:  make(map[int][32]byte),
	}
	log.slots[slotKey{View: view, SeqNum: seq}] = slot
	return slot
}

// func (log *ConsensusLog) getSlot(seq int64) (*consensusSlot, bool) {
// 	v, ok := log.slots.Load(seq)
// 	if !ok {
// 		return nil, false
// 	}
// 	return v.(*consensusSlot), true
// }

// // resetForView wipes all consensus state for a new view.
// // Caller MUST hold cl.mu.
// func (slot *consensusSlot) resetForView(newView int64) {
// 	slot.view = newView
// 	slot.prePrepare = nil
// 	slot.prePrepareSig = nil
// 	slot.digest = [32]byte{}
// 	slot.prepares = make(map[int]*core.PrepareMsgSig)
// 	slot.commits = make(map[int][32]byte)
// 	slot.prepareSent = false
// 	slot.commitSent = false
// 	slot.executionPending = false
// 	slot.executed = false
// 	// executed intentionally NOT reset — if we already executed this seq
// 	// in an older view, we must not execute it again.
// }

// func (slot *consensusSlot) resetForNewView(newView int64, newDigest [32]byte) bool {
// 	if slot.digest != newDigest {
// 		slot.view = newView
// 		slot.prePrepare = nil
// 		slot.prePrepareSig = nil
// 		slot.digest = [32]byte{}
// 		slot.prepares = make(map[int]*core.PrepareMsgSig)
// 		slot.commits = make(map[int][32]byte)
// 		slot.prepareSent = false
// 		slot.commitSent = false
// 		slot.executionPending = false
// 		slot.executed = false
// 		return true
// 	} else {
// 		slot.view = newView
// 		slot.prepares = make(map[int]*core.PrepareMsgSig)
// 		slot.commits = make(map[int][32]byte)
// 		slot.prepareSent = false
// 		slot.commitSent = false
// 		slot.executionPending = false
// 		slot.executed = false // executed is committed
// 		return false
// 	}

// }
