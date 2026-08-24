package node

import (
	"bytes"
	"math/big"
	"testing"
	"time"

	"github.com/michael112233/pbft/vr"
)

func TestEvalElectionVDFReportsCompletion(t *testing.T) {
	n := &Node{
		electionVDFResultCh: make(chan electionVDFResult, 1),
		eventLoopStopCh:     make(chan struct{}),
		electionManager:     NewElectionManager(),
	}
	view := int64(7)
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
			t.Fatalf("completion metadata = (view %d, delay %d), want (view %d, delay %d)", result.view, result.delaySteps, view, delaySteps)
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
