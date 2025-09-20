package node

import (
	"sync"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/logger"
)

type Node struct {
	NodeID int64

	cfg        *config.Config
	log        *logger.Logger
	messageHub *NodeMessageHub
}

func NewNode(cfg *config.Config) *Node {
	return &Node{
		NodeID: cfg.NodeID,

		cfg:        cfg,
		log:        logger.NewLogger(cfg.NodeID, "node"),
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
