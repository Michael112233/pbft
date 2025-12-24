package node

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/logger"
)

type Node struct {
	NodeID                  int64
	viewNumber              int64
	prepareMsgNumber        map[int64]*atomic.Int32
	preprepareMsg           map[int64][]*core.PreprepareMessage
	commitMsgNumber         map[int64]*atomic.Int32
	lastPreprepareSeqNumber int64
	lastPrepareSeqNumber    int64
	lastCommitSeqNumber     int64
	initCommitSeqNumber     int64
	lastStableCheckpoint    int64
	checkpointList          map[int64]*atomic.Int32
	seq2digest              map[int64]string
	preprepareSeqLock       sync.Mutex
	prepareSeqLock          sync.Mutex
	commitSeqLock           sync.Mutex
	PrepareMessageLock      sync.Mutex
	CommitMessageLock       sync.Mutex
	checkpointLock          sync.RWMutex
	seq2digestLock          sync.RWMutex

	cfg        *config.Config
	log        *logger.Logger
	messageHub *NodeMessageHub
	viewChange *ViewChanger

	expireTimers      map[string]*time.Timer
	timerLock         sync.RWMutex
	handleMessageLock sync.Mutex
	preprepareStarted bool

	// 全局 timer 管理
	globalTimers     []*time.Timer
	globalTimerLock  sync.RWMutex
	timerAllowed     bool
	timerAllowedLock sync.RWMutex

	StopChan chan struct{}

	Mempool []*core.Transaction

	raftElection        *RaftElection
	mempoolLock         sync.Mutex
	heartbeatTicker     *time.Ticker
	heartbeatTickerLock sync.Mutex
	heartbeatTickerDone chan struct{} // 用于通知 goroutine 停止
}

func NewNode(nodeID int64, cfg *config.Config) *Node {
	prepareMsgNumber := make(map[int64]*atomic.Int32, cfg.SeqNumberUpperBound)
	// for i := cfg.SeqNumberLowerBound; i <= cfg.SeqNumberUpperBound; i++ {
	// 	prepareMsgNumber[int64(i)] = &atomic.Int32{}
	// 	prepareMsgNumber[int64(i)].Store(0)
	// }

	commitMsgNumber := make(map[int64]*atomic.Int32, cfg.SeqNumberUpperBound)
	// for i := cfg.SeqNumberLowerBound; i <= cfg.SeqNumberUpperBound; i++ {
	// 	commitMsgNumber[int64(i)] = &atomic.Int32{}
	// 	commitMsgNumber[int64(i)].Store(0)
	// }

	// initialize checkpoint counters for each possible sequence number
	checkpointList := make(map[int64]*atomic.Int32, cfg.SeqNumberUpperBound)
	for i := cfg.SeqNumberLowerBound; i <= cfg.SeqNumberUpperBound; i++ {
		checkpointList[int64(i)] = &atomic.Int32{}
		checkpointList[int64(i)].Store(0)
	}

	seq2digest := make(map[int64]string, cfg.SeqNumberUpperBound)
	// for i := cfg.SeqNumberLowerBound; i <= cfg.SeqNumberUpperBound; i++ {
	// 	seq2digest[int64(i)] = ""
	// }

	preprepareMsg := make(map[int64][]*core.PreprepareMessage, cfg.SeqNumberUpperBound)
	// for i := cfg.SeqNumberLowerBound; i <= cfg.SeqNumberUpperBound; i++ {
	// 	preprepareMsg[int64(i)] = make([]*core.PreprepareMessage, 0)
	// }

	return &Node{
		NodeID:                  nodeID,
		viewNumber:              0,
		preprepareMsg:           preprepareMsg,
		prepareMsgNumber:        prepareMsgNumber,
		commitMsgNumber:         commitMsgNumber,
		checkpointList:          checkpointList,
		seq2digest:              seq2digest,
		initCommitSeqNumber:     -1,
		lastPreprepareSeqNumber: -1,
		lastPrepareSeqNumber:    -1,
		lastCommitSeqNumber:     -1,
		cfg:                     cfg,
		log:                     logger.NewLogger(nodeID, "node"),
		messageHub:              NewNodeMessageHub(),
		expireTimers:            make(map[string]*time.Timer),
		globalTimers:            make([]*time.Timer, 0),
		timerAllowed:            true,
		viewChange:              NewViewChanger(cfg),
		StopChan:                make(chan struct{}),
		Mempool:                 make([]*core.Transaction, 0),
		preprepareStarted:       false,
		raftElection:            &RaftElection{},
	}
}

func (n *Node) Start() {
	n.messageHub.Start(n, &sync.WaitGroup{})
	n.startHeartbeatTicker(n.viewNumber)
	n.StartGarbageCollection()
	n.log.Info("node started")
}

