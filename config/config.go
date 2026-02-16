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
	RunTime int   `json:"run_time"`

	FaultyNodesNum int64

	ElectionMethod string `json:"election_method"`

	ExpireTime          int64 `json:"expire_time"`
	SeqNumberUpperBound int64 `json:"seq_number_upper_bound"`
	SeqNumberLowerBound int64 `json:"seq_number_lower_bound"`
	CheckpointInterval  int64 `json:"checkpoint_interval"`

	// TCP buffer sizes (in bytes)
	TCPReadBufferSize  int `json:"tcp_read_buffer_size"`  // 接收缓冲区大小，默认256KB
	TCPWriteBufferSize int `json:"tcp_write_buffer_size"` // 发送缓冲区大小，默认256KB

	RaftTimeout  int64 `json:"raft_timeout"`
	RaftInterval int64 `json:"raft_interval"` // Heartbeat interval in milliseconds
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

	// 设置TCP缓冲区默认值（256KB = 256 * 1024 bytes）
	if config.TCPReadBufferSize == 0 {
		config.TCPReadBufferSize = 256 * 1024
	}
	if config.TCPWriteBufferSize == 0 {
		config.TCPWriteBufferSize = 256 * 1024
	}

	return config
}
