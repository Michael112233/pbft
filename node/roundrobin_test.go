package node

import (
	"testing"

	"github.com/michael112233/pbft/config"
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
		if got := n.primaryForView(tt.view); got != tt.expected {
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

	expectedLeader := n.primaryForView(n.forView)
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

	expectedLeader := n.primaryForView(n.forView)
	if expectedLeader != 4 {
		t.Fatalf("primaryForView(%d) = %d, want 4", n.forView, expectedLeader)
	}
	if expectedLeader == n.GetNodeID() {
		t.Fatalf("node %d should not be treated as the leader for view %d", n.GetNodeID(), n.forView)
	}
}
