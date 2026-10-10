package config

import (
	"encoding/json"
	"testing"
	"time"
)

// NetworkDelayFCrash must crash exactly f nodes: the scenario is network delay
// combined with the full crash budget, so a count below f is a weaker scenario
// under the same name. The case that motivated the check is a nodes_dead set
// written for node_num 4 (one dead) carried over to node_num 7, where f is 2.
func TestValidateScenarioDeadNodes(t *testing.T) {
	dead := func(ids ...int) map[int]bool {
		m := make(map[int]bool, len(ids))
		for _, id := range ids {
			m[id] = true
		}
		return m
	}

	tests := []struct {
		name    string
		nodeNum int64
		dead    map[int]bool
		wantErr bool
	}{
		{"n=4 f=1 one dead", 4, dead(2), false},
		{"n=4 f=1 none dead", 4, nil, true},
		{"n=4 f=1 two dead exceeds f", 4, dead(2, 3), true},

		{"n=7 f=2 two dead", 7, dead(2, 3), false},
		{"n=7 f=2 two dead non-adjacent", 7, dead(1, 7), false},
		// The stale-config case: fine at node_num 4, must fail at 7.
		{"n=7 f=2 one dead is below f", 7, dead(2), true},
		{"n=7 f=2 three dead exceeds f", 7, dead(1, 2, 3), true},
		{"n=7 f=2 none dead", 7, nil, true},

		// explicit false entries are not dead nodes, so this is still zero
		{"n=7 all entries false", 7, map[int]bool{1: false, 2: false, 3: false}, true},

		{"aggregator cannot be dead", 7, dead(EpochAggregatorNodeID, 2), true},
		{"dead node above node_num", 7, dead(2, 8), true},
		{"dead node below 1", 7, dead(0, 2), true},

		// f = 0 below four nodes: the scenario cannot mean anything there.
		{"n=3 f=0", 3, dead(2), true},
		{"n=1 f=0", 1, nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Config{NodeNum: tt.nodeNum, NodesDead: tt.dead}
			err := c.validateScenarioDeadNodes()
			if tt.wantErr && err == nil {
				t.Fatalf("node_num %d dead %v: want error, got nil", tt.nodeNum, tt.dead)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("node_num %d dead %v: want nil, got %v", tt.nodeNum, tt.dead, err)
			}
		})
	}
}

// The error names the count it wanted and the set it got, so a stale config is
// diagnosable from the message alone. NodesDead is a map, so the ids must come
// out sorted rather than in random iteration order.
func TestValidateScenarioDeadNodesErrorIsStable(t *testing.T) {
	c := &Config{NodeNum: 7, NodesDead: map[int]bool{7: true, 1: true, 5: true}}
	want := "NetworkDelayFCrash needs exactly f = 2 nodes set in nodes_dead for node_num 7, got 3 [1 5 7]"
	for i := 0; i < 50; i++ {
		err := c.validateScenarioDeadNodes()
		if err == nil {
			t.Fatal("want error, got nil")
		}
		if err.Error() != want {
			t.Fatalf("got %q, want %q", err.Error(), want)
		}
	}
}

// ProposalDelay must slow exactly f nodes, like NetworkDelayFCrash crashes
// exactly f. Unlike nodes_dead, the aggregator is allowed: a slow leader is live.
func TestValidateScenarioProposalDelayNodes(t *testing.T) {
	set := func(ids ...int) map[int]bool {
		m := make(map[int]bool, len(ids))
		for _, id := range ids {
			m[id] = true
		}
		return m
	}

	tests := []struct {
		name    string
		nodeNum int64
		delay   map[int]bool
		wantErr bool
	}{
		{"n=4 f=1 one slow", 4, set(1), false},
		{"n=4 f=1 aggregator slow is fine", 4, set(EpochAggregatorNodeID), false},
		{"n=4 f=1 none slow", 4, nil, true},
		{"n=4 f=1 two slow exceeds f", 4, set(1, 2), true},

		{"n=7 f=2 two slow", 7, set(1, 5), false},
		{"n=7 f=2 one slow is below f", 7, set(1), true},
		{"n=7 f=2 three slow exceeds f", 7, set(1, 2, 3), true},

		{"n=10 f=3 three slow", 10, set(1, 4, 7), false},
		{"n=10 f=3 two slow is below f", 10, set(1, 4), true},
		{"n=13 f=4 four slow", 13, set(1, 2, 3, 13), false},

		{"explicit false entries are not slow", 7, map[int]bool{1: true, 2: false, 3: false}, true},
		{"slow node above node_num", 7, set(2, 8), true},
		{"slow node below 1", 7, set(0, 2), true},
		{"n=3 f=0", 3, set(1), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Config{NodeNum: tt.nodeNum, ProposalDelayNodes: tt.delay}
			err := c.validateScenarioProposalDelayNodes()
			if tt.wantErr && err == nil {
				t.Fatalf("node_num %d delay %v: want error, got nil", tt.nodeNum, tt.delay)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("node_num %d delay %v: want nil, got %v", tt.nodeNum, tt.delay, err)
			}
		})
	}
}