func (n *Node) Stop() {
	// Stop all expire timers to prevent resource leaks
	n.StopAllExpireTimers()
	// Stop heartbeat ticker
	n.stopHeartbeatTicker()
	// Close network resources to stop listeners and connections
	if n.messageHub != nil {
		n.messageHub.Close()
	}
	n.log.Info("node stopped")
}

func (n *Node) GetAddr() string {
	return config.NodeAddr[int(n.NodeID)]
}

func (n *Node) SetPreprepareSequenceNumber(seqNumber int64, preprepareMessage *core.PreprepareMessage) {
	n.preprepareSeqLock.Lock()
	defer n.preprepareSeqLock.Unlock()
	n.lastPreprepareSeqNumber = seqNumber
	if _, exists := n.preprepareMsg[seqNumber]; !exists {
		n.preprepareMsg[seqNumber] = make([]*core.PreprepareMessage, 0)
	}
	n.preprepareMsg[seqNumber] = append(n.preprepareMsg[seqNumber], preprepareMessage)
	// n.log.Info(fmt.Sprintf("Add preprepare message to map, sequence number is %d, preprepare messages are %v", seqNumber, n.preprepareMsg[seqNumber]))
}

func (n *Node) GetPreprepareSequenceNumber() int64 {
	n.preprepareSeqLock.Lock()
	defer n.preprepareSeqLock.Unlock()
	return n.lastPreprepareSeqNumber
}

// SnapshotPreprepareMessages returns a deep copy snapshot of preprepareMsg under lock.
// This avoids concurrent iteration/write when serializing during view change.
func (n *Node) SnapshotPreprepareMessages() map[int64][]*core.PreprepareMessage {
	n.preprepareSeqLock.Lock()
	defer n.preprepareSeqLock.Unlock()

	snapshot := make(map[int64][]*core.PreprepareMessage, len(n.preprepareMsg))
	for seq, msgs := range n.preprepareMsg {
		// n.log.Info(fmt.Sprintf("Snapshot preprepare messages, sequence number is %d, preprepare messages: %v", seq, msgs))
		if len(msgs) == 0 {
			snapshot[seq] = nil
			continue
		}
		copied := make([]*core.PreprepareMessage, len(msgs))
		copy(copied, msgs)
		snapshot[seq] = copied
	}
	// n.log.Info(fmt.Sprintf("Snapshot preprepare messages %v", snapshot))
	return snapshot
}

func (n *Node) SetPrepareSequenceNumber(seqNumber int64) {
	n.prepareSeqLock.Lock()
	defer n.prepareSeqLock.Unlock()
	n.lastPrepareSeqNumber = seqNumber
}

func (n *Node) GetPrepareSequenceNumber() int64 {
	n.prepareSeqLock.Lock()
	defer n.prepareSeqLock.Unlock()
	return n.lastPrepareSeqNumber
}

func (n *Node) SetCommitSequenceNumber(seqNumber int64) {
	n.commitSeqLock.Lock()
	defer n.commitSeqLock.Unlock()
	n.lastCommitSeqNumber = seqNumber
	if n.initCommitSeqNumber == -1 {
		n.initCommitSeqNumber = seqNumber
	}
}

func (n *Node) GetCommitSequenceNumber() int64 {
	n.commitSeqLock.Lock()
	defer n.commitSeqLock.Unlock()
	return n.lastCommitSeqNumber
}

func (n *Node) GetPrepareMessageNumber(seqNumber int64) int32 {
	n.PrepareMessageLock.Lock()
	defer n.PrepareMessageLock.Unlock()
	counter, exists := n.prepareMsgNumber[seqNumber]
	if !exists || counter == nil {
		return 0
	}
	return counter.Load()
}

func (n *Node) GetCommitMessageNumber(seqNumber int64) int32 {
	n.CommitMessageLock.Lock()
	defer n.CommitMessageLock.Unlock()
	counter, exists := n.commitMsgNumber[seqNumber]
	if !exists || counter == nil {
		return 0
	}
	return counter.Load()
}

func (n *Node) AddPrepareMessageNumber(seqNumber int64) {
	n.PrepareMessageLock.Lock()
	defer n.PrepareMessageLock.Unlock()
	if _, exists := n.prepareMsgNumber[seqNumber]; !exists {
		n.prepareMsgNumber[seqNumber] = &atomic.Int32{}
		n.prepareMsgNumber[seqNumber].Store(0)
	}
	n.prepareMsgNumber[seqNumber].Add(1)
}

