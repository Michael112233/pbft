package node

import (
	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/logger"
)

type Node struct {
	cfg *config.Config
	log *logger.Logger
}

func NewNode(cfg *config.Config) *Node {
	return &Node{
		cfg: cfg,
		log: logger.NewLogger(cfg.NodeID, "node"),
	}
}

func (n *Node) Start() {
	n.log.Info("node started")
}

func (n *Node) Stop() {
	n.log.Info("node stopped")
}
