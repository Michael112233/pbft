package node

import (
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

type ConsensusSlot struct {
	// View         int64
	// SeqNum       int64
	// Digest       [32]byte
	ClientMsgs   []core.ClientMsgSignature
	PrepareVotes map[int]bool // can use bitmap for optimization
	CommitVotes  map[int]bool
	Phase        ConsensusPhase

	mu sync.Mutex // Per-slot lock
}

type ConsensusState struct {
	slots map[slotKey]*ConsensusSlot
	// currentView int64
	// f           int // Byzantine fault tolerance parameter

	mu sync.RWMutex // Protects map access
}

func NewConsensusState() *ConsensusState {
	return &ConsensusState{
		slots: make(map[slotKey]*ConsensusSlot), // capacity
		// currentView: 0,
		// f:           f,
	}
}

func NewConsensusSlot() *ConsensusSlot {