func (n *Node) AddCommitMessageNumber(seqNumber int64) {
	n.CommitMessageLock.Lock()
	defer n.CommitMessageLock.Unlock()
	if _, exists := n.commitMsgNumber[seqNumber]; !exists {
		n.commitMsgNumber[seqNumber] = &atomic.Int32{}
		n.commitMsgNumber[seqNumber].Store(0)
	}
	n.commitMsgNumber[seqNumber].Add(1)
}

func (n *Node) AddSeq2Digest(seqNumber int64, digest string) {
	n.seq2digestLock.Lock()
	defer n.seq2digestLock.Unlock()
	if _, exists := n.seq2digest[seqNumber]; !exists {
		n.seq2digest[seqNumber] = ""
	}
	n.seq2digest[seqNumber] = digest
}

func (n *Node) StartTimerForRequest(sequenceNumber int64) {
	timerID := fmt.Sprintf("request_%d_%d", n.NodeID, sequenceNumber)
	n.StartExpireTimer(timerID)
}

func (n *Node) StartRaftTimer(viewNumber int64) {
	timerID := fmt.Sprintf("raft_%d", viewNumber)
	n.StartExpireTimer(timerID)

	// 启动定期发送 heartbeat 的机制（只有 leader 才需要发送）
	n.startHeartbeatTicker(viewNumber)
}

// startHeartbeatTicker 启动定期发送 heartbeat 的 ticker
func (n *Node) startHeartbeatTicker(viewNumber int64) {
	// 停止现有的 ticker（如果存在）
	n.stopHeartbeatTicker()

	// 检查当前节点是否是 leader
	currentLeader := n.viewChange.leaderElection.GetLeader(viewNumber)
	if currentLeader != n.GetAddr() {
		// 不是 leader，不需要发送 heartbeat
		return
	}

	// 设置默认的 raft_interval（如果配置中没有设置，使用 1000ms）
	interval := n.cfg.RaftInterval
	if interval <= 0 {
		interval = 1000 // 默认 1 秒
	}

	// 创建 ticker 和 done channel
	n.heartbeatTickerLock.Lock()
	n.heartbeatTicker = time.NewTicker(time.Duration(interval) * time.Millisecond)
	// 关闭旧的 done channel（如果存在）
	if n.heartbeatTickerDone != nil {
		close(n.heartbeatTickerDone)
	}
	// 创建新的 done channel
	n.heartbeatTickerDone = make(chan struct{})
	done := n.heartbeatTickerDone
	ticker := n.heartbeatTicker
	n.heartbeatTickerLock.Unlock()

	n.log.Info(fmt.Sprintf("Started heartbeat ticker for view %d with interval %d ms", viewNumber, interval))

	// 启动 goroutine 定期发送 heartbeat
	go func() {
		defer func() {
			// 如果 goroutine 因为 panic 退出，确保 ticker 被停止
			if r := recover(); r != nil {
				n.log.Error(fmt.Sprintf("Heartbeat ticker goroutine panicked: %v", r))
				n.stopHeartbeatTicker()
			}
		}()
		
		for {
			select {
			case <-ticker.C:
				// 再次检查是否是 leader
				currentLeader := n.viewChange.leaderElection.GetLeader(viewNumber)
				if currentLeader == n.GetAddr() && !n.viewChange.IsInViewChange() {
					n.SendHeartbeatMessage(viewNumber)
				} else {
					// 不再是 leader 或正在 view change，停止 ticker
					n.stopHeartbeatTicker()
					return
				}
			case <-done:
				// done channel 被关闭，表示需要停止
				return
			case <-n.StopChan:
				// 节点停止，停止 ticker
				n.stopHeartbeatTicker()
				return
			}
		}
	}()
}

// stopHeartbeatTicker 停止 heartbeat ticker
func (n *Node) stopHeartbeatTicker() {
	n.heartbeatTickerLock.Lock()
	defer n.heartbeatTickerLock.Unlock()

	if n.heartbeatTicker != nil {
		n.heartbeatTicker.Stop()
		n.heartbeatTicker = nil
		n.log.Debug("Stopped heartbeat ticker")
	}
	
	// 关闭 done channel 以通知 goroutine 停止
	if n.heartbeatTickerDone != nil {
		close(n.heartbeatTickerDone)
		n.heartbeatTickerDone = nil
	}
}

