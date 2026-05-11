package node

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

type ScoreboardEntry struct {
	score    int
	view     int64
	leaderId int
}
type Scoreboard struct {
	scoreboardUpdates map[int64]ScoreboardEntry
	scores            map[int]int
	priorities        map[int]int
	highestView       int64
}

func NewScoreboard(NumNodes int64) *Scoreboard {

	scoreboard := make(map[int64]ScoreboardEntry)
	scoreboard[0] = ScoreboardEntry{
		score:    0,
		view:     0,
		leaderId: -1,
	}
	scores := make(map[int]int)
	priorities := make(map[int]int)
	for i := 1; i <= int(NumNodes); i++ {
		scores[i] = 1
		if i == 1 {
			priorities[i] = -3
		} else {
			priorities[i] = 1
		}

	}
	return &Scoreboard{
		scoreboardUpdates: scoreboard,
		scores:            scores,
		priorities:        priorities,
		highestView:       0,
	}
}
func (s *Scoreboard) Update(view int64, score int, leaderId int) bool {
	_, exists := s.scoreboardUpdates[view]
	if !exists {
		s.scoreboardUpdates[view] = ScoreboardEntry{
			score:    score,
			view:     view,
			leaderId: leaderId,
		}
		return true
	}
	return false
}

func (s *Scoreboard) String() string {
	if s == nil {
		return "priorities\nscores"
	}

	nodeIDs := make([]int, 0, len(s.scores))

	for nodeID := range s.scores {
		nodeIDs = append(nodeIDs, nodeID)

	}

	sort.Ints(nodeIDs)

	priorities := make([]string, 0, len(nodeIDs))
	scores := make([]string, 0, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		priorities = append(priorities, fmt.Sprintf("%d,%d", nodeID, s.priorities[nodeID]))
		scores = append(scores, fmt.Sprintf("%d,%d", nodeID, s.scores[nodeID]))
	}

	return fmt.Sprintf("\npriorities\n%s\nscores\n%s", strings.Join(priorities, "; "), strings.Join(scores, "; "))
}

func (s *Scoreboard) PrintScoresAndPriorities() {
	fmt.Println(s.String())
}

func (s *Scoreboard) GetLeader(newView, currentView int64) int {
	if s == nil || len(s.scores) == 0 {
		return 0
	}
	rounds := newView - currentView
	if rounds <= 0 {
		return 0
	}

	nodeIDs := make([]int, 0, len(s.scores))
	scores := make(map[int]int, len(s.scores))
	priorities := make(map[int]int, len(s.scores))
	totalScore := 0
	for nodeID, score := range s.scores {
		nodeIDs = append(nodeIDs, nodeID)
		scores[nodeID] = score
		priorities[nodeID] = s.priorities[nodeID]
		totalScore += score
	}
	sort.Ints(nodeIDs)

	return runWeightedRoundRobin(rounds, nodeIDs, scores, priorities, totalScore)
}

func (s *Scoreboard) UpdatePriorities(newView, currentView int64) int {
	if s == nil || len(s.scores) == 0 {
		return 0
	}
	rounds := newView - currentView
	if rounds <= 0 {
		return 0
	}
	if s.priorities == nil {
		s.priorities = make(map[int]int, len(s.scores))
	}

	nodeIDs := make([]int, 0, len(s.scores))
	totalScore := 0
	for nodeID, score := range s.scores {
		nodeIDs = append(nodeIDs, nodeID)
		totalScore += score
	}
	sort.Ints(nodeIDs)

	leaderID := runWeightedRoundRobin(rounds, nodeIDs, s.scores, s.priorities, totalScore)
	// if newView > s.highestView {
	// 	s.highestView = newView
	// }

	return leaderID
}

func runWeightedRoundRobin(rounds int64, nodeIDs []int, scores map[int]int, priorities map[int]int, totalScore int) int {
	leaderID := 0
	for round := int64(0); round < rounds; round++ {
		leaderID = nodeIDs[0]
		leaderPriority := priorities[leaderID] + scores[leaderID]

		for _, nodeID := range nodeIDs {
			priority := priorities[nodeID] + scores[nodeID]
			priorities[nodeID] = priority
			if priority > leaderPriority {
				leaderID = nodeID
				leaderPriority = priority
			}
		}

		priorities[leaderID] -= totalScore
	}
	return leaderID
}

func BucketThroughput(throughput float64, alpha float64) (int, error) {
	if alpha <= 0 {
		return 0, errors.New("alpha must be positive")
	}
	// if qMax < 0 {
	// 	return 0, errors.New("qMax must be non-negative")
	// }

	q := int(math.Floor(alpha * throughput))
	if q < 0 {
		return 0, nil
	}
	// if q > qMax {
	// 	return qMax, nil
	// }
	return q, nil
}

func MedianBucket(throughputs []float64, alpha float64) (int, error) {
	if len(throughputs) == 0 {
		return 0, errors.New("throughputs must not be empty")
	}

	buckets := make([]int, 0, len(throughputs))
	for _, throughput := range throughputs {
		bucket, err := BucketThroughput(throughput, alpha)
		if err != nil {
			return 0, err
		}
		buckets = append(buckets, bucket)
	}
	sort.Ints(buckets)

	return buckets[(len(buckets)-1)/2], nil
}

func EMAUpdateInt(oldScore int, sample int, d int) (int, error) {
	if d <= 0 {
		return 0, errors.New("d must be positive")
	}
	if oldScore < 0 || sample < 0 {
		return 0, errors.New("oldScore and sample must be non-negative")
	}

	return ((d-1)*oldScore + sample + d/2) / d, nil
}

func (s *Scoreboard) UpdateScore(nodeID int, throughputs []float64, alpha float64, d int) (int, error) {
	medianBucket, err := MedianBucket(throughputs, alpha)
	if err != nil {
		return 0, err
	}
	oldScore := s.scores[nodeID]
	newScore, err := EMAUpdateInt(oldScore, medianBucket, d)
	if err != nil {
		return 0, err
	}
	s.scores[nodeID] = newScore
	return newScore, nil
}
