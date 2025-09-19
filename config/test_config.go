package config

import (
	"github.com/michael112233/pbft/logger"
)

const (
	cfgPath = "config/run.json"
)

func TestConfig() {
	config := ReadCfg(cfgPath)
	log := logger.NewLogger(config.NodeID, "test")
	log.Test("config: %v", config)
}
