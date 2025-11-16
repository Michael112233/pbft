package leader_election

import (
	"os"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/logger"
)

var log = logger.NewLogger(0, "leader_election")

type LeaderElection struct {
	cfg        *config.Config
	method     string
	nodeNum    int64
	leaderList map[int64]int64
}

func NewLeaderElection(config *config.Config) *LeaderElection {
	return &LeaderElection{
		cfg:        config,
		method:     config.ElectionMethod,
		nodeNum:    config.NodeNum,
		leaderList: make(map[int64]int64),
	}
}

func (l *LeaderElection) GetLeader(viewId int64) string {
	switch l.method {
	case "round_robin":
		return l.GetFromRoundRobin(viewId)
	case "carousel":
		return l.GetFromCarousel(viewId)
	default:
		log.Error("invalid election method: %s", l.method)
		os.Exit(1)
		return ""
	}
}
