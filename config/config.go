package config

import (
	"encoding/json"
	"fmt"
	"os"
)

type Config struct {
	DataDir      string `json:"data_dir"`
	MaxTxNum     int64  `json:"max_tx_num"`
	InjectSpeed  int64  `json:"inject_speed"`
	MaxBlockSize int64  `json:"max_block_size"`

	NodeNum int64 `json:"node_num"`
	NodeID  int64 `json:"node_id"`

	FaultyNodesNum int64

	ElectionMethod string `json:"election_method"`
}

func ReadCfg(filename string) *Config {
	jsonData, err := os.ReadFile(filename)
	if err != nil {
		fmt.Printf("error reading json file: %v\n", err)
		os.Exit(1)
	}

	// 创建新的Config实例
	config := &Config{}
	err = json.Unmarshal(jsonData, config)
	if err != nil {
		fmt.Printf("error unmarshaling json: %v\n", err)
		os.Exit(1)
	}

	config.FaultyNodesNum = (config.NodeNum - 1) / 3
	return config
}
