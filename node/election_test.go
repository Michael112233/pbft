package node

import (
	"bytes"
	"math/big"
	"reflect"
	"testing"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/logger"
	"github.com/michael112233/pbft/vr"
)

func TestEvalElectionVDFReportsCompletion(t *testing.T) {
	t.Chdir(t.TempDir())
	n := &Node{
		log:                 logger.NewLogger(1, "node"),
		electionVDFResultCh: make(chan electionVDFResult, 1),
		eventLoopStopCh:     make(chan struct{}),
		electionManager:     NewElectionManager(),
	}
	view := core.ViewID{Generation: 2, Counter: 7}
	seed := []byte("view-7")
	delaySteps := uint64(8)
	modulus := big.NewInt(77)
	vrfProof := []byte("vrf-proof")
	beta := []byte("beta")

	n.electionManager.electionVDFWorkers.Add(1)
	go n.evalElectionVDF(view, seed, delaySteps, modulus, vrfProof, beta)

	select {
	case result := <-n.electionVDFResultCh:
		if result.err != nil {
			t.Fatalf("VDF worker returned error: %v", result.err)
		}
		if result.view != view || result.delaySteps != delaySteps {
			t.Fatalf("completion metadata = (view %v, delay %d), want (view %v, delay %d)", result.view, result.delaySteps, view, delaySteps)
		}
		if !bytes.Equal(result.seed, seed) || !bytes.Equal(result.vrfProof, vrfProof) || !bytes.Equal(result.beta, beta) {
			t.Fatal("completion did not preserve its election inputs")
		}

		valid, err := vr.ValidateVDF(seed, result.y, result.vdfProof, modulus, delaySteps)
		if err != nil {
			t.Fatalf("ValidateVDF() error = %v", err)
		}
		if !valid {
			t.Fatal("event-loop completion contained an invalid VDF proof")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for VDF completion")
	}

	n.electionManager.electionVDFWorkers.Wait()
}

func TestElectionCandidateForView(t *testing.T) {
	const nodeNum = 4
	const draws = 3000

	for _, tt := range []struct {
		name     string
		excluded map[int]bool
	}{
		{name: "no exclusions", excluded: nil},
		{name: "empty exclusions", excluded: map[int]bool{}},
		{name: "node 2 excluded", excluded: map[int]bool{2: true}},
		{name: "false entry is not an exclusion", excluded: map[int]bool{2: false}},
		{name: "two excluded", excluded: map[int]bool{1: true, 3: true}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			counts := map[int]int{}
			for gen := uint64(1); gen <= 30; gen++ {
				for counter := uint64(1); counter <= 100; counter++ {
					view := core.ViewID{Generation: gen, Counter: counter}
					counts[electionCandidateForView(view, nodeNum, tt.excluded)]++
				}
			}

			pool := 0
			for id := 1; id <= nodeNum; id++ {
				if tt.excluded[id] {
					if counts[id] != 0 {
						t.Fatalf("excluded node %d picked %d times", id, counts[id])
					}
					continue
				}
				pool++
			}

			// Uniform over the pool, within 15%.
			want := float64(draws) / float64(pool)
			for id := 1; id <= nodeNum; id++ {
				if tt.excluded[id] {
					continue
				}
				if float64(counts[id]) < want*0.85 || float64(counts[id]) > want*1.15 {
					t.Errorf("node %d picked %d times, want ~%.0f (counts %v)", id, counts[id], want, counts)
				}
			}
		})
	}
}

// With no crashed nodes, Election must draw from the same number of nodes
// RoundRobin rotates over, or the impairment experiment compares unequal pools.
func TestElectionCandidateForViewUsesFullPoolWhenNothingIsDead(t *testing.T) {
	const nodeNum = 7
	seen := map[int]bool{}
	for counter := uint64(1); counter <= 500; counter++ {
		seen[electionCandidateForView(core.ViewID{Generation: 1, Counter: counter}, nodeNum, nil)] = true
	}
	for id := 1; id <= nodeNum; id++ {
		if !seen[id] {
			t.Errorf("node %d never stood as candidate; pool is smaller than nodeNum", id)
		}
	}
}

// In scenario mode nodes_dead are excluded only in NetworkDelayFCrash
// generations; outside scenario mode they are crashed all run, so always excluded.
func TestElectionExcludedForGeneration(t *testing.T) {
	dead := map[int]bool{2: true, 3: true} // n=7, f=2
	list := []core.Scenario{core.ScenarioHealthy, core.ScenarioNetworkDelayFCrash, core.ScenarioProposalDelay, core.ScenarioNetworkDelay}
	cfg := &config.Config{NodeNum: 7, NodesDead: dead, ScenarioMode: true, ScenariosEnum: list, ScenarioGenerations: 10}

	for gen := uint64(1); gen <= 80; gen++ {
		got := electionExcludedForGeneration(cfg, gen)
		inFCrash := scenarioForGeneration(gen, list, 10) == core.ScenarioNetworkDelayFCrash
		if inFCrash && !reflect.DeepEqual(got, dead) {
			t.Fatalf("gen %d (NetworkDelayFCrash): excluded %v, want %v", gen, got, dead)
		}
		if !inFCrash && len(got) != 0 {
			t.Fatalf("gen %d (%s): excluded %v, want none", gen, core.ScenarioToString(scenarioForGeneration(gen, list, 10)), got)
		}
	}

	// Outside FCrash the dead ids stand as candidates like anyone else.
	seen := map[int]bool{}
	for counter := uint64(1); counter <= 500; counter++ {
		view := core.ViewID{Generation: 1, Counter: counter} // gen 1 is Healthy
		seen[electionCandidateForView(view, 7, electionExcludedForGeneration(cfg, view.Generation))] = true
	}
	if !seen[2] || !seen[3] {
		t.Fatalf("Healthy generation never picked nodes_dead ids 2/3 (seen %v)", seen)
	}

	static := &config.Config{NodeNum: 7, NodesDead: dead}
	if got := electionExcludedForGeneration(static, 1); !reflect.DeepEqual(got, dead) {
		t.Fatalf("non-scenario mode: excluded %v, want %v", got, dead)
	}
}

func TestElectionCandidateForViewIsDeterministic(t *testing.T) {
	view := core.ViewID{Generation: 3, Counter: 11}
	first := electionCandidateForView(view, 7, map[int]bool{2: true})
	for i := 0; i < 10; i++ {
		if got := electionCandidateForView(view, 7, map[int]bool{2: true}); got != first {
			t.Fatalf("candidate for the same view changed: %d then %d", first, got)
		}
	}
}