// ParseScenarios only checks a node set when its scenario is in the list, so a
// run without ProposalDelay does not need proposal_delay_nodes at all.
func TestParseScenariosChecksOnlyListedScenarios(t *testing.T) {
	base := func(scenarios ...string) *Config {
		return &Config{NodeNum: 7, EpochMode: true, ScenarioMode: true, Scenarios: scenarios}
	}
	if err := base("Healthy", "NetworkDelay").ParseScenarios(); err != nil {
		t.Fatalf("no ProposalDelay/FCrash in list: %v", err)
	}
	if err := base("Healthy", "ProposalDelay").ParseScenarios(); err == nil {
		t.Fatal("ProposalDelay with no proposal_delay_nodes: want error")
	}
	c := base("Healthy", "ProposalDelay", "NetworkDelayFCrash")
	c.ProposalDelayNodes = map[int]bool{1: true, 5: true}
	c.NodesDead = map[int]bool{2: true, 3: true}
	if err := c.ParseScenarios(); err != nil {
		t.Fatalf("n=7 f=2 sets: %v", err)
	}
}

func TestTimerDefaultsAndOverrides(t *testing.T) {
	c := &Config{}
	if c.PeriodicTriggerTimeout() != 10*time.Second || c.FixedTriggerTimeout() != 150*time.Millisecond ||
		c.RelaxedFixedTriggerTimeout() != 350*time.Millisecond || c.EpochTimer() != 45*time.Second {
		t.Fatalf("defaults: periodic %v fixed %v relaxed %v",
			c.PeriodicTriggerTimeout(), c.FixedTriggerTimeout(), c.RelaxedFixedTriggerTimeout())
	}
	if err := c.ValidateTimer(); err != nil {
		t.Fatalf("defaults invalid: %v", err)
	}

	if err := json.Unmarshal([]byte(`{"timer": {"periodic_trigger_timeout_ms": 5000, "fixed_trigger_timeout_ms": 200, "relaxed_fixed_trigger_timeout_ms": 400, "epoch_timer_ms": 75000}}`), c); err != nil {
		t.Fatal(err)
	}
	if c.PeriodicTriggerTimeout() != 5*time.Second || c.FixedTriggerTimeout() != 200*time.Millisecond ||
		c.RelaxedFixedTriggerTimeout() != 400*time.Millisecond || c.EpochTimer() != 75*time.Second {
		t.Fatalf("overrides: periodic %v fixed %v relaxed %v",
			c.PeriodicTriggerTimeout(), c.FixedTriggerTimeout(), c.RelaxedFixedTriggerTimeout())
	}

	for _, bad := range []TimerConfig{
		{PeriodicTriggerTimeoutMs: -1},
		{FixedTriggerTimeoutMs: -1},
		{RelaxedFixedTriggerTimeoutMs: -1},
		{EpochTimerMs: -1},
	} {
		if err := (&Config{Timer: bad}).ValidateTimer(); err == nil {
			t.Fatalf("%+v: want error", bad)
		}
	}
}

// The epoch grid is off unless asked for, and the oracle delay defaults to the
// 100 ms it used to be hardcoded at.
func TestEpochGridAndOracleDelay(t *testing.T) {
	c := &Config{}
	if c.Timer.EpochGrid || c.OracleDecisionDelay() != 100*time.Millisecond {
		t.Fatalf("defaults: grid %t oracle delay %v", c.Timer.EpochGrid, c.OracleDecisionDelay())
	}
	if err := json.Unmarshal([]byte(`{"timer": {"epoch_grid": true}, "oracle_decision_delay_ms": 1050}`), c); err != nil {
		t.Fatal(err)
	}
	if !c.Timer.EpochGrid || c.OracleDecisionDelay() != 1050*time.Millisecond {
		t.Fatalf("overrides: grid %t oracle delay %v", c.Timer.EpochGrid, c.OracleDecisionDelay())
	}
	if err := (&Config{OracleDecisionDelayMs: -1}).ValidateTimer(); err == nil {
		t.Fatal("negative oracle_decision_delay_ms: want error")
	}
}

