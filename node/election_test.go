package node

import (
	"bytes"
	"math/big"
	"testing"
	"time"

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

func TestElectionCandidateForViewSkipsExcludedNode(t *testing.T) {
	const nodeNum = 4
	counts := map[int]int{}
	for gen := uint64(1); gen <= 30; gen++ {
		for counter := uint64(1); counter <= 100; counter++ {
			counts[electionCandidateForView(core.ViewID{Generation: gen, Counter: counter}, nodeNum)]++
		}
	}
	if counts[electionExcludedNodeID] != 0 {
		t.Fatalf("excluded node %d picked %d times", electionExcludedNodeID, counts[electionExcludedNodeID])
	}
	for id := 1; id <= nodeNum; id++ {
		if id == electionExcludedNodeID {
			continue
		}
		// 3000 draws over 3 ids: ~1000 each.
		if counts[id] < 850 || counts[id] > 1150 {
			t.Errorf("node %d picked %d times, want ~1000 (counts %v)", id, counts[id], counts)
		}
	}
}
