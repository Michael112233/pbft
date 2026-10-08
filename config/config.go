package config

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/michael112233/pbft/core"
)

// EpochAggregatorNodeID is the node every other node sends its epoch data to,
// and the only node allowed to touch the shared netem qdisc on lo. Experiment
// scaffolding, not protocol. It lives here rather than in package node so the
// config validation that forbids crashing it cannot drift from the node-side
// constants that act on it (node/epochtimer.go, node/scenario.go).
const EpochAggregatorNodeID = 4

type Config struct {
	MaxTxNum              int64 `json:"max_tx_num"`
	InjectSpeed           int64 `json:"inject_speed"`
	MaxBlockSize          int64 `json:"max_block_size"`
	PendingQueueCapacity  int   `json:"pending_queue_capacity"`
	ClientMsgPaddingBytes int   `json:"client_msg_padding_bytes"`

	NodeNum         int64        `json:"node_num"`
	NodesDead       map[int]bool `json:"nodes_dead"`
	Periodic        bool         `json:"periodic"`
	Period          int64        `json:"period"`
	NumberOfPeriods int          `json:"number_of_periods"`
	PeakTpsTest     bool         `json:"peak_tps_test"`
	LeaderType      string       `json:"leader_type"`
	FarNodeID       int          `json:"far_node_id"`
	FarNodeDelayMs  int64        `json:"far_node_delay_ms"`
	Netem           NetemConfig  `json:"netem"`
	LeaderTypeEnum  core.VCType
	ActiveL         bool `json:"active_l"`
	// ProposalDelayNodes are the nodes that sleep 100 ms in tryPropose while they
	// lead. Same shape as nodes_dead; the ProposalDelay scenario needs exactly f.
	ProposalDelayNodes map[int]bool `json:"proposal_delay_nodes"`
	ProposalDelayMS    int          `json:"proposal_delay_ms"`
	GC                 bool         `json:"gc"`
	Logging            bool         `json:"logging"`
	CarryState         bool         `json:"carry_state"`
	LogShares          bool         `json:"log_shares"`
	RetrySleep         int          `json:"retry_sleep"`
	MaxInflightSeq     int64        `json:"max_inflight_seq"`
	Fixed              bool         `json:"fixed"`
	PeriodicReq        bool         `json:"periodic_req"`
	CompleteSuite      bool         `json:"complete_suite"`
	LatencyLog         bool         `json:"node_latency_logger"`
	MaxBatchSize       int          `json:"max_batch_size"`
	MaxBatchDelay      int          `json:"max_batch_delay"`
	ConsensusChanSize  int          `json:"consensus_chan_size"`
	MinVDFDelay        int          `json:"min_vdf_delay"`
	MaxVDFDelay        int          `json:"max_vdf_delay"`
	ParallelWorkers    bool         `json:"parallel_workers"`
	// PilotExecTrace logs per-slot execution times for the first 2.5 s of each view
	// (node/pilottrace.go). Log only.
	PilotExecTrace bool `json:"pilot_exec_trace"`

	// Per-view throughput measurement and the perf trigger's bar
	// (config/performance.go). Accepts the legacy "performance": true.
	Performance PerformanceConfig `json:"performance"`

	// Trigger timeouts (config/timer.go).
	Timer TimerConfig `json:"timer"`

	// Targeted proposal throttling (config/throttle.go). ProposalMinIntervalMs is
	// the gate severity, calibrated once and shared by every policy;
	// ProposalGateAtStart pins the gate on from startup for calibration runs
	// with no controller.
	ProposalMinIntervalMs int            `json:"proposal_min_interval_ms"`
	ProposalGateAtStart   bool           `json:"proposal_gate_at_start"`
	Throttle              ThrottleConfig `json:"throttle"`

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

	// Client retry (config/clientretry.go). Off: a request a node drops (view change
	// running, not the leader, pending queue reset on a new view) is never resent.
	ClientRetry           bool   `json:"client_retry"`
	ClientRetryMode       string `json:"client_retry_mode"`
	ClientRetryIntervalMs int    `json:"client_retry_interval_ms"`
	ClientRetryMaxMs      int    `json:"client_retry_max_ms"`

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

	// Latency-aware leader policy (Aware). Zero values fall back to the defaults
	// in config/aware.go.
	LatencyProbe         bool    `json:"latency_probe"`
	AwareProbeIntervalMs int     `json:"aware_probe_interval_ms"`
	AwareProbeTimeoutMs  int     `json:"aware_probe_timeout_ms"`
	AwareRTTWindowS      int     `json:"aware_rtt_window_s"`
	AwareGraceMs         int     `json:"aware_grace_ms"`
	AwareStalenessEpochs uint64  `json:"aware_staleness_epochs"`
	AwareAlpha           float64 `json:"aware_alpha"`
	AwareEpsilonMs       float64 `json:"aware_epsilon_ms"`
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

	if err := config.ValidateClientRetry(); err != nil {
		fmt.Printf("Invalid client retry config: %v\n", err)
		os.Exit(1)
	}

	if err := config.ValidateThrottle(); err != nil {
		fmt.Printf("Invalid throttle config: %v\n", err)
		os.Exit(1)
	}

	if err := config.ValidatePerformance(); err != nil {
		fmt.Printf("Invalid performance config: %v\n", err)
		os.Exit(1)
	}

	if err := config.ValidateTimer(); err != nil {
		fmt.Printf("Invalid timer config: %v\n", err)
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
		if scenario == core.ScenarioProposalDelay {
			if err := c.validateScenarioProposalDelayNodes(); err != nil {
				return err
			}
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
// crash: exactly f nodes, none of them node 4, which aggregates epochs (no
// aggregate means no generation switch, so the scenario could never end) and
// owns the netem qdisc.
//
// Exactly f, not 1..f: the scenario's whole point is network delay combined with
// the full crash budget, and f is the only count that makes PeriodicElection's
// edge over RoundRobin (skipping dead nodes instead of burning a timeout on
// them) show up at its designed size. Fewer dead nodes still runs, but it is a
// weaker scenario wearing the same name, which is exactly the kind of silent
// mismatch that makes two runs incomparable. A 4-node nodes_dead set carried
// over to node_num: 7 is caught here instead of quietly measuring the wrong
// thing.
func (c *Config) validateScenarioDeadNodes() error {
	dead, err := c.exactlyFNodes(core.ScenarioNetworkDelayFCrash, "nodes_dead", c.NodesDead)
	if err != nil {
		return err
	}
	for _, id := range dead {
		if id == EpochAggregatorNodeID {
			return fmt.Errorf("NetworkDelayFCrash: node %d cannot be dead, it is the epoch aggregator", EpochAggregatorNodeID)
		}
	}
	return nil
}

// validateScenarioProposalDelayNodes checks the proposal_delay_nodes set the
// ProposalDelay scenario slows down: exactly f nodes, for the same reason as
// nodes_dead. With one slow node at node_num 7 the slow leader comes round once
// per 7 views instead of f times, so the throughput bar has less to catch and
// the scenario no longer means what it does at node_num 4. The aggregator may be
// slow: a slow leader is still live, so epochs and netem are unaffected.
func (c *Config) validateScenarioProposalDelayNodes() error {
	_, err := c.exactlyFNodes(core.ScenarioProposalDelay, "proposal_delay_nodes", c.ProposalDelayNodes)
	return err
}

// exactlyFNodes returns the ids set true in set, sorted, after checking that
// each is in 1..node_num and that there are exactly f = (node_num-1)/3 of them.
// Explicit false entries are ignored.
func (c *Config) exactlyFNodes(scenario core.Scenario, key string, set map[int]bool) ([]int, error) {
	name := core.ScenarioToString(scenario)
	f := int((c.NodeNum - 1) / 3)
	if f == 0 {
		return nil, fmt.Errorf("%s needs node_num >= 4 so f >= 1, got node_num %d", name, c.NodeNum)
	}
	var ids []int
	for id, on := range set {
		if !on {
			continue
		}
		if id < 1 || int64(id) > c.NodeNum {
			return nil, fmt.Errorf("%s: %s has node %d outside 1..%d", name, key, id, c.NodeNum)
		}
		ids = append(ids, id)
	}
	// set is a map, so iteration order is random; sort for a stable message.
	sort.Ints(ids)
	if len(ids) != f {
		return nil, fmt.Errorf("%s needs exactly f = %d nodes set in %s for node_num %d, got %d %v",
			name, f, key, c.NodeNum, len(ids), ids)
	}
	return ids, nil
}
