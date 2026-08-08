package config

import (
	"fmt"
	"strings"
)

const (
	NetemExecutionEvent         = "execution"
	NetemLifetimeDuration       = "duration"
	NetemLifetimeUntilNextEvent = "until_next_event"
	DefaultNetemInterface       = "lo"
	DefaultNetemSocketPath      = "logs/netem-controller.sock"
	DefaultNetemLimit           = 100000
)

// two types duration and until_next_event
// dont have duration field for until_next_event
type NetemConfig struct {
	Enabled    bool        `json:"enabled"`
	Interface  string      `json:"interface"`
	SocketPath string      `json:"socket_path"`
	PIDPath    string      `json:"pid_path"`
	Limit      int         `json:"limit"`
	Rules      []NetemRule `json:"rules"`
}

type NetemRule struct {
	ID     string           `json:"id"`
	Event  NetemEventConfig `json:"event"`
	Action NetemAction      `json:"action"`
}

type NetemEventConfig struct {
	Type   string `json:"type"`
	NodeID int    `json:"node_id"`
	Seq    int64  `json:"seq"`
}

type NetemAction struct {
	DelayMs    int64  `json:"delay_ms"`
	Lifetime   string `json:"lifetime"`
	DurationMs int64  `json:"duration_ms,omitempty"`
}

func (cfg *Config) ApplyNetemDefaults() {
	if cfg.Netem.Interface == "" {
		cfg.Netem.Interface = DefaultNetemInterface
	}
	if cfg.Netem.SocketPath == "" {
		cfg.Netem.SocketPath = DefaultNetemSocketPath
	}
	if cfg.Netem.PIDPath == "" {
		cfg.Netem.PIDPath = cfg.Netem.SocketPath + ".pid"
	}
	if cfg.Netem.Limit == 0 {
		cfg.Netem.Limit = DefaultNetemLimit
	}
}

// this explain what could be structure of rules
func (cfg *Config) ValidateNetem() error {
	if !cfg.Netem.Enabled && len(cfg.Netem.Rules) == 0 {
		return nil
	}
	if cfg.Netem.Interface == "" {
		return fmt.Errorf("interface is required")
	}
	if cfg.Netem.SocketPath == "" {
		return fmt.Errorf("socket_path is required")
	}
	if cfg.Netem.PIDPath == "" {
		return fmt.Errorf("pid_path is required")
	}
	if cfg.Netem.Limit <= 0 {
		return fmt.Errorf("limit must be positive")
	}
	if cfg.Netem.Enabled && len(cfg.Netem.Rules) == 0 {
		return fmt.Errorf("at least one rule is required when netem is enabled")
	}
	if cfg.NodeNum < 1 || cfg.NodeNum > 8 {
		return fmt.Errorf("node_num must be between 1 and 8 for loopback netem")
	}

	ids := make(map[string]struct{}, len(cfg.Netem.Rules))
	triggers := make(map[string]string, len(cfg.Netem.Rules))
	for i, rule := range cfg.Netem.Rules {
		if strings.TrimSpace(rule.ID) == "" {
			return fmt.Errorf("rule %d has an empty id", i)
		}
		if _, exists := ids[rule.ID]; exists {
			return fmt.Errorf("duplicate rule id %q", rule.ID)
		}
		ids[rule.ID] = struct{}{}

		if rule.Event.Type != NetemExecutionEvent {
			return fmt.Errorf("rule %q has unsupported event type %q", rule.ID, rule.Event.Type)
		}
		if rule.Event.NodeID < 1 || int64(rule.Event.NodeID) > cfg.NodeNum {
			return fmt.Errorf("rule %q has invalid node_id %d", rule.ID, rule.Event.NodeID)
		}
		if rule.Event.Seq <= 0 {
			return fmt.Errorf("rule %q must use a positive execution sequence", rule.ID)
		}
		triggerKey := fmt.Sprintf("%s:%d:%d", rule.Event.Type, rule.Event.NodeID, rule.Event.Seq)
		if previousID, exists := triggers[triggerKey]; exists {
			return fmt.Errorf("rules %q and %q use the same execution trigger", previousID, rule.ID)
		}
		triggers[triggerKey] = rule.ID

		if rule.Action.DelayMs < 0 {
			return fmt.Errorf("rule %q has a negative delay", rule.ID)
		}
		switch rule.Action.Lifetime {
		case NetemLifetimeDuration:
			if rule.Action.DurationMs <= 0 {
				return fmt.Errorf("rule %q requires a positive duration_ms", rule.ID)
			}
		case NetemLifetimeUntilNextEvent:
			if rule.Action.DurationMs != 0 {
				return fmt.Errorf("rule %q must omit duration_ms for until_next_event", rule.ID)
			}
		default:
			return fmt.Errorf("rule %q has unsupported lifetime %q", rule.ID, rule.Action.Lifetime)
		}
	}
	return nil
}

func (cfg NetemConfig) ExecutionRule(nodeID int, seq int64) (NetemRule, bool) {
	if !cfg.Enabled {
		return NetemRule{}, false
	}
	for _, rule := range cfg.Rules {
		if rule.Event.Type == NetemExecutionEvent && rule.Event.NodeID == nodeID && rule.Event.Seq == seq {
			return rule, true
		}
	}
	return NetemRule{}, false
}
