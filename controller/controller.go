package controller

import (
	"github.com/michael112233/pbft/attacks"
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
	const attacksPath = "config/attacks.json"
	scenario, err := attacks.LoadScenario(attacksPath)
	if err != nil {
		log.Error("Failed to load attacks config: %v", err)
	}
	engine := attacks.NewEngine(nodeID, scenario)
	engine.Start()
	defer engine.Stop()

	Node := node.NewNode(nodeID, cfg)
	defer Node.Stop()

	Node.Start()

	time.Sleep(time.Duration(cfg.RunTime+20) * time.Second)
}

func runClient(cfg *config.Config) {
	defer func() {
		if err := result.ExportToCSV("tps_results.csv"); err != nil {
			log.Error("Failed to export CSV: %v", err)
		}
	}()

	core.NewBlockchain(cfg)
	client := client.NewClient(config.ClientAddr, cfg)
	txs := data.ReadData(cfg.MaxTxNum)
	client.AddTxs(txs)
	client.Start()

	time.Sleep(time.Duration(cfg.RunTime+20) * time.Second)

	client.Stop()
}

func Main(nodeID int64, role, mode, cfgPath string) {
	cfg := config.ReadCfg(cfgPath)

	switch mode {
	case "local":
		config.GenerateLocalNetwork(int(cfg.NodeNum))
	case "remote":
		config.GenerateRemoteNetwork(int(cfg.NodeNum))
	}

	switch role {
	case "node":
		runNode(nodeID, cfg)
	case "client":
		runClient(cfg)
	}
}
