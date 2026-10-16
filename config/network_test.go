package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestGenerateLocalNetworkLearningAgentAddresses(t *testing.T) {
	GenerateLocalNetwork(4)

	if got := LearningAgentAddr[1]; got != "127.0.0.1:29001" {
		t.Fatalf("LearningAgentAddr[1] = %q, want %q", got, "127.0.0.1:29001")
	}
	if got := LearningAgentAddr[4]; got != "127.0.0.1:29004" {
		t.Fatalf("LearningAgentAddr[4] = %q, want %q", got, "127.0.0.1:29004")
	}
}

func TestGenerateLoopbackIPNetworkLearningAgentAddresses(t *testing.T) {
	GenerateLoopbackIPNetwork(4)

	if got := LearningAgentAddr[1]; got != "127.0.0.2:29000" {
		t.Fatalf("LearningAgentAddr[1] = %q, want %q", got, "127.0.0.2:29000")
	}
	if got := LearningAgentAddr[4]; got != "127.0.0.5:29000" {
		t.Fatalf("LearningAgentAddr[4] = %q, want %q", got, "127.0.0.5:29000")
	}
}

func testHosts(nodes, clients int) *Hosts {
	h := &Hosts{Nodes: map[string]HostEntry{}, Clients: map[string]HostEntry{}}
	for i := 1; i <= nodes; i++ {
		h.Nodes[strconv.Itoa(i)] = HostEntry{IP: fmt.Sprintf("10.10.1.%d", i)}
	}
	for i := 1; i <= clients; i++ {
		h.Clients[strconv.Itoa(i)] = HostEntry{IP: fmt.Sprintf("10.10.1.%d", nodes+i)}
	}
	return h
}

func TestGenerateRemoteNetworkUsesHostsFile(t *testing.T) {
	if err := GenerateRemoteNetwork(7, 1, testHosts(7, 1)); err != nil {
		t.Fatal(err)
	}

	if NetworkMode != NetworkModeRemote {
		t.Fatalf("NetworkMode = %q, want %q", NetworkMode, NetworkModeRemote)
	}
	if len(NodeAddr) != 7 {
		t.Fatalf("NodeAddr has %d entries, want 7: %v", len(NodeAddr), NodeAddr)
	}
	if got := NodeAddr[1]; got != "10.10.1.1:28100" {
		t.Fatalf("NodeAddr[1] = %q, want 10.10.1.1:28100", got)
	}
	if got := NodeAddr[7]; got != "10.10.1.7:28700" {
		t.Fatalf("NodeAddr[7] = %q, want 10.10.1.7:28700", got)
	}
	if got := LearningAgentAddr[7]; got != "10.10.1.7:29000" {
		t.Fatalf("LearningAgentAddr[7] = %q, want 10.10.1.7:29000", got)
	}
	if got := ClientAddrFor(1, 1); got != "10.10.1.8:20000" {
		t.Fatalf("client addr = %q, want 10.10.1.8:20000", got)
	}
}

func TestGenerateRemoteNetworkRejectsBadHosts(t *testing.T) {
	cases := map[string]struct {
		hosts     *Hosts
		clientNum int
	}{
		"no hosts file":    {nil, 1},
		"node missing":     {testHosts(6, 1), 1},
		"too few clients":  {testHosts(7, 1), 2},
		"node id 0":        {func() *Hosts { h := testHosts(7, 1); h.Nodes["0"] = HostEntry{IP: "10.10.1.50"}; return h }(), 1},
		"bad ip":           {func() *Hosts { h := testHosts(7, 1); h.Nodes["3"] = HostEntry{IP: "node3"}; return h }(), 1},
		"shared node ip":   {func() *Hosts { h := testHosts(7, 1); h.Nodes["2"] = HostEntry{IP: "10.10.1.1"}; return h }(), 1},
		"client on a node": {func() *Hosts { h := testHosts(7, 1); h.Clients["1"] = HostEntry{IP: "10.10.1.4"}; return h }(), 1},
	}
	for name, tc := range cases {
		if err := GenerateRemoteNetwork(7, tc.clientNum, tc.hosts); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestLoadHosts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts.json")
	data := `{"run_dir": "pbft-run",
	  "nodes": {"1": {"ip": "10.10.1.1", "ssh": "node1"}, "2": {"ip": "10.10.1.2", "ssh": "node2"},
	            "3": {"ip": "10.10.1.3", "ssh": "node3"}, "4": {"ip": "10.10.1.4", "ssh": "node4"}},
	  "clients": {"1": {"ip": "10.10.1.8", "ssh": "client"}}}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	h, err := LoadHosts(path)
	if err != nil {
		t.Fatal(err)
	}
	if h.RunDir != "pbft-run" || h.Nodes["4"].SSH != "node4" || h.Clients["1"].IP != "10.10.1.8" {
		t.Fatalf("parsed %+v", h)
	}
	if err := GenerateRemoteNetwork(4, 1, h); err != nil {
		t.Fatal(err)
	}
}

func TestClientAddrFor(t *testing.T) {
	GenerateLoopbackIPNetwork(7)

	if got := ClientAddrFor(1, 1); got != "127.0.0.1:20000" {
		t.Fatalf("single client addr = %q, want the legacy 127.0.0.1:20000", got)
	}
	if got := ClientAddrFor(1, 7); got != "127.0.1.2:20000" {
		t.Fatalf("client 1 of 7 = %q, want 127.0.1.2:20000", got)
	}
	if got := ClientAddrFor(7, 7); got != "127.0.1.8:20000" {
		t.Fatalf("client 7 of 7 = %q, want 127.0.1.8:20000", got)
	}
}
