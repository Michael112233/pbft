package node

import (
	"reflect"
	"testing"

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
	up := netemCmd{up: true, delayMs: scenarioNetworkDelayMS}
	const delayNode = 1
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
		gotDelay, gotDead, gotNetem := scenarioEffects(c.nodeID, delayNode, deadNodes, c.next)
		if gotDelay != c.wantDelay || gotDead != c.wantDead || !reflect.DeepEqual(gotNetem, c.wantNetem) {
			t.Errorf("%s: got (%t, %t, %v), want (%t, %t, %v)", c.name, gotDelay, gotDead, gotNetem, c.wantDelay, c.wantDead, c.wantNetem)
		}
	}
}
