package node

import (
	"reflect"
	"testing"
)

func TestScoreboardGetLeaderUsesWeightedRoundRobin(t *testing.T) {
	scoreboard := NewScoreboard(4)

	expectedLeaders := []int{1, 2, 3, 4, 1, 2, 3, 4}
	for view, expectedLeader := range expectedLeaders {
		newView := int64(view + 1)
		if got := scoreboard.GetLeader(newView, 0); got != expectedLeader {
			t.Fatalf("GetLeader(%d, 0) = %d, want %d", newView, got, expectedLeader)
		}
	}
}

func TestScoreboardGetLeaderHonorsWeights(t *testing.T) {
	scoreboard := NewScoreboard(3)
	scoreboard.scores = map[int]int{
		1: 5,
		2: 1,
		3: 1,
	}

	expectedLeaders := []int{1, 1, 2, 1, 3, 1, 1}
	for view, expectedLeader := range expectedLeaders {
		newView := int64(view + 1)
		if got := scoreboard.GetLeader(newView, 0); got != expectedLeader {
			t.Fatalf("GetLeader(%d, 0) = %d, want %d", newView, got, expectedLeader)
		}
	}
}

func TestScoreboardGetLeaderBreaksTiesBySmallerNodeID(t *testing.T) {
	scoreboard := &Scoreboard{
		scores: map[int]int{
			3: 1,
			1: 1,
			2: 1,
		},
		priorities: map[int]int{
			3: 0,
			1: 0,
			2: 0,
		},
	}

	if got := scoreboard.GetLeader(1, 0); got != 1 {
		t.Fatalf("GetLeader(1, 0) = %d, want 1", got)
	}
}

func TestScoreboardGetLeaderRunsOncePerViewDifference(t *testing.T) {
	scoreboard := NewScoreboard(4)

	if got := scoreboard.GetLeader(3, 1); got != 2 {
		t.Fatalf("GetLeader(3, 1) = %d, want 2", got)
	}
}

func TestScoreboardGetLeaderDoesNotMutateScoresOrPriorities(t *testing.T) {
	scoreboard := NewScoreboard(3)
	scoreboard.scores = map[int]int{
		1: 5,
		2: 1,
		3: 1,
	}
	scoreboard.priorities = map[int]int{
		1: -2,
		2: 1,
		3: 1,
	}

	originalScores := map[int]int{
		1: 5,
		2: 1,
		3: 1,
	}
	originalPriorities := map[int]int{
		1: -2,
		2: 1,
		3: 1,
	}

	_ = scoreboard.GetLeader(6, 2)

	if !reflect.DeepEqual(scoreboard.scores, originalScores) {
		t.Fatalf("scores were mutated: got %v, want %v", scoreboard.scores, originalScores)
	}
	if !reflect.DeepEqual(scoreboard.priorities, originalPriorities) {
		t.Fatalf("priorities were mutated: got %v, want %v", scoreboard.priorities, originalPriorities)
	}
}

func TestScoreboardGetLeaderReturnsZeroForCurrentOrPastView(t *testing.T) {
	scoreboard := NewScoreboard(4)

	if got := scoreboard.GetLeader(2, 2); got != 0 {
		t.Fatalf("GetLeader(2, 2) = %d, want 0", got)
	}
}

func TestScoreboardUpdatePrioritiesMutatesActualPriorities(t *testing.T) {
	scoreboard := NewScoreboard(4)

	if got := scoreboard.UpdatePriorities(2, 0); got != 2 {
		t.Fatalf("UpdatePriorities(2, 0) = %d, want 2", got)
	}

	expectedPriorities := map[int]int{
		1: -2,
		2: -2,
		3: 2,
		4: 2,
	}
	if !reflect.DeepEqual(scoreboard.priorities, expectedPriorities) {
		t.Fatalf("priorities = %v, want %v", scoreboard.priorities, expectedPriorities)
	}
	if scoreboard.highestView != 2 {
		t.Fatalf("highestView = %d, want 2", scoreboard.highestView)
	}
}

