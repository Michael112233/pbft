package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadCfgParsesFarNodeLatencySettings(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cfg.json")
	content := `{
		"max_tx_num": 10,
		"inject_speed": 1,
		"max_block_size": 1,
		"node_num": 4,
		"nodes_dead": {},
		"periodic": false,
		"period": 100,
		"peak_tps_test": false,
		"leader_type": "roundrobin",
		"far_node_id": 4,
		"far_node_delay_ms": 125
	}`
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	cfg := ReadCfg(cfgPath)
	if cfg.FarNodeID != 4 {
		t.Fatalf("FarNodeID = %d, want 4", cfg.FarNodeID)
	}
	if cfg.FarNodeDelayMs != 125 {
		t.Fatalf("FarNodeDelayMs = %d, want 125", cfg.FarNodeDelayMs)
	}
}

func TestArtificialLatency(t *testing.T) {
	oldNodeAddr := NodeAddr
	NodeAddr = map[int]string{
		1: "localhost:28100",
		2: "localhost:28200",
		3: "localhost:28300",
		4: "localhost:28400",
	}
	defer func() {
		NodeAddr = oldNodeAddr
	}()

	cfg := &Config{FarNodeID: 4, FarNodeDelayMs: 75}
	want := 75 * time.Millisecond

	tests := []struct {
		name string
		from string
		to   string
		want time.Duration
	}{
		{name: "sender is far node", from: "localhost:28400", to: "localhost:28100", want: want},
		{name: "receiver is far node", from: "localhost:28100", to: "localhost:28400", want: want},
		{name: "client to far node", from: "localhost:20000", to: "localhost:28400", want: want},
		{name: "far node to client", from: "localhost:28400", to: "localhost:20000", want: want},
		{name: "unrelated pair", from: "localhost:28100", to: "localhost:28200", want: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := cfg.ArtificialLatency(tc.from, tc.to); got != tc.want {
				t.Fatalf("ArtificialLatency(%q, %q) = %s, want %s", tc.from, tc.to, got, tc.want)
			}
		})
	}
}

func TestArtificialLatencyDisabledByDefault(t *testing.T) {
	cfg := &Config{}
	if got := cfg.ArtificialLatency("localhost:28400", "localhost:28100"); got != 0 {
		t.Fatalf("ArtificialLatency() = %s, want 0", got)
	}
}
