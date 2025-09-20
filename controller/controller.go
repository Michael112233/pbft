package controller

import (
	"github.com/michael112233/pbft/client"
	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/data"
	"github.com/michael112233/pbft/node"
)

func runNode(cfg *config.Config) {
	Node := node.NewNode(cfg)

	Node.Start()

	Node.Stop()
}

func runClient(cfg *config.Config) {
	// Init a client
	client := client.NewClient(config.ClientAddr, cfg)

	// Get the transaction details
	txs := data.ReadData(cfg.MaxTxNum)
	client.AddTxs(txs)

	client.Start()
	client.InjectTxs()
	client.Stop()
}

func Main(role, mode, cfgPath string) {
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
		runNode(cfg)
	case "client":
		runClient(cfg)
	}
}
