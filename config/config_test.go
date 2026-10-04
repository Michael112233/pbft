package config

import "testing"

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
