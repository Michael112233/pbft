package node

import (
	"sync"
	"time"
	"sync/atomic"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/logger"
)

type Node struct {
	NodeID           int64
	viewNumber       int64
	prepareMsgNumber map[int64]*atomic.Int32
	commitMsgNumber  map[int64]*atomic.Int32
	lastPreprepareSeqNumber int64
	lastPrepareSeqNumber int64
	lastCommitSeqNumber int64

	preprepareSeqLock sync.Mutex
	prepareSeqLock sync.Mutex
	commitSeqLock sync.Mutex

	cfg        *config.Config
	log        *logger.Logger
	messageHub *NodeMessageHub

	expireTimer *time.Timer
	expire      bool
	expireLock  sync.RWMutex
}

func NewNode(nodeID int64, cfg *config.Config) *Node {
	prepareMsgNumber := make(map[int64]*atomic.Int32, 200000)
	for i := 0; i < 200000; i++ {
		prepareMsgNumber[int64(i)] = &atomic.Int32{}
		prepareMsgNumber[int64(i)].Store(0)
	}

	commitMsgNumber := make(map[int64]*atomic.Int32, 200000)
	for i := 0; i < 200000; i++ {
		commitMsgNumber[int64(i)] = &atomic.Int32{}
		commitMsgNumber[int64(i)].Store(0)
	}

	return &Node{
		NodeID:           nodeID,
		viewNumber:       0,
		prepareMsgNumber: prepareMsgNumber,
		commitMsgNumber:  commitMsgNumber,
		lastPreprepareSeqNumber: -1,
		lastPrepareSeqNumber: -1,
		lastCommitSeqNumber: -1,
		cfg:              cfg,
		log:              logger.NewLogger(nodeID, "node"),
		messageHub:       NewNodeMessageHub(),
		expireTimer:      nil,
		expire:           false,
	}
}

func (n *Node) Start() {
	n.messageHub.Start(n, &sync.WaitGroup{})
	n.log.Info("node started")
}

func (n *Node) Stop() {
	// Stop the expire timer to prevent resource leaks
	n.StopExpireTimer()
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
}

func (n *Node) GetCommitSequenceNumber() int64 {
	n.commitSeqLock.Lock()
	defer n.commitSeqLock.Unlock()
	return n.lastCommitSeqNumber
}

// StartExpireTimer starts the expire timer with proper checks
// If timer is already running, it stops the old one and starts a new one
func (n *Node) StartExpireTimer() {
	// Reset expire flag
	n.expireLock.Lock()
	n.expire = false
	n.expireLock.Unlock()

	// Stop existing timer if it's running
	if n.expireTimer != nil {
		if !n.expireTimer.Stop() {
			// If timer already expired, drain the channel
			select {
			case <-n.expireTimer.C:
			default:
			}
		}
	}

	// Create new timer
	n.expireTimer = time.NewTimer(time.Duration(n.cfg.ExpireTime) * time.Second)
	n.log.Debug("expire timer started with duration: %d seconds", n.cfg.ExpireTime)

	// Start monitoring goroutine
	go n.monitorTimer()
}

// StopExpireTimer stops the expire timer safely
func (n *Node) StopExpireTimer() {
	if n.expireTimer != nil {
		if n.expireTimer.Stop() {
			n.log.Debug("expire timer stopped")
		} else {
			// Timer already expired, drain the channel
			select {
			case <-n.expireTimer.C:
			default:
			}
			n.log.Debug("expire timer was already expired, drained channel")
		}
	}
}

// IsExpired returns the current expire status
func (n *Node) IsExpired() bool {
	n.expireLock.RLock()
	defer n.expireLock.RUnlock()
	return n.expire
}

// SetExpired sets the expire status (used internally)
func (n *Node) SetExpired(expired bool) {
	n.expireLock.Lock()
	defer n.expireLock.Unlock()
	n.expire = expired
}

// monitorTimer monitors the timer and sets expire flag when timeout occurs
func (n *Node) monitorTimer() {
	if n.expireTimer == nil {
		return
	}

	// Wait for timer to expire
	<-n.expireTimer.C

	// Set expire flag
	n.SetExpired(true)
	n.log.Info("Timer expired! Setting expire flag to true")
}