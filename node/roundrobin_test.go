package node

import (
	"testing"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/logger"
)

func TestPrimaryForViewUsesOneBasedNodeIDs(t *testing.T) {
	n := &Node{
		cfg: &config.Config{
			NodeNum: 4,
		},
	}

	tests := []struct {
		view     int64
		expected int
	}{
		{view: 1, expected: 1},
		{view: 2, expected: 2},
		{view: 3, expected: 3},
		{view: 4, expected: 4},
		{view: 5, expected: 1},
		{view: 6, expected: 2},
		{view: 7, expected: 3},
		{view: 8, expected: 4},
	}

	for _, tt := range tests {
		if got := n.primaryForView(tt.view, -1); got != tt.expected {
			t.Fatalf("primaryForView(%d) = %d, want %d", tt.view, got, tt.expected)
		}
	}
}

func TestRoundRobinLeaderSelectionTreatsNodeFourAsLeaderForViewFour(t *testing.T) {
	n := &Node{
		NodeID:  4,
		forView: 4,
		cfg: &config.Config{
			NodeNum: 4,
		},
	}

	expectedLeader := n.primaryForView(n.forView, -1)
	if expectedLeader != 4 {
		t.Fatalf("primaryForView(%d) = %d, want 4", n.forView, expectedLeader)
	}
	if expectedLeader != n.GetNodeID() {
		t.Fatalf("node %d should be treated as the leader for view %d", n.GetNodeID(), n.forView)
	}
}

func TestRoundRobinLeaderSelectionDoesNotTreatNodeOneAsLeaderForViewFour(t *testing.T) {
	n := &Node{
		NodeID:  1,
		forView: 4,
		cfg: &config.Config{
			NodeNum: 4,
		},
	}

	expectedLeader := n.primaryForView(n.forView, -1)
	if expectedLeader != 4 {
		t.Fatalf("primaryForView(%d) = %d, want 4", n.forView, expectedLeader)
	}
	if expectedLeader == n.GetNodeID() {
		t.Fatalf("node %d should not be treated as the leader for view %d", n.GetNodeID(), n.forView)
	}
}

func TestPrimaryForViewUsesStableCheckpointVotesWhenActiveLEnabled(t *testing.T) {
	n := &Node{
		cfg: &config.Config{
			NodeNum: 4,
			ActiveL: true,
		},
		fNodes: 1,
		log:    logger.NewLogger(1, "node"),
		checkpoints: map[checkpoint]CheckpointData{
			{seq: 20}: {
				votes: map[int]core.CheckpointMsgSig{
					3: {},
					1: {},
					2: {},
				},
			},
		},
		lastStableCheckpoint: checkpoint{seq: 20},
	}

	if got := n.primaryForView(7, -1); got != 1 {
		t.Fatalf("primaryForView(%d) = %d, want 1 from stable checkpoint voters", 7, got)
	}
}

func TestPrimaryForViewFallsBackToRoundRobinWhenActiveLHasNoVotes(t *testing.T) {
	n := &Node{
		cfg: &config.Config{
			NodeNum: 4,
			ActiveL: true,
		},
		fNodes:               1,
		log:                  logger.NewLogger(1, "node"),
		checkpoints:          make(map[checkpoint]CheckpointData),
		lastStableCheckpoint: checkpoint{seq: 20},
	}

	if got := n.primaryForView(6, -1); got != 2 {
		t.Fatalf("primaryForView(%d) = %d, want round-robin fallback 2", 6, got)
	}
}
