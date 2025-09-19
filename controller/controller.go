package controller

import (
	"github.com/michael112233/pbft/client"
	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/data"
)

func runNode(cfg *config.Config) {
	// Node := node.NewNode(cfg)
	// Node.Start()
}

func runClient(cfg *config.Config) {
	// Init a client
	client := client.NewClient(config.ClientAddr, cfg)

	// Get the transaction details
	txs := data.ReadData()
	client.AddTxs(txs)

	// Start the client
	// client.Start()
	data.PrintTxs(txs, 50)
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
