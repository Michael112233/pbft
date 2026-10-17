package node

import (
	"reflect"
	"testing"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
)

func TestScenarioForGeneration(t *testing.T) {
	list := []core.Scenario{core.ScenarioHealthy, core.ScenarioProposalDelay, core.ScenarioNetworkDelay}
	cases := []struct {
		gen  uint64
		want core.Scenario
	}{
		{1, core.ScenarioHealthy},
		{100, core.ScenarioHealthy},
		{101, core.ScenarioProposalDelay},
		{200, core.ScenarioProposalDelay},
		{201, core.ScenarioNetworkDelay},
		{205, core.ScenarioNetworkDelay}, // NewView catch-up can jump straight here from 99
		{300, core.ScenarioNetworkDelay},
		{301, core.ScenarioHealthy}, // cycles
		{601, core.ScenarioHealthy},
	}
	for _, c := range cases {
		if got := scenarioForGeneration(c.gen, list, 100); got != c.want {
			t.Errorf("gen %d: got %s, want %s", c.gen, core.ScenarioToString(got), core.ScenarioToString(c.want))
		}
	}
}

func TestScenarioEffects(t *testing.T) {
	down := netemCmd{up: false}
	up := netemCmd{up: true, delayMs: 170}
	delayNodes := map[int]bool{1: true, 2: false, 3: false, 4: false}
	deadNodes := map[int]bool{1: false, 2: true, 3: false, 4: false}
	cases := []struct {
		name      string
		nodeID    int
		next      core.Scenario
		wantDelay bool
		wantDead  bool
		wantNetem []netemCmd
	}{
		{"healthy delay node", 1, core.ScenarioHealthy, false, false, nil},
		{"healthy netem node", 4, core.ScenarioHealthy, false, false, []netemCmd{down}},
		{"healthy revives dead node", 2, core.ScenarioHealthy, false, false, nil},
		{"proposal delay on delay node", 1, core.ScenarioProposalDelay, true, false, nil},
		{"proposal delay on other node", 2, core.ScenarioProposalDelay, false, false, nil},
		{"proposal delay netem node tears down", 4, core.ScenarioProposalDelay, false, false, []netemCmd{down}},
		{"network delay netem node", 4, core.ScenarioNetworkDelay, false, false, []netemCmd{down, up}},
		{"network delay other node", 2, core.ScenarioNetworkDelay, false, false, nil},
		{"network delay clears proposal delay", 1, core.ScenarioNetworkDelay, false, false, nil},
		{"f crash kills dead node", 2, core.ScenarioNetworkDelayFCrash, false, true, nil},
		{"f crash leaves live node", 1, core.ScenarioNetworkDelayFCrash, false, false, nil},
		{"f crash netem node", 4, core.ScenarioNetworkDelayFCrash, false, false, []netemCmd{down, up}},
	}
	for _, c := range cases {
		gotDelay, gotDead, gotNetem := scenarioEffects(c.nodeID, ownsScenarioNetem(c.nodeID, config.NetworkModeLoopbackIP), delayNodes, deadNodes, c.next, 170)
		if gotDelay != c.wantDelay || gotDead != c.wantDead || !reflect.DeepEqual(gotNetem, c.wantNetem) {
			t.Errorf("%s: got (%t, %t, %v), want (%t, %t, %v)", c.name, gotDelay, gotDead, gotNetem, c.wantDelay, c.wantDead, c.wantNetem)
		}
	}
}

