package leader_election

import "github.com/michael112233/pbft/config"

func (l *LeaderElection) GetFromRoundRobin(viewId int64) string {
	return config.NodeAddr[int(viewId%l.nodeNum)]
}
