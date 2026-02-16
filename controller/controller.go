package controller

import (
	"bufio"
	"fmt"
	"os"

	"github.com/michael112233/pbft/client"
	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/data"
	"github.com/michael112233/pbft/logger"
	"github.com/michael112233/pbft/node"
)

var log = logger.NewLogger(0, "controller")

func runNode(nodeID int64, cfg *config.Config) {
	Node := node.NewNode(int(nodeID), cfg)

	defer Node.Stop()

	Node.Start()

	// time.Sleep(time.Duration(cfg.RunTime+20) * time.Second)
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println("Node is running. Type 'exit' to stop.")
	for scanner.Scan() {
		input := scanner.Text()
		if input == "exit" {
			fmt.Println("Exiting node...")
			break
		} else {
			Node.PrintDetails()
		}
	}
}

func runClient(cfg *config.Config) {
	// defer func() {
	// 	if err := result.ExportToCSV("tps_results.csv"); err != nil {
	// 		log.Error("Failed to export CSV: %v", err)
	// 	}
	// }()

	// core.NewBlockchain(cfg)
	client := client.NewClient(config.ClientAddr, "client", cfg)
	txs := data.ReadData(cfg.MaxTxNum)
	client.AddTxs(txs)
	client.Start()

	// time.Sleep(time.Duration(cfg.RunTime+20) * time.Second)
	fmt.Println("Node is running. Type 'exit' to stop.")
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		input := scanner.Text()
		if input == "exit" {
			fmt.Println("Exiting node...")
			break
		} else {
			tps, elapsed, txnCommited := client.TransactionManager.GetThroughput()
			fmt.Printf("Current TPS: %f, Elapsed Time: %f, Transactions Committed: %d\n", tps, elapsed, txnCommited)
		}
	}
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
