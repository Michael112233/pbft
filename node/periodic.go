package node

import (
	"github.com/michael112233/pbft/core"
)

func (n *Node) periodicVC(periodInterval int64) {

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
	if n.periodInterval != periodInterval {
		n.log.Info("Periodic VC: period interval mismatch, expected %d but got %d, skipping periodic VC", n.periodInterval, periodInterval)
		n.viewMu.Unlock()
		return
	}
	n.log.Info("Starting periodic view change my current for view %d and my n.view %d and the next for view will be %d", n.forView, n.view, n.forView+1)
	n.handleViewChangeTimeoutDummy()
	n.viewMu.Unlock()
}
