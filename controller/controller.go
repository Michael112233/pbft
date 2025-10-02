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
	defer Node.Stop()

	Node.Start()

	// Keep the node process alive until a stop signal is received
	time.Sleep(45 * time.Second)
}

func runClient(cfg *config.Config) {
	defer func() {
		// result.PrintResult()
		// Export results to CSV
		if err := result.ExportToCSV("tps_results.csv"); err != nil {
			log.Error("Failed to export CSV: %v", err)
		}
	}()

	// Init a blockchain (no FinishInjecting usage)
	core.NewBlockchain(cfg)

	// Init a client
	client := client.NewClient(config.ClientAddr, cfg)

	// Get the transaction details
	txs := data.ReadData(cfg.MaxTxNum)
	client.AddTxs(txs)
	client.Start()

	// Wait for 60 seconds to allow transaction processing
	time.Sleep(20 * time.Second)

	// client.Stop() waits for WaitGroup and then returns; message hub remains available to send close messages
	client.Stop()

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
