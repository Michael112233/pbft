package node

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/logger"
)

type Node struct {
	NodeID                  int64
	viewNumber              int64
	prepareMsgNumber        map[int64]*atomic.Int32
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

	cfg        *config.Config
	log        *logger.Logger
	messageHub *NodeMessageHub
	viewChange *ViewChanger

	expireTimers      map[string]*time.Timer
	expireLock        sync.RWMutex
	timerLock         sync.RWMutex
	handleMessageLock sync.Mutex

	StopChan chan struct{}
}

func NewNode(nodeID int64, cfg *config.Config) *Node {
	prepareMsgNumber := make(map[int64]*atomic.Int32, cfg.SeqNumberUpperBound)
	for i := cfg.SeqNumberLowerBound; i <= cfg.SeqNumberUpperBound; i++ {
		prepareMsgNumber[int64(i)] = &atomic.Int32{}
		prepareMsgNumber[int64(i)].Store(0)
	}

	commitMsgNumber := make(map[int64]*atomic.Int32, cfg.SeqNumberUpperBound)
	for i := cfg.SeqNumberLowerBound; i <= cfg.SeqNumberUpperBound; i++ {
		commitMsgNumber[int64(i)] = &atomic.Int32{}
		commitMsgNumber[int64(i)].Store(0)
	}

	seq2digest := make(map[int64]string, cfg.SeqNumberUpperBound)
	for i := cfg.SeqNumberLowerBound; i <= cfg.SeqNumberUpperBound; i++ {
		seq2digest[int64(i)] = ""
	}

	return &Node{
		NodeID:                  nodeID,
		viewNumber:              0,
		prepareMsgNumber:        prepareMsgNumber,
		commitMsgNumber:         commitMsgNumber,
		seq2digest:              seq2digest,
		initCommitSeqNumber:     -1,
		lastPreprepareSeqNumber: -1,
		lastPrepareSeqNumber:    -1,
		lastCommitSeqNumber:     -1,
		cfg:                     cfg,
		log:                     logger.NewLogger(nodeID, "node"),
		messageHub:              NewNodeMessageHub(),
		expireTimers:            make(map[string]*time.Timer),
		viewChange:              NewViewChanger(cfg),
		StopChan:                make(chan struct{}),
	}
}

func (n *Node) Start() {
	n.messageHub.Start(n, &sync.WaitGroup{})
	n.StartGarbageCollection()
	n.log.Info("node started")
}

func (n *Node) Stop() {
	// Stop all expire timers to prevent resource leaks
	n.StopAllExpireTimers()
	// Close network resources to stop listeners and connections
	if n.messageHub != nil {
		n.messageHub.Close()
	}
	n.log.Info("node stopped")
}

func (n *Node) GetAddr() string {
	return config.NodeAddr[int(n.NodeID)]
}

func (n *Node) SetPreprepareSequenceNumber(seqNumber int64) {
	n.preprepareSeqLock.Lock()
	defer n.preprepareSeqLock.Unlock()
	n.lastPreprepareSeqNumber = seqNumber
}

func (n *Node) GetPreprepareSequenceNumber() int64 {
	n.preprepareSeqLock.Lock()
	defer n.preprepareSeqLock.Unlock()
	return n.lastPreprepareSeqNumber
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
	return n.prepareMsgNumber[seqNumber].Load()
}

func (n *Node) GetCommitMessageNumber(seqNumber int64) int32 {
	n.CommitMessageLock.Lock()
	defer n.CommitMessageLock.Unlock()
	return n.commitMsgNumber[seqNumber].Load()
}

func (n *Node) AddPrepareMessageNumber(seqNumber int64) {
	n.PrepareMessageLock.Lock()
	defer n.PrepareMessageLock.Unlock()
	n.prepareMsgNumber[seqNumber].Add(1)
}

func (n *Node) AddCommitMessageNumber(seqNumber int64) {
	n.CommitMessageLock.Lock()
	defer n.CommitMessageLock.Unlock()
	n.commitMsgNumber[seqNumber].Add(1)
}

// StartExpireTimer starts a new expire timer with a unique ID
// Multiple timers can run concurrently
func (n *Node) StartExpireTimer(timerID string) {
	// Reset expire flag when starting new timer
	n.expireLock.Lock()
	n.viewChange.ResetViewChanger()
	n.expireLock.Unlock()

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
			n.log.Debug("expire timer '%s' stopped", timerID)
		} else {
			// Timer already expired, drain the channel
			select {
			case <-timer.C:
			default:
			}
			n.log.Debug("expire timer '%s' was already expired, drained channel", timerID)
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
			n.log.Debug("expire timer '%s' was already expired, drained channel", timerID)
		}
	}

	// Clear all timers
	n.expireTimers = make(map[string]*time.Timer)
	n.log.Debug("all expire timers stopped")
}

// monitorTimer monitors a specific timer and sets expire flag when timeout occurs
func (n *Node) monitorTimer(timerID string, timer *time.Timer) {
	if timer == nil {
		return
	}

	// Wait for timer to expire
	<-timer.C
	n.log.Info("Timer '%s' expired! Setting inViewChange flag to true", timerID)

	// Stop all other timers when this one expires
	n.StopAllExpireTimers()
	n.log.Info("All timers stopped after timer '%s' expiration", timerID)

	// start view changer
	if !n.viewChange.IsInViewChange() {
		n.viewChange.StartViewChange(n.viewNumber, n.lastStableCheckpoint)
		// n.SendViewChangeMessage()
	}
}
