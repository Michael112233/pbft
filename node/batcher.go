package node

import (
	"time"

	"github.com/michael112233/pbft/core"
)

type Batcher struct {
	batch            []core.ClientMsgSignature
	maxBatchSize     int
	maxBatchWaitTime time.Duration
	batchTimer       *time.Timer
}

func (n *Node) ArmBatchTimer() {
	if n.batchLogic.batchTimer == nil {
		n.batchLogic.batchTimer = time.NewTimer(n.batchLogic.maxBatchWaitTime)
	} else {
		n.batchLogic.batchTimer.Reset(n.batchLogic.maxBatchWaitTime)
	}
}

func (n *Node) StopBatchTimer() {
	if n.batchLogic.batchTimer != nil {
		n.batchLogic.batchTimer.Stop()
	}
}

func (n *Node) GetBatchSize() int {
	return n.batchLogic.maxBatchSize
}
