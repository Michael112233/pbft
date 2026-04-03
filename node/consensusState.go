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

func (log *ConsensusLog) PrintSlot(seqNum int64) {
	if v, ok := log.slots.Load(seqNum); ok {
		slot := v.(*consensusSlot)
		fmt.Printf("SeqNum: %d, View: %d, Digest: %x, PrepareVotes: %d, CommitVotes: %d, PrepareSent: %t, CommitSent: %t, Executed: %t\n",
			seqNum, slot.view, slot.digest, len(slot.prepares), len(slot.commits), slot.prepareSent, slot.commitSent, slot.executed)
		fmt.Printf("Message details %d\n, data %v", slot.prePrepare.ClientMsg.Data.Id, slot.prePrepare.ClientMsg.Data)
	}
}

func (log *ConsensusLog) PrintDetails() {
	newmap := make(map[int]*consensusSlot)
	log.slots.Range(func(key, value any) bool {
		seqNum := key.(int64)
		slot := value.(*consensusSlot)
		// log.log.Info(fmt.Sprintf("SeqNum: %d, View: %d, Digest: %x, PrepareVotes: %d, CommitVotes: %d, PrepareSent: %t, CommitSent: %t, Executed: %t",
		// 	seqNum, slot.view, slot.digest, len(slot.prepares), len(slot.commits), slot.prepareSent, slot.commitSent, slot.executed))

		// for nodeID, prepareDigest := range slot.prepares {
		// 	log.log.Info(fmt.Sprintf("  Prepare Vote from Node %d: Digest %x", nodeID, prepareDigest))
		// }
		// for nodeID, commitDigest := range slot.commits {
		// 	log.log.Info(fmt.Sprintf("  Commit Vote from Node %d: Digest %x", nodeID, commitDigest))
		// }
		// return true
		newmap[int(seqNum)] = slot
		return true
	})
	// sort the map by seqNum
	sortedSeqNums := make([]int, 0, len(newmap))
	for seqNum := range newmap {
		sortedSeqNums = append(sortedSeqNums, int(seqNum))
	}
	slices.Sort(sortedSeqNums)
	fmt.Printf("Total lenght of log: %d\n", len(sortedSeqNums))
	for _, seqNum := range sortedSeqNums {
		slot := newmap[seqNum]
		if !slot.executed || seqNum == 300 || seqNum == 14356 || seqNum == 10234 {
			fmt.Printf("SeqNum: %d, View: %d, Data ID: %d, PrepareVotes: %d, CommitVotes: %d, PrepareSent: %t, CommitSent: %t, Executed: %t\n",
				seqNum, slot.view, slot.prePrepare.ClientMsg.Data.Id, len(slot.prepares), len(slot.commits), slot.prepareSent, slot.commitSent, slot.executed)
			for nodeID, prepareDigest := range slot.prepares {
				fmt.Printf("  Prepare Vote from Node %d: Digest %x\n", nodeID, prepareDigest)
			}
			for nodeID, commitDigest := range slot.commits {
				fmt.Printf("  Commit Vote from Node %d: Digest %x\n", nodeID, commitDigest)
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
		digest:   [32]byte{},
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
