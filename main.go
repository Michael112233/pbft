package main

import (
	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/logger"
)

func main() {
	config.ReadCfg("config.json")
	log := logger.NewLogger(config.NodeID, "others")
	log.Info("config: %v", config.config)
}
