package node

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
)

// --------------------------------------------------------
// Garbage Collection Principle Definition
// --------------------------------------------------------

func (n *Node) StartGarbageCollection() {
	n.checkpointLock.Lock()
	defer n.checkpointLock.Unlock()

	n.lastStableCheckpoint = -1
	n.checkpointList = make(map[int64]*atomic.Int32)
	for i := int64(n.cfg.SeqNumberLowerBound); i <= int64(n.cfg.SeqNumberUpperBound); i++ {
		n.checkpointList[i] = &atomic.Int32{}
		n.checkpointList[i].Store(0)
	}
}

func (n *Node) TriggerGarbageCollection(seqNumber int64, digest string) {
	n.log.Info(fmt.Sprintf("Check whether it is time to trigger garbage collection for sequence number %d", seqNumber))
	if (seqNumber-n.initCommitSeqNumber)%n.cfg.CheckpointInterval != 0 {
		return
	}
	n.log.Info(fmt.Sprintf("Trigger garbage collection for sequence number %d", seqNumber))

	n.checkpointLock.RLock()
	checkpointCounter := n.checkpointList[seqNumber]
	n.checkpointLock.RUnlock()

	checkpointCounter.Add(1)
	n.SendCheckpointMessage(seqNumber, digest)
}

func (n *Node) SendCheckpointMessage(sequenceNumber int64, digest string) {
	for _, othersIp := range config.NodeAddr {
		if othersIp == n.GetAddr() {
			continue
		}
		// TODO: the sequence number should be the last sequence number of the block committed on the blockchain
		checkpointMessage := core.CheckpointMessage{
			Timestamp:      time.Now().UnixNano(),
			From:           n.GetAddr(),
			To:             othersIp,
			SequenceNumber: sequenceNumber,
			Digest:         digest,
		}
		n.log.Info(fmt.Sprintf("Send checkpoint message to %s", othersIp))
		n.messageHub.Send(core.MsgCheckpointMessage, othersIp, checkpointMessage, nil)
	}
}

func (n *Node) HandleCheckpointMessage(data core.CheckpointMessage) {
	// n.handleMessageLock.Lock()
	// defer n.handleMessageLock.Unlock()
	n.log.Info(fmt.Sprintf("Received checkpoint message from %s, sequence number %d", data.From, data.SequenceNumber))

	// Get checkpoint counter with read lock
	n.checkpointLock.RLock()
	checkpointCounter := n.checkpointList[data.SequenceNumber]
	n.checkpointLock.RUnlock()

	checkpointCounter.Add(1)

	// Check digest with read lock
	n.seq2digestLock.RLock()
	expectedDigest := n.seq2digest[data.SequenceNumber]
	n.seq2digestLock.RUnlock()

	if data.Digest != expectedDigest {
		n.log.Error(fmt.Sprintf("Checkpoint message digest mismatch. from %s, sequence number %d", data.From, data.SequenceNumber))
		return
	}

	if checkpointCounter.Load() == int32(2*n.cfg.FaultyNodesNum+1) {
		n.lastStableCheckpoint = data.SequenceNumber
		n.log.Debug(fmt.Sprintf("Node %d last stable checkpoint is %d", n.NodeID, n.lastStableCheckpoint))
	}
}
