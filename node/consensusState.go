package node

import (
	"fmt"
	"slices"
	"sync"

	"github.com/michael112233/pbft/core"
)

type ConsensusPhase int

const (
	PhaseNone ConsensusPhase = iota
	PhasePreprepared
	PhasePrepared
	PhaseCommitted
)

type slotKey struct {
	View   int64
	SeqNum int64
	Digest [32]byte
}

type consensusSlot struct {
	mu sync.Mutex

	view int64 // view this log entry belongs to

	// PrePrepare (nil until received/created)
	prePrepare *core.PreprepareMsg
	digest     [32]byte

	// Vote sets — key is sender's NodeID, value is the digest they voted for.
	// This defends against equivocation: a Byzantine leader can send different
	// digests to different replicas, so we must only count votes matching our
	// accepted PrePrepare digest at quorum-check time.
	prepares map[int][32]byte
	commits  map[int][32]byte

	// One-shot flags so we broadcast exactly once per phase transition
	prepareSent bool // did *this* node already broadcast Prepare
	commitSent  bool // did *this* node already broadcast Commit
	executed    bool // already delivered to application
}

type ConsensusLog struct {
	slots sync.Map // int64(seqNum) -> *consensusSlot

}

func NewConsensusLog() *ConsensusLog {
	return &ConsensusLog{
		slots: sync.Map{}, // capacity

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
		fmt.Printf("SeqNum: %d, View: %d, Digest: %x, PrepareVotes: %d, CommitVotes: %d, PrepareSent: %t, CommitSent: %t, Executed: %t\n",
			seqNum, slot.view, slot.digest, len(slot.prepares), len(slot.commits), slot.prepareSent, slot.commitSent, slot.executed)
		for nodeID, prepareDigest := range slot.prepares {
			fmt.Printf("  Prepare Vote from Node %d: Digest %x\n", nodeID, prepareDigest)
		}
		for nodeID, commitDigest := range slot.commits {
			fmt.Printf("  Commit Vote from Node %d: Digest %x\n", nodeID, commitDigest)
		}
	}

}

func (log *ConsensusLog) getOrCreateLog(seq int64, view int64) *consensusSlot {
	if v, ok := log.slots.Load(seq); ok {
		return v.(*consensusSlot)
	}
	entry := &consensusSlot{
		digest:   [32]byte{},
		view:     view,
		prepares: make(map[int][32]byte),
		commits:  make(map[int][32]byte),
	}
	actual, _ := log.slots.LoadOrStore(seq, entry)
	return actual.(*consensusSlot)
}

// resetForView wipes all consensus state for a new view.
// Caller MUST hold cl.mu.
func (slot *consensusSlot) resetForView(newView int64) {
	slot.view = newView
	slot.prePrepare = nil
	slot.digest = [32]byte{}
	slot.prepares = make(map[int][32]byte)
	slot.commits = make(map[int][32]byte)
	slot.prepareSent = false
	slot.commitSent = false
	slot.executed = false
	// executed intentionally NOT reset — if we already executed this seq
	// in an older view, we must not execute it again.
}
