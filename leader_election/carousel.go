package leader_election

import (
	"fmt"
	"slices"

	"github.com/michael112233/pbft/config"
)

func (l *LeaderElection) GetFromCarousel(viewId int64) string {
	leader := ""
	if _, exists := l.leaderList[viewId]; exists {
		leader = config.NodeAddr[int(l.leaderList[viewId])]
		fmt.Println(fmt.Sprintf("viewId: %d, leader: %s", viewId, leader))
		return leader
	}

	if viewId < l.cfg.FaultyNodesNum {
		l.leaderList[viewId] = viewId % l.nodeNum
		leader = config.NodeAddr[int(viewId%l.nodeNum)]
		fmt.Println(fmt.Sprintf("viewId: %d, leader: %s", viewId, leader))
		return leader
	}

	ActiveLeader := make([]int64, 0)
	for i := int64(len(l.leaderList) - int(l.cfg.FaultyNodesNum)); i < int64(len(l.leaderList)); i++ {
		ActiveLeader = append(ActiveLeader, l.leaderList[i])
	}
	for i := int64(0); i < l.nodeNum; i++ {
		if !slices.Contains(ActiveLeader, i) {
			l.leaderList[viewId] = i
			leader = config.NodeAddr[int(i)]
			fmt.Println(fmt.Sprintf("viewId: %d, leader: %s", viewId, leader))
			break
		}
	}
	return leader
}