// StartExpireTimer starts a new expire timer with a unique ID
// Multiple timers can run concurrently
func (n *Node) StartExpireTimer(timerID string) {
	// 检查是否允许创建新的 timer
	if !n.isTimerAllowed() {
		n.log.Debug("Timer creation not allowed, skipping timer '%s'", timerID)
		return
	}

	// Stop existing timer with same ID if it exists
	n.timerLock.Lock()
	if existingTimer, exists := n.expireTimers[timerID]; exists {
		if !existingTimer.Stop() {
			// If timer already expired, drain the channel
			select {
			case <-existingTimer.C:
			default:
			}
		}
		delete(n.expireTimers, timerID)
	}

	// Create new timer
	newTimer := time.NewTimer(time.Duration(n.cfg.ExpireTime) * time.Second)
	n.expireTimers[timerID] = newTimer
	n.timerLock.Unlock()

	// 将新 timer 添加到全局数组中
	n.addToGlobalTimers(newTimer)

	n.log.Debug("expire timer '%s' started with duration: %d seconds", timerID, n.cfg.ExpireTime)

	// Start monitoring goroutine for this specific timer
	go n.monitorTimer(timerID, newTimer)
}

// StopExpireTimer stops a specific timer by ID
func (n *Node) StopExpireTimer(timerID string) {
	n.timerLock.Lock()
	defer n.timerLock.Unlock()

	if timer, exists := n.expireTimers[timerID]; exists {
		if timer.Stop() {
			// n.log.Debug("expire timer '%s' stopped", timerID)
		} else {
			// Timer already expired, drain the channel
			select {
			case <-timer.C:
			default:
			}
			// n.log.Debug("expire timer '%s' was already expired, drained channel", timerID)
		}
		delete(n.expireTimers, timerID)
	}
}

// StopAllExpireTimers stops all running timers
func (n *Node) StopAllExpireTimers() {
	n.timerLock.Lock()
	defer n.timerLock.Unlock()

	for timerID, timer := range n.expireTimers {
		if timer.Stop() {
			n.log.Debug("expire timer '%s' stopped", timerID)
		} else {
			// Timer already expired, drain the channel
			select {
			case <-timer.C:
			default:
			}
			// n.log.Debug("expire timer '%s' was already expired, drained channel", timerID)
		}
	}

	// Clear all timers
	n.expireTimers = make(map[string]*time.Timer)
	n.log.Debug("all expire timers stopped")
}

// isTimerAllowed 检查是否允许创建新的 timer
func (n *Node) isTimerAllowed() bool {
	n.timerAllowedLock.RLock()
	defer n.timerAllowedLock.RUnlock()
	return n.timerAllowed
}

// setTimerAllowed 设置是否允许创建新的 timer
func (n *Node) setTimerAllowed(allowed bool) {
	n.timerAllowedLock.Lock()
	defer n.timerAllowedLock.Unlock()
	n.timerAllowed = allowed
}

// addToGlobalTimers 将 timer 添加到全局数组中
func (n *Node) addToGlobalTimers(timer *time.Timer) {
	n.globalTimerLock.Lock()
	defer n.globalTimerLock.Unlock()
	n.globalTimers = append(n.globalTimers, timer)
}

// clearAllGlobalTimers 清除所有全局 timer
func (n *Node) clearAllGlobalTimers() {
	n.globalTimerLock.Lock()
	defer n.globalTimerLock.Unlock()

	// 停止所有全局 timer
	for _, timer := range n.globalTimers {
		if timer != nil {
			timer.Stop()
		}
	}

	// 清空数组
	n.globalTimers = make([]*time.Timer, 0)
	n.log.Info("All global timers cleared and stopped")
}

// monitorTimer monitors a specific timer and sets expire flag when timeout occurs
func (n *Node) monitorTimer(timerID string, timer *time.Timer) {
	if timer == nil {
		return
	}

	// Wait for timer to expire
	<-timer.C

	// 检查是否是 Raft timer
	isRaftTimer := len(timerID) > 5 && timerID[:5] == "raft_"

	if isRaftTimer {
		n.log.Info("Raft timer '%s' expired! Leader heartbeat timeout, triggering view change", timerID)
		// 停止 heartbeat ticker（如果正在运行）
		n.stopHeartbeatTicker()
	} else {
		n.log.Info("Timer '%s' expired! Setting inViewChange flag to true", timerID)
	}

	// 禁止创建新的 timer
	n.setTimerAllowed(false)
	n.log.Info("Timer creation disabled after timer '%s' expiration", timerID)

	// Stop all other timers when this one expires
	n.StopAllExpireTimers()
	n.log.Info("All timers stopped after timer '%s' expiration", timerID)

	// 清除所有全局 timer
	n.clearAllGlobalTimers()

	// start view changer
	if !n.viewChange.IsInViewChange() {
		if isRaftTimer {
			n.log.Info("Raft timer expired, starting view change due to leader heartbeat timeout!")
			if !n.viewChange.leaderElection.HasLeader(n.viewNumber + 1) {
				n.StartRaftElection(n.viewNumber + 1)
			}
		} else {
			n.log.Info("Start view change!")
		}
		n.viewChange.StartViewChange(n.viewNumber, n.lastStableCheckpoint)
		n.SendViewChangeMessage()
	}
}
