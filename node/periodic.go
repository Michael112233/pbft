package node

import (
	"github.com/michael112233/pbft/core"
)

func (n *Node) periodicVC() {

	n.viewMu.Lock()
	n.pbftTimerManager.forceStopPBFTTimer()

	if n.vcType == core.VCTypeElection {
		n.pbftTimerManager.startPeriodicElectionTimer(n)
		n.viewMu.Unlock()
		return
	}

	if n.viewChangeRunning {
		n.log.Info("Periodic VC: view change already running, skipping periodic VC")
		n.viewMu.Unlock()
		return
	}
	n.log.Info("Starting periodic view change my for view %d and my n.view %d", n.forView, n.view)
	n.handleViewChangeTimeoutDummy()
	n.viewMu.Unlock()
}
