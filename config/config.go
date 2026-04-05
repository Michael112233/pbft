package config

import (
	"encoding/json"
	"fmt"
	"os"
)

type Config struct {
	MaxTxNum     int64 `json:"max_tx_num"`
	InjectSpeed  int64 `json:"inject_speed"`
	MaxBlockSize int64 `json:"max_block_size"`

	NodeNum     int64        `json:"node_num"`
	NodesDead   map[int]bool `json:"nodes_alive"`
	Periodic    bool         `json:"periodic"`
	PeakTpsTest bool         `json:"peak_tps_test"`
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

	// config.FaultyNodesNum = (config.NodeNum - 1) / 3

	// // 设置TCP缓冲区默认值（256KB = 256 * 1024 bytes）
	// if config.TCPReadBufferSize == 0 {
	// 	config.TCPReadBufferSize = 256 * 1024
	// }
	// if config.TCPWriteBufferSize == 0 {
	// 	config.TCPWriteBufferSize = 256 * 1024
	// }

	return config
}
