package controller

import (
	"time"

	"github.com/michael112233/pbft/client"
	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/data"
	"github.com/michael112233/pbft/node"
	"github.com/michael112233/pbft/result"
)

var Blockchain *core.Blockchain

func runNode(nodeID int64, cfg *config.Config) {
	Node := node.NewNode(nodeID, cfg)
	defer Node.Stop()

	Node.Start()

	time.Sleep(30 * time.Second)
}

func runClient(cfg *config.Config) {
	defer result.PrintResult()
	// Init a blockchain
	core.NewBlockchain()

	// Init a client
	client := client.NewClient(config.ClientAddr, cfg)
	defer client.Stop()

	// Get the transaction details
	txs := data.ReadData(cfg.MaxTxNum)
	client.AddTxs(txs)
	client.Start()
}

func Main(nodeID int64, role, mode, cfgPath string) {
	cfg := config.ReadCfg(cfgPath)

	// mode -> network structure
	switch mode {
	case "local":
		config.GenerateLocalNetwork(int(cfg.NodeNum))
	case "remote":
		config.GenerateRemoteNetwork(int(cfg.NodeNum))
	}

	// if mode == "local", then all nodes are running on the same machin
	// role -> system role
	switch role {
	case "node":
		runNode(nodeID, cfg)
	case "client":
		runClient(cfg)
	}
}
