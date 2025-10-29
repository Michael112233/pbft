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
	if (seqNumber-n.cfg.SeqNumberLowerBound)%n.cfg.CheckpointInterval != 0 {
		return
	}
	n.log.Info(fmt.Sprintf("Trigger garbage collection for sequence number %d", seqNumber))

	if seqNumber < n.lastStableCheckpoint {
		return
	}

	n.checkpointLock.RLock()
	if _, exists := n.checkpointList[seqNumber]; !exists {
		n.checkpointList[seqNumber] = &atomic.Int32{}
		n.checkpointList[seqNumber].Store(0)
	}
	n.checkpointList[seqNumber].Add(1)
	n.checkpointLock.RUnlock()

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

	if data.SequenceNumber < n.lastStableCheckpoint {
		return
	}

	// Get checkpoint counter with read lock
	n.checkpointLock.RLock()
	if _, exists := n.checkpointList[data.SequenceNumber]; !exists {
		n.checkpointList[data.SequenceNumber] = &atomic.Int32{}
		n.checkpointList[data.SequenceNumber].Store(0)
	}
	n.checkpointList[data.SequenceNumber].Add(1)
	n.checkpointLock.RUnlock()

	if n.checkpointList[data.SequenceNumber].Load() == int32(2*n.cfg.FaultyNodesNum) {
		n.lastStableCheckpoint = data.SequenceNumber
		n.log.Debug(fmt.Sprintf("Node %d last stable checkpoint is %d", n.NodeID, n.lastStableCheckpoint))

		// 清理sequenceNumber之前的信息以释放内存
		n.cleanupOldData(data.SequenceNumber)
	}
}

// cleanupOldData 清理指定序列号之前的所有数据以释放内存
func (n *Node) cleanupOldData(stableCheckpoint int64) {
	n.log.Info(fmt.Sprintf("Starting garbage collection: cleaning data before sequence number %d", stableCheckpoint))

	// 清理预准备消息
	n.preprepareSeqLock.Lock()
	for seq := range n.preprepareMsg {
		if seq < stableCheckpoint {
			delete(n.preprepareMsg, seq)
		}
	}
	n.preprepareSeqLock.Unlock()

	// 清理准备消息计数器
	n.PrepareMessageLock.Lock()
	for seq := range n.prepareMsgNumber {
		if seq < stableCheckpoint {
			delete(n.prepareMsgNumber, seq)
		}
	}
	n.PrepareMessageLock.Unlock()

	// 清理提交消息计数器
	n.CommitMessageLock.Lock()
	for seq := range n.commitMsgNumber {
		if seq < stableCheckpoint {
			delete(n.commitMsgNumber, seq)
		}
	}
	n.CommitMessageLock.Unlock()

	// 清理序列号到摘要映射
	n.seq2digestLock.Lock()
	for seq := range n.seq2digest {
		if seq < stableCheckpoint {
			delete(n.seq2digest, seq)
		}
	}
	n.seq2digestLock.Unlock()

	// 清理检查点列表
	n.checkpointLock.Lock()
	for seq := range n.checkpointList {
		if seq < stableCheckpoint {
			delete(n.checkpointList, seq)
		}
	}
	n.checkpointLock.Unlock()

	n.log.Info(fmt.Sprintf("Garbage collection completed: cleaned data before sequence number %d", stableCheckpoint))
}
