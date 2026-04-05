package node

func (n *Node) periodicVC() {
	n.viewMu.Lock()
	n.handleViewChangeTimeoutDummy()
	n.viewMu.Unlock()
}