// "performance" accepts the legacy boolean as well as the object form.
func TestPerformanceConfigForms(t *testing.T) {
	tests := []struct {
		json    string
		enabled bool
		want    PerformanceConfig
	}{
		{`{"performance": true}`, true, PerformanceConfig{Enabled: true}},
		{`{"performance": false}`, false, PerformanceConfig{}},
		{`{}`, false, PerformanceConfig{}},
		{`{"performance": {"enabled": true, "view_strategy": "mean", "window_delay_slots": 5, "interval_slots": 125, "grace_ms": 500, "bar_factor": 0.9, "default_max_throughput": 150}}`, true,
			PerformanceConfig{Enabled: true, ViewStrategy: "mean", WindowDelaySlots: 5, IntervalSlots: 125, GraceMs: 500, BarFactor: 0.9, DefaultMaxThroughput: 150}},
	}
	for _, tt := range tests {
		var c Config
		if err := json.Unmarshal([]byte(tt.json), &c); err != nil {
			t.Fatalf("%s: %v", tt.json, err)
		}
		if c.Performance != tt.want {
			t.Fatalf("%s: got %+v, want %+v", tt.json, c.Performance, tt.want)
		}
	}

	var c Config
	if err := json.Unmarshal([]byte(`{"performance": "yes"}`), &c); err == nil {
		t.Fatal(`"performance": "yes" parsed without error`)
	}
}

func TestPerformanceDefaultsAndValidation(t *testing.T) {
	c := &Config{Performance: PerformanceConfig{Enabled: true}}
	if c.PerfIntervalSlots() != 250 || c.PerfGrace() != time.Second || c.PerfViewStrategy() != PerfViewStrategyMax ||
		c.PerfBarFactor() != 0.91 || c.PerfDefaultMaxThroughput() != 160 {
		t.Fatalf("defaults: interval %d grace %v strategy %q factor %g default %g",
			c.PerfIntervalSlots(), c.PerfGrace(), c.PerfViewStrategy(), c.PerfBarFactor(), c.PerfDefaultMaxThroughput())
	}
	if c.PerfWindowDelaySlots() != 3 {
		t.Fatalf("default window delay = %d, want 3", c.PerfWindowDelaySlots())
	}
	if neg := (&Config{Performance: PerformanceConfig{WindowDelaySlots: -1}}); neg.PerfWindowDelaySlots() != 0 {
		t.Fatalf("negative window delay = %d, want 0", neg.PerfWindowDelaySlots())
	}
	if got, want := c.PerfDefaultBar(), 0.91*160; got != want {
		t.Fatalf("default bar = %g, want %g", got, want)
	}
	if err := c.ValidatePerformance(); err != nil {
		t.Fatalf("defaults invalid: %v", err)
	}
	for _, bad := range []PerformanceConfig{
		{ViewStrategy: "median"},
		{IntervalSlots: -1},
		{BarFactor: 1.5},
		{DefaultMaxThroughput: -1},
	} {
		c := &Config{Performance: bad}
		if err := c.ValidatePerformance(); err == nil {
			t.Fatalf("%+v: want error", bad)
		}
	}
}

// In scenario mode the Throttle scenario owns the throttle: it needs a gate
// severity and slots, works with the learning agent as well as the oracle, and
// throttle.enabled (static throttling) is rejected.
func TestParseScenariosThrottle(t *testing.T) {
	base := func() *Config {
		return &Config{NodeNum: 7, EpochMode: true, ScenarioMode: true, OracleMode: true,
			Scenarios: []string{"Healthy", "Throttle"}, ProposalMinIntervalMs: 100}
	}
	c := base()
	if err := c.ParseScenarios(); err != nil {
		t.Fatalf("valid Throttle config: %v", err)
	}
	if !c.ThrottleScenarioMode() {
		t.Fatal("ThrottleScenarioMode() = false with Throttle in scenarios")
	}

	agent := base()
	agent.OracleMode = false
	if err := agent.ParseScenarios(); err != nil {
		t.Fatalf("Throttle with the learning agent: %v", err)
	}

	noThrottle := base()
	noThrottle.Scenarios = []string{"Healthy", "NetworkDelay"}
	if err := noThrottle.ParseScenarios(); err != nil || noThrottle.ThrottleScenarioMode() {
		t.Fatalf("no Throttle in scenarios: err %v, ThrottleScenarioMode %t", err, noThrottle.ThrottleScenarioMode())
	}

	for name, mutate := range map[string]func(*Config){
		"no gate severity":         func(c *Config) { c.ProposalMinIntervalMs = 0 },
		"gate at start":            func(c *Config) { c.ProposalGateAtStart = true },
		"too many slots":           func(c *Config) { c.Throttle.Slots = 8 },
		"static throttle in scenario mode": func(c *Config) {
			c.Scenarios = []string{"Healthy"}
			c.Throttle.Enabled = true
		},
	} {
		c := base()
		mutate(c)
		if err := c.ParseScenarios(); err == nil {
			t.Errorf("%s: want error, got nil", name)
		}
	}
}
