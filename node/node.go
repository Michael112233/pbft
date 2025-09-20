package node

import (
	"sync"
	"sync/atomic"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/logger"
)

type Node struct {
	NodeID      int64
	viewNumber  int64
	prepareMsgs map[int64]*atomic.Int32

	cfg        *config.Config
	log        *logger.Logger
	messageHub *NodeMessageHub

	prepareMu sync.Mutex
}

func NewNode(nodeID int64, cfg *config.Config) *Node {
	prepareMsgs := make(map[int64]*atomic.Int32, 200000)
	for i := 0; i < 200000; i++ {
		prepareMsgs[int64(i)] = &atomic.Int32{}
		prepareMsgs[int64(i)].Store(0)
	}

	return &Node{
		NodeID:      nodeID,
		viewNumber:  0,
		prepareMsgs: prepareMsgs,

		cfg:        cfg,
		log:        logger.NewLogger(nodeID, "node"),
		messageHub: NewNodeMessageHub(),
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
