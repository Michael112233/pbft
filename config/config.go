package config

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/michael112233/pbft/core"
)

type Config struct {
	MaxTxNum              int64 `json:"max_tx_num"`
	InjectSpeed           int64 `json:"inject_speed"`
	MaxBlockSize          int64 `json:"max_block_size"`
	PendingQueueCapacity  int   `json:"pending_queue_capacity"`
	ClientMsgPaddingBytes int   `json:"client_msg_padding_bytes"`

	NodeNum                 int64        `json:"node_num"`
	NodesDead               map[int]bool `json:"nodes_dead"`
	Periodic                bool         `json:"periodic"`
	Period                  int64        `json:"period"`
	NumberOfPeriods         int          `json:"number_of_periods"`
	PeakTpsTest             bool         `json:"peak_tps_test"`
	LeaderType              string       `json:"leader_type"`
	FarNodeID               int          `json:"far_node_id"`
	FarNodeDelayMs          int64        `json:"far_node_delay_ms"`
	Netem                   NetemConfig  `json:"netem"`
	LeaderTypeEnum          core.VCType
	ActiveL                 bool  `json:"active_l"`
	PerformanceTrigger      bool  `json:"performance_trigger"`
	Performance             bool  `json:"performance"`
	ProposalDelayNode       int   `json:"proposal_delay_node"`
	ProposalDelayMS         int   `json:"proposal_delay_ms"`
	GC                      bool  `json:"gc"`
	Logging                 bool  `json:"logging"`
	CarryState              bool  `json:"carry_state"`
	LogShares               bool  `json:"log_shares"`
	RetrySleep              int   `json:"retry_sleep"`
	MaxInflightSeq          int64 `json:"max_inflight_seq"`
	Fixed                   bool  `json:"fixed"`
	PeriodicReq             bool  `json:"periodic_req"`
	CompleteSuite           bool  `json:"complete_suite"`
	LatencyLog              bool  `json:"node_latency_logger"`
	SerialClient            bool  `json:"serial_client"`
	MaxBatchSize            int   `json:"max_batch_size"`
	MaxBatchDelay           int   `json:"max_batch_delay"`
	ConsensusChanSize       int   `json:"consensus_chan_size"`
	MinVDFDelay             int   `json:"min_vdf_delay"`
	MaxVDFDelay             int   `json:"max_vdf_delay"`
	ParallelWorkers         bool  `json:"parallel_workers"`
	PerformanceTimedTrigger bool  `json:"performance_timed_trigger"`

	// OracleMode replaces the learning-agent RPC with a local goroutine that
	// sleeps to mimic model latency and then feeds a decision straight into
	// the node's learning-decision channel, so tests can run without a real
	// learning-agent process.
	OracleMode        bool     `json:"oracle_mode"`
	OracleActions     []string `json:"oracle_actions"`
	OracleRandom      bool     `json:"oracle_random"`
	OracleSeed        int64    `json:"oracle_seed"`
	OracleActionsEnum []core.Action

	EpochMode bool `json:"epoch_mode"`

	// DefaultAction names the action (trigger mode + leader policy) every node
	// starts in, e.g. "FixedRoundRobin". Empty keeps the legacy PerformanceRoundRobin.
	DefaultAction string `json:"default_action"`
}

// InitialAction returns the action nodes start in.
func (c *Config) InitialAction() core.Action {
	if c.DefaultAction == "" {
		return core.FixedRoundRobin
	}
	return core.StringtoAction(c.DefaultAction)
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
	config.ApplyNetemDefaults()
	if err := config.ValidateNetem(); err != nil {
		fmt.Printf("Invalid netem config: %v\n", err)
		os.Exit(1)
	}
	if config.LeaderType == "roundrobin" {
		config.LeaderTypeEnum = core.VCTypeRoundRobin
	} else if config.LeaderType == "election" {
		config.LeaderTypeEnum = core.VCTypeElection
	} else if config.LeaderType == "wrr" {
		config.LeaderTypeEnum = core.VCTypeWRR
	} else {
		fmt.Printf("Invalid leader type in config: %s\n", config.LeaderType)
		os.Exit(1)
	}

	if config.DefaultAction != "" && core.ActiontoString(core.StringtoAction(config.DefaultAction)) != config.DefaultAction {
		fmt.Printf("Invalid default_action in config: %s\n", config.DefaultAction)
		os.Exit(1)
	}

	if config.OracleMode {
		config.OracleActionsEnum = make([]core.Action, len(config.OracleActions))
		for i, actionStr := range config.OracleActions {
			config.OracleActionsEnum[i] = core.StringtoAction(actionStr)
		}
		if len(config.OracleActionsEnum) == 0 {
			fmt.Printf("Invalid oracle config: oracle_actions must be non-empty when oracle_mode is enabled\n")
			os.Exit(1)
		}
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
