package main

import (
	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/logger"
)

const (
	cfgPath = "config/run.json"
)

func main() {
	config := config.ReadCfg(cfgPath)
	log := logger.NewLogger(config.NodeID, "others")
	log.Info("config: %v", config)
}