// With f > 1 every node in the delay set is slowed, and only in ProposalDelay;
// the dead set is independent of it.
func TestScenarioEffectsFNodes(t *testing.T) {
	delayNodes := map[int]bool{1: true, 5: true} // n=7, f=2
	deadNodes := map[int]bool{2: true, 3: true}  // n=7, f=2
	for id := 1; id <= 7; id++ {
		delay, dead, _ := scenarioEffects(id, false, delayNodes, deadNodes, core.ScenarioProposalDelay, 170)
		if delay != delayNodes[id] || dead {
			t.Errorf("ProposalDelay node %d: got delay=%t dead=%t, want delay=%t dead=false", id, delay, dead, delayNodes[id])
		}
		delay, dead, _ = scenarioEffects(id, false, delayNodes, deadNodes, core.ScenarioNetworkDelayFCrash, 170)
		if delay || dead != deadNodes[id] {
			t.Errorf("NetworkDelayFCrash node %d: got delay=%t dead=%t, want delay=false dead=%t", id, delay, dead, deadNodes[id])
		}
		delay, dead, _ = scenarioEffects(id, false, delayNodes, deadNodes, core.ScenarioHealthy, 170)
		if delay || dead {
			t.Errorf("Healthy node %d: got delay=%t dead=%t, want both false", id, delay, dead)
		}
	}
}

// In remote mode every node delays its own egress, so every node runs the
// netem commands node 4 alone runs on lo.
func TestScenarioNetemOwnership(t *testing.T) {
	for id := 1; id <= 7; id++ {
		if got, want := ownsScenarioNetem(id, config.NetworkModeLoopbackIP), id == scenarioNetemNodeID; got != want {
			t.Errorf("loopbackip node %d owns netem = %t, want %t", id, got, want)
		}
		if !ownsScenarioNetem(id, config.NetworkModeRemote) {
			t.Errorf("remote node %d does not own netem", id)
		}
		_, _, cmds := scenarioEffects(id, ownsScenarioNetem(id, config.NetworkModeRemote), nil, nil, core.ScenarioNetworkDelay, 170)
		want := []netemCmd{{up: false}, {up: true, delayMs: 170}}
		if !reflect.DeepEqual(cmds, want) {
			t.Errorf("remote node %d NetworkDelay cmds = %v, want %v", id, cmds, want)
		}
	}
}

func TestNetemArgs(t *testing.T) {
	addrs := map[int]string{1: "10.10.1.1:28100", 2: "10.10.1.2:28200", 3: "10.10.1.3:28300", 4: "10.10.1.4:28400"}
	peers, err := peerIPs(2, addrs)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"10.10.1.1", "10.10.1.3", "10.10.1.4"}; !reflect.DeepEqual(peers, want) {
		t.Fatalf("peerIPs = %v, want %v", peers, want)
	}
	up := netemCmd{up: true, delayMs: 170}
	down := netemCmd{up: false}
	cases := []struct {
		name  string
		peers []string
		cmd   netemCmd
		want  []string
	}{
		{"lo up", nil, up, []string{"-n", "s.sh", "up", "170", "4"}},
		{"lo down", nil, down, []string{"-n", "s.sh", "down"}},
		{"egress up", peers, up, []string{"-n", "s.sh", "up", "170", "10.10.1.1", "10.10.1.3", "10.10.1.4"}},
		{"egress down", peers, down, []string{"-n", "s.sh", "down", "10.10.1.1", "10.10.1.3", "10.10.1.4"}},
	}
	for _, c := range cases {
		if got := netemArgs("s.sh", c.peers, 4, c.cmd); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	if _, err := peerIPs(1, map[int]string{1: "10.10.1.1:28100", 3: "10.10.1.3:28300"}); err == nil {
		t.Error("peerIPs with node 2 missing: no error")
	}
}

// The network-delay scenarios use the configured delay (scenario_network_delay_ms),
// not a fixed one; on an emulated WAN it is the extra on top of the base delays.
func TestScenarioEffectsUseConfiguredDelay(t *testing.T) {
	for _, next := range []core.Scenario{core.ScenarioNetworkDelay, core.ScenarioNetworkDelayFCrash} {
		_, _, cmds := scenarioEffects(6, true, nil, nil, next, 124)
		want := []netemCmd{{up: false}, {up: true, delayMs: 124}}
		if !reflect.DeepEqual(cmds, want) {
			t.Errorf("%s: cmds = %v, want %v", core.ScenarioToString(next), cmds, want)
		}
	}
}
