package node

import (
	"testing"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
)

func TestTryAdvancePrepareReturnsAfterQuorum(t *testing.T) {
	oldNodeAddr := config.NodeAddr
	config.NodeAddr = map[int]string{}
	defer func() {
		config.NodeAddr = oldNodeAddr
	}()

	n, _ := newTestNodeWithKeys(t, 1, 4)
	n.fNodes = 1

	slot := &consensusSlot{
		view: 1,
		prePrepare: &core.PreprepareMsg{
			View:            1,
			SeqNum:          1,
			DigestClientMsg: [32]byte{1},
		},
		prepares: map[int]*core.PrepareMsgSig{
			2: {PrepareMsg: core.PrepareMsg{View: 1, SeqNum: 1, Digest: [32]byte{1}, From: 2}},
			3: {PrepareMsg: core.PrepareMsg{View: 1, SeqNum: 1, Digest: [32]byte{1}, From: 3}},
		},
		commits: make(map[int][32]byte),
	}

	done := make(chan struct{})
	go func() {
		n.tryAdvancePrepare(slot, 1, 1, [32]byte{1})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("tryAdvancePrepare did not return after prepare quorum")
	}

	slot.mu.Lock()
	defer slot.mu.Unlock()
	if !slot.commitSent {
		t.Fatal("slot.commitSent was not set")
	}
	if got := slot.commits[n.GetNodeID()]; got != ([32]byte{1}) {
		t.Fatalf("self commit digest = %x, want %x", got, [32]byte{1})
	}
}
