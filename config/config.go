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

	// ScenarioMode cycles through Scenarios, moving to the next one every
	// ScenarioGenerations generations. Needs EpochMode, since only epochs move
	// the generation.
	ScenarioMode        bool     `json:"scenario_mode"`
	Scenarios           []string `json:"scenarios"`
	ScenarioGenerations uint64   `json:"scenario_generations"`
	ScenariosEnum       []core.Scenario
}

const DefaultScenarioGenerations = 100

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

	if err := config.ParseScenarios(); err != nil {
		fmt.Printf("Invalid scenario config: %v\n", err)
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

// ParseScenarios fills ScenariosEnum and rejects scenario configs that could
// never take effect or would clash with other netem users.
func (c *Config) ParseScenarios() error {
	if !c.ScenarioMode {
		return nil
	}
	if len(c.Scenarios) == 0 {
		return fmt.Errorf("scenarios must be non-empty when scenario_mode is enabled")
	}
	if !c.EpochMode {
		return fmt.Errorf("scenario_mode needs epoch_mode, otherwise the generation never changes")
	}
	if c.Netem.Enabled {
		return fmt.Errorf("scenario_mode and netem.enabled both manage the qdisc on %s", c.Netem.Interface)
	}
	if c.ScenarioGenerations == 0 {
		c.ScenarioGenerations = DefaultScenarioGenerations
	}
	c.ScenariosEnum = make([]core.Scenario, len(c.Scenarios))
	for i, name := range c.Scenarios {
		scenario, ok := core.StringToScenario(name)
		if !ok {
			return fmt.Errorf("unknown scenario %q", name)
		}
		if scenario == core.ScenarioProposalDelay && (c.ProposalDelayNode < 1 || int64(c.ProposalDelayNode) > c.NodeNum) {
			return fmt.Errorf("ProposalDelay needs proposal_delay_node in 1..%d, got %d", c.NodeNum, c.ProposalDelayNode)
		}
		if scenario == core.ScenarioNetworkDelayFCrash {
			if err := c.validateScenarioDeadNodes(); err != nil {
				return err
			}
		}
		c.ScenariosEnum[i] = scenario
	}
	return nil
}

// validateScenarioDeadNodes checks the nodes_dead set NetworkDelayFCrash will
// crash: 1..f nodes, none of them node 4, which aggregates epochs (no
// aggregate means no generation switch, so the scenario could never end) and
// owns the netem qdisc.
func (c *Config) validateScenarioDeadNodes() error {
	f := int((c.NodeNum - 1) / 3)
	var dead []int
	for id, isDead := range c.NodesDead {
		if !isDead {
			continue
		}
		if id < 1 || int64(id) > c.NodeNum {
			return fmt.Errorf("NetworkDelayFCrash: nodes_dead has node %d outside 1..%d", id, c.NodeNum)
		}
		if id == 4 {
			return fmt.Errorf("NetworkDelayFCrash: node 4 cannot be dead, it is the epoch aggregator")
		}
		dead = append(dead, id)
	}
	if len(dead) == 0 || len(dead) > f {
		return fmt.Errorf("NetworkDelayFCrash needs 1..%d nodes set in nodes_dead, got %v", f, dead)
	}
	return nil
}
