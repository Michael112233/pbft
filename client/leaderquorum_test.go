package client

import (
	"testing"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/logger"
)

// The client switches leader on f+1 matching notices (n = 7, f = 2: the third),
// not 2f; a notice for another leader or action does not count toward it.
func TestLeaderUpdateNeedsFPlusOne(t *testing.T) {
	oldNodeAddr := config.NodeAddr
	config.NodeAddr = map[int]string{6: "10.10.1.6:28600"}
	defer func() { config.NodeAddr = oldNodeAddr }()

	c := &Client{
		log:             logger.NewLogger(0, "client"),
		fNodes:          2,
		newLeaderQuorum: make(map[LeaderUpdate]int),
	}
	view := core.ViewID{Generation: 1, Counter: 6}
	notice := core.LeaderIdUpdate{NewLeaderId: 6, View: view, Action: core.PerformanceRoundRobin}
	leader := func() string {
		c.leaderMu.RLock()
		defer c.leaderMu.RUnlock()
		return c.leaderAddr
	}

	other := notice
	other.Action = core.PerformanceElection // different quorum key
	c.HandleLeaderUpdate(other)
	for i := 1; i <= 2; i++ {
		c.HandleLeaderUpdate(notice)
		if got := leader(); got != "" {
			t.Fatalf("switched to %q after %d matching notices, want f+1 = 3", got, i)
		}
	}
	c.HandleLeaderUpdate(notice)
	if got := leader(); got != "10.10.1.6:28600" {
		t.Fatalf("after 3 matching notices leader is %q, want node 6", got)
	}
	if c.currentView != view {
		t.Fatalf("current view %v, want %v", c.currentView, view)
	}
}
