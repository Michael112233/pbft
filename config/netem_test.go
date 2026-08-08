package config

import (
	"strings"
	"testing"
)

func validNetemConfig() *Config {
	return &Config{
		NodeNum: 4,
		Netem: NetemConfig{
			Enabled:    true,
			Interface:  "lo",
			SocketPath: "logs/netem.sock",
			PIDPath:    "logs/netem.pid",
			Limit:      100000,
			Rules: []NetemRule{{
				ID: "delay",
				Event: NetemEventConfig{
					Type:   NetemExecutionEvent,
					NodeID: 1,
					Seq:    2,
				},
				Action: NetemAction{
					DelayMs:  250,
					Lifetime: NetemLifetimeUntilNextEvent,
				},
			}},
		},
	}
}

func TestValidateNetemRejectsInvalidRules(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{name: "duplicate id", mutate: func(cfg *Config) { cfg.Netem.Rules = append(cfg.Netem.Rules, cfg.Netem.Rules[0]) }, wantErr: "duplicate rule id"},
		{name: "duplicate trigger", mutate: func(cfg *Config) {
			duplicate := cfg.Netem.Rules[0]
			duplicate.ID = "other"
			cfg.Netem.Rules = append(cfg.Netem.Rules, duplicate)
		}, wantErr: "same execution trigger"},
		{name: "invalid node", mutate: func(cfg *Config) { cfg.Netem.Rules[0].Event.NodeID = 5 }, wantErr: "invalid node_id"},
		{name: "invalid sequence", mutate: func(cfg *Config) { cfg.Netem.Rules[0].Event.Seq = 0 }, wantErr: "positive execution sequence"},
		{name: "negative delay", mutate: func(cfg *Config) { cfg.Netem.Rules[0].Action.DelayMs = -1 }, wantErr: "negative delay"},
		{name: "duration missing", mutate: func(cfg *Config) { cfg.Netem.Rules[0].Action.Lifetime = NetemLifetimeDuration }, wantErr: "positive duration_ms"},
		{name: "duration on persistent", mutate: func(cfg *Config) { cfg.Netem.Rules[0].Action.DurationMs = 1 }, wantErr: "omit duration_ms"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validNetemConfig()
			tc.mutate(cfg)
			err := cfg.ValidateNetem()
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ValidateNetem() error = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestExecutionRuleMatchesOnlyConfiguredNodeAndSequence(t *testing.T) {
	cfg := validNetemConfig()
	if rule, ok := cfg.Netem.ExecutionRule(1, 2); !ok || rule.ID != "delay" {
		t.Fatalf("ExecutionRule(1, 2) = %#v, %t", rule, ok)
	}
	if _, ok := cfg.Netem.ExecutionRule(2, 2); ok {
		t.Fatal("ExecutionRule matched the wrong node")
	}
	if _, ok := cfg.Netem.ExecutionRule(1, 3); ok {
		t.Fatal("ExecutionRule matched the wrong sequence")
	}
}
