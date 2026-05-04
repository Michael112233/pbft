package controller

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/michael112233/pbft/client"
	"github.com/michael112233/pbft/config"

	"github.com/michael112233/pbft/logger"
	"github.com/michael112233/pbft/node"
)

var log = logger.NewLogger(0, "controller")

func runNode(nodeID int64, cfg *config.Config) {
	Node := node.NewNode(int(nodeID), cfg)
	Node.PrintDetails()

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
		} else if input[0] == 's' {
			// Extract number after 's'
			parts := strings.Fields(input) // splits "s 1245" into ["s", "1245"]
			if len(parts) >= 2 {
				if num, err := strconv.ParseInt(parts[1], 10, 64); err == nil {
					Node.PrintSlot(num)
				} else {
					fmt.Println("Invalid number:", parts[1])
				}
			}
		} else if input[0] == 'i' {

			Node.PrintDetails() // no number provided, use default

		} else if input[0] == 'd' {
			Node.Dead()
		} else if input[0] == 'e' {
			Node.PrintExecutedSlots()
		} else if input[0] == 'c' {
			Node.PrintCommitSentSummary()
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
	leaderAddr := config.NodeAddr[1]
	client := client.NewClient(config.ClientAddr, "client", cfg, leaderAddr)
	// txs := data.ReadData(cfg.MaxTxNum)
	// client.AddTxs(txs)
	client.Start()

	// time.Sleep(time.Duration(cfg.RunTime+20) * time.Second)
	fmt.Println("Node is running. Type 'exit' to stop.")
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		input := scanner.Text()
		if input == "exit" {
			fmt.Println("Exiting node...")
			break
		} else if strings.HasPrefix(input, "export") {
			path := "tps_series.json"
			parts := strings.Fields(input)
			if len(parts) >= 2 {
				path = parts[1]
			}
			if err := client.ExportTPSSeries(path); err != nil {
				fmt.Printf("Failed to export TPS series: %v\n", err)
			} else {
				fmt.Printf("Exported TPS series to %s\n", path)
			}
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
