package leader_election

import "github.com/michael112233/pbft/config"

func (l *LeaderElection) GetFromRaft(viewId int64) string {
	if viewId == 0 {
		return config.NodeAddr[0]
	}
	if _, exists := l.leaderList[viewId]; exists {
		return config.NodeAddr[int(l.leaderList[viewId])]
	}
	return "No leader"
}