func TestScoreboardUpdatePrioritiesContinuesFromCurrentPriorities(t *testing.T) {
	scoreboard := NewScoreboard(4)

	if got := scoreboard.UpdatePriorities(2, 0); got != 2 {
		t.Fatalf("first UpdatePriorities(2, 0) = %d, want 2", got)
	}
	if got := scoreboard.UpdatePriorities(4, 2); got != 4 {
		t.Fatalf("second UpdatePriorities(4, 2) = %d, want 4", got)
	}

	expectedPriorities := map[int]int{
		1: 0,
		2: 0,
		3: 0,
		4: 0,
	}
	if !reflect.DeepEqual(scoreboard.priorities, expectedPriorities) {
		t.Fatalf("priorities = %v, want %v", scoreboard.priorities, expectedPriorities)
	}
}

func TestScoreboardUpdatePrioritiesReturnsZeroForCurrentOrPastView(t *testing.T) {
	scoreboard := NewScoreboard(4)
	originalPriorities := map[int]int{
		1: 0,
		2: 0,
		3: 0,
		4: 0,
	}

	if got := scoreboard.UpdatePriorities(2, 2); got != 0 {
		t.Fatalf("UpdatePriorities(2, 2) = %d, want 0", got)
	}
	if !reflect.DeepEqual(scoreboard.priorities, originalPriorities) {
		t.Fatalf("priorities = %v, want %v", scoreboard.priorities, originalPriorities)
	}
}

func TestBucketThroughput(t *testing.T) {
	tests := []struct {
		name       string
		throughput float64
		alpha      float64
		qMax       int
		want       int
	}{
		{name: "floors scaled throughput", throughput: 997.3, alpha: 0.1, qMax: 200, want: 99},
		{name: "clips below zero", throughput: -12.5, alpha: 0.1, qMax: 200, want: 0},
		{name: "clips above maximum", throughput: 3000, alpha: 0.1, qMax: 200, want: 200},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BucketThroughput(tt.throughput, tt.alpha, tt.qMax)
			if err != nil {
				t.Fatalf("BucketThroughput returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("BucketThroughput() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestBucketThroughputRejectsInvalidArguments(t *testing.T) {
	if _, err := BucketThroughput(10, 0, 20); err == nil {
		t.Fatal("BucketThroughput with alpha 0 returned nil error")
	}
	if _, err := BucketThroughput(10, 1, -1); err == nil {
		t.Fatal("BucketThroughput with negative qMax returned nil error")
	}
}

func TestMedianBucket(t *testing.T) {
	got, err := MedianBucket([]float64{15, 997.3, 40, 25}, 0.1, 200)
	if err != nil {
		t.Fatalf("MedianBucket returned error: %v", err)
	}
	if got != 2 {
		t.Fatalf("MedianBucket() = %d, want 2", got)
	}
}

func TestMedianBucketRejectsEmptyThroughputs(t *testing.T) {
	if _, err := MedianBucket(nil, 0.1, 200); err == nil {
		t.Fatal("MedianBucket with empty throughputs returned nil error")
	}
}

func TestEMAUpdateInt(t *testing.T) {
	got, err := EMAUpdateInt(10, 20, 4)
	if err != nil {
		t.Fatalf("EMAUpdateInt returned error: %v", err)
	}
	if got != 13 {
		t.Fatalf("EMAUpdateInt() = %d, want 13", got)
	}
}

func TestEMAUpdateIntRejectsInvalidArguments(t *testing.T) {
	if _, err := EMAUpdateInt(10, 20, 0); err == nil {
		t.Fatal("EMAUpdateInt with d 0 returned nil error")
	}
	if _, err := EMAUpdateInt(-1, 20, 4); err == nil {
		t.Fatal("EMAUpdateInt with negative oldScore returned nil error")
	}
	if _, err := EMAUpdateInt(10, -1, 4); err == nil {
		t.Fatal("EMAUpdateInt with negative sample returned nil error")
	}
}
