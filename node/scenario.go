package node

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/michael112233/pbft/core"
)

const (
	// Node 4 owns the netem qdisc, the same experiment-scaffolding hardcode as
	// the epoch aggregator. Only one node may touch the shared qdisc on lo.
	scenarioNetemNodeID    = 4
	scenarioNetworkDelayMS = 170 //match it with alt_run_project
	scenarioNetemScript    = "scripts/netem_scenario.sh"
	netemCommandTimeout    = 15 * time.Second
	netemCmdChanSize       = 8
)

type netemCmd struct {
	up      bool // false = tear the qdisc down
	delayMs int
}

func (c netemCmd) String() string {
	if c.up {
		return fmt.Sprintf("up %dms", c.delayMs)
	}
	return "down"
}

// scenarioForGeneration maps a generation to its scenario: generations 1..span
// get list[0], the next span get list[1], and so on, cycling. It is derived from
// the generation rather than triggered on gen%span, so a node that jumps several
// generations at once (NewView catch-up) still lands in the right scenario.
func scenarioForGeneration(gen uint64, list []core.Scenario, span uint64) core.Scenario {
	return list[((gen-1)/span)%uint64(len(list))]
}

// scenarioEffects says what nodeID must run to enter next. Everything is turned
// off first (both off-paths are idempotent), then only next's fault is enabled.
func scenarioEffects(nodeID, delayNode int, deadNodes map[int]bool, next core.Scenario) (proposalDelay, dead bool, netem []netemCmd) {
	// set to zero once scenario switch
	proposalDelay = next == core.ScenarioProposalDelay && nodeID == delayNode
	dead = next == core.ScenarioNetworkDelayFCrash && deadNodes[nodeID]
	if nodeID == scenarioNetemNodeID {
		netem = append(netem, netemCmd{up: false})
		if next == core.ScenarioNetworkDelay || next == core.ScenarioNetworkDelayFCrash {
			netem = append(netem, netemCmd{up: true, delayMs: scenarioNetworkDelayMS})
		}
	}
	return proposalDelay, dead, netem
}
// 20ms to down at switch to net delay first run down then up

// maybeSwitchScenario applies the scenario for gen if it differs from the
// current one. Loop-owned; netem commands are only queued, never run here.
func (n *Node) maybeSwitchScenario(gen uint64) {
	if !n.scenarioMode {
		return
	}
	next := scenarioForGeneration(gen, n.cfg.ScenariosEnum, n.cfg.ScenarioGenerations)
	if n.scenarioApplied && next == n.currScenario {
		// already applied, nothing to do 
		return
	}
	proposalDelay, dead, cmds := scenarioEffects(n.GetNodeID(), n.cfg.ProposalDelayNode, n.cfg.NodesDead, next)
	n.SetProposalDelay(proposalDelay)
	n.SetDead(dead)
	for _, cmd := range cmds {
		n.enqueueNetemCmd(cmd)
	}
	prev := "none"
	if n.scenarioApplied {
		prev = core.ScenarioToString(n.currScenario)
	}
	n.log.Info("scenario switch gen=%d %s -> %s proposalDelay=%t dead=%t netem=%v",
		gen, prev, core.ScenarioToString(next), proposalDelay, dead, cmds)
	n.currScenario = next
	n.scenarioApplied = true
}

func (n *Node) enqueueNetemCmd(cmd netemCmd) {
	select {
	case n.netemCmdCh <- cmd:
	case <-n.eventLoopStopCh:
	}
}

// startScenario checks the netem script on node 4, starts its worker, and
// applies the first generation's scenario. Called before the event loop
// starts, so it still has sole ownership of node state.
func (n *Node) startScenario() error {
	if !n.scenarioMode {
		return nil
	}
	if n.GetNodeID() == scenarioNetemNodeID {
		path, err := filepath.Abs(scenarioNetemScript)
		if err != nil {
			return fmt.Errorf("resolve netem script: %w", err)
		}
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("netem script: %w", err)
		}
		n.netemScriptPath = path
		n.netemCmdCh = make(chan netemCmd, netemCmdChanSize)
		n.netemWorkerDone = make(chan struct{})
		go n.netemWorker()
	}
	n.maybeSwitchScenario(n.GetForViewID().Generation)
	return nil
}

// stopScenario drains the netem worker and removes the qdisc so the delay does
// not outlive the node. Must run after the event loop has stopped (the loop is
// the only sender on netemCmdCh).
func (n *Node) stopScenario() {
	if n.netemCmdCh == nil {
		return
	}
	n.netemStopOnce.Do(func() {
		close(n.netemCmdCh)
		<-n.netemWorkerDone
		n.runNetemCmd(netemCmd{up: false})
	})
}

// netemWorker runs queued commands one at a time, waiting for each, so a down
// always finishes before the next up. It touches no node state.
func (n *Node) netemWorker() {
	defer close(n.netemWorkerDone)
	for cmd := range n.netemCmdCh {
		n.runNetemCmd(cmd)
	}
}

func (n *Node) runNetemCmd(cmd netemCmd) {
	args := []string{"-n", n.netemScriptPath, "down"}
	if cmd.up {
		args = []string{"-n", n.netemScriptPath, "up", strconv.Itoa(cmd.delayMs), strconv.FormatInt(n.cfg.NodeNum, 10)}
	}
	ctx, cancel := context.WithTimeout(context.Background(), netemCommandTimeout)
	defer cancel()
	start := time.Now()
	output, err := exec.CommandContext(ctx, "sudo", args...).CombinedOutput()
	took := time.Since(start)
	if err != nil {
		n.log.Error("netem %s failed took=%v: %v output=%s", cmd, took, err, strings.TrimSpace(string(output)))
		return
	}
	n.log.Info("netem %s applied took=%v", cmd, took)
}
