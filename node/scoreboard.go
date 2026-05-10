package node

type ScoreboardEntry struct {
	score    int
	view     int64
	leaderId int
}
type Scoreboard struct {
	scoreboardUpdates map[int64]ScoreboardEntry
	scores            map[int]int
}

func NewScoreboard() *Scoreboard {
	scoreboard := make(map[int64]ScoreboardEntry)
	scoreboard[0] = ScoreboardEntry{
		score:    0,
		view:     0,
		leaderId: -1,
	}
	return &Scoreboard{
		scoreboardUpdates: scoreboard,
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
