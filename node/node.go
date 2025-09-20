package node

import (
	"sync"
	"sync/atomic"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/logger"
)

type Node struct {
	NodeID           int64
	viewNumber       int64
	prepareMsgNumber map[int64]*atomic.Int32
	commitMsgNumber  map[int64]*atomic.Int32

	cfg        *config.Config
	log        *logger.Logger
	messageHub *NodeMessageHub
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
		cfg:              cfg,
		log:              logger.NewLogger(nodeID, "node"),
		messageHub:       NewNodeMessageHub(),
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
