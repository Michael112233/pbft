package controller

import (
	"time"

	"github.com/michael112233/pbft/client"
	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/data"
	"github.com/michael112233/pbft/logger"
	"github.com/michael112233/pbft/node"
	"github.com/michael112233/pbft/result"
)

var log = logger.NewLogger(0, "controller")

func runNode(nodeID int64, cfg *config.Config) {
	Node := node.NewNode(nodeID, cfg)
	Node.Start()

	// Keep the node process alive until a stop signal is received
	for {
		select {
		case <-Node.StopChan:
			time.Sleep(20 * time.Second)
			Node.Stop()
			return
		default:
			time.Sleep(1 * time.Second)
		}
	}
}

func runClient(cfg *config.Config) {
	defer result.PrintResult()

	// Init a blockchain (no FinishInjecting usage)
	core.NewBlockchain(cfg)

	// Init a client
	client := client.NewClient(config.ClientAddr, cfg)

	// Get the transaction details
	txs := data.ReadData(cfg.MaxTxNum)
	client.AddTxs(txs)
	client.Start()

	// Wait for client's injection goroutine(s) to finish
	// client.Stop() waits for WaitGroup and then returns; message hub remains available to send close messages
	client.Stop()

	// Broadcast close to all nodes after injection completes
	client.BroadcastClose()
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
