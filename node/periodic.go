package node

func (n *Node) periodicVC() {
	n.viewMu.Lock()
	n.pbftTimerManager.forceStopPBFTTimer()
	if n.viewChangeRunning {
		n.log.Info("Periodic VC: view change already running, skipping periodic VC")
		n.viewMu.Unlock()
		return
	}
	n.log.Info("Starting periodic view change for view %d", n.forView)
	n.handleViewChangeTimeoutDummy()
	n.viewMu.Unlock()
}
