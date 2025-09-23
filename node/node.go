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

	timer_list []*time.Timer
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
		timer_list:       make([]*time.Timer, 0),
	}
}

func (n *Node) Start() {
	n.messageHub.Start(n, &sync.WaitGroup{})
	n.log.Info("node started")
}

func (n *Node) Stop() {
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

func (n *Node) SetPrepareSequenceNumber(seqNumber int64) {
	n.prepareSeqLock.Lock()
	defer n.prepareSeqLock.Unlock()
	n.lastPrepareSeqNumber = seqNumber
}

func (n *Node) SetCommitSequenceNumber(seqNumber int64) {
	n.commitSeqLock.Lock()
	defer n.commitSeqLock.Unlock()
	n.lastCommitSeqNumber = seqNumber
}