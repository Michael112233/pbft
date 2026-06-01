package node

import "time"

func (n *Node) recordClientRequestReceived(txCount int) {
	if txCount <= 0 {
		return
	}
	n.clientReceivedTxs.Add(int64(txCount))
}

func (n *Node) clientReceiveRateLogger() {
	defer close(n.clientReceiveRateDone)

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	var last int64
	for {
		select {
		case <-ticker.C:
			current := n.clientReceivedTxs.Load()
			delta := current - last
			last = current
			n.log.Warn("node client receive rate: %d tx/s total_received=%d", delta, current)
		case <-n.clientReceiveRateStop:
			return
		}
	}
}
