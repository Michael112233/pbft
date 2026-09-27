package node

// Aggregate grace timer (epoch aggregator only). Started once 2f+1 epoch data
// messages are in; when it fires the aggregate is sent with whatever arrived,
// unless all n arrived first. Loop-owned, like the other timers.

func (n *Node) startAggregateGraceTimer() {
	n.aggregateGraceTimerCh = resetOneShotTimer(&n.aggregateGraceTimer, n.cfg.AwareGrace())
}

func (n *Node) stopAggregateGraceTimer() {
	stopOneShotTimer(n.aggregateGraceTimer)
	n.aggregateGraceTimerCh = nil
}

func (n *Node) handleAggregateGraceTimeout() {
	n.stopAggregateGraceTimer()
	n.epochManager.onGraceTimeout()
}
