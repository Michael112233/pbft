package node

import (
	"bytes"
	"testing"

	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/logger"
)

type epochNodeStub struct {
	nodeID       int
	quorum       int
	nodeCount    int
	graceStarts  int
	broadcasts   int
	storedGens   []uint64
	forView      core.ViewID
	currAction   core.Action
	signedMini   core.EpochAggregateMsgMini
	broadcastMsg core.EpochAggregateMsg
	broadcastSig []byte
}

func (n *epochNodeStub) GetNodeID() int { return n.nodeID }

func (n *epochNodeStub) QuorumSize() int { return n.quorum }

func (n *epochNodeStub) NodeCount() int { return n.nodeCount }

func (n *epochNodeStub) startAggregateGraceTimer() { n.graceStarts++ }

func (n *epochNodeStub) stopAggregateGraceTimer() {}

func (n *epochNodeStub) storeAwareAggregate(gen uint64, _ []core.EpochDataMsgSig) {
	n.storedGens = append(n.storedGens, gen)
}

func (n *epochNodeStub) GetForViewID() core.ViewID { return n.forView }

func (n *epochNodeStub) GetCurrAction() core.Action { return n.currAction }

func (n *epochNodeStub) assert(condition bool, message string, args ...interface{}) {
	if !condition {
		panic("epoch node assertion failed")
	}
}

func (n *epochNodeStub) SendLearningDataToAgent(uint64, core.Action, float64, float64, float64, uint8) {
}

func (n *epochNodeStub) stopEpochTimer() {}

func (n *epochNodeStub) signEpochAggregateMsg(msg core.EpochAggregateMsgMini) ([]byte, error) {
	n.signedMini = msg
	return []byte("aggregate-signature"), nil
}

func (n *epochNodeStub) asyncBroadCast(msgType string, msg interface{}, signature []byte) {
	if msgType != core.MsgEpochAggregateMessage {
		return
	}
	n.broadcasts++
	n.broadcastMsg = msg.(core.EpochAggregateMsg)
	n.broadcastSig = append([]byte(nil), signature...)
}

func TestEpochManagerBroadcastsSignedAggregateAfterGraceTimeout(t *testing.T) {
	t.Chdir(t.TempDir())
	node := &epochNodeStub{
		nodeID:     4,
		quorum:     2,
		nodeCount:  3,
		forView:    core.ViewID{Generation: 7, Counter: 3},
		currAction: core.PerformanceElection,
	}
	manager := NewEpochManager(logger.NewLogger(4, "node"), node)

	manager.HandleEpochDataMsg(core.EpochDataMsg{EpochGeneration: 7, From: 3}, []byte("sig-3"))
	manager.HandleEpochDataMsg(core.EpochDataMsg{EpochGeneration: 7, From: 1}, []byte("sig-1"))
	if node.broadcasts != 0 || node.graceStarts != 1 {
		t.Fatalf("at quorum want grace timer started and no broadcast, got broadcasts=%d graceStarts=%d", node.broadcasts, node.graceStarts)
	}
	manager.onGraceTimeout()

	if node.broadcastMsg.EpochGeneration != 7 || node.broadcastMsg.From != 4 {
		t.Fatalf("broadcast aggregate = %#v", node.broadcastMsg)
	}
	if node.signedMini.EpochGeneration != node.broadcastMsg.EpochGeneration || node.signedMini.From != node.broadcastMsg.From || node.signedMini.EpochData != node.broadcastMsg.EpochData || node.signedMini.CurrentAction != node.broadcastMsg.CurrentAction {
		t.Fatalf("signed mini %#v does not match broadcast aggregate %#v", node.signedMini, node.broadcastMsg)
	}
	if !bytes.Equal([]byte("aggregate-signature"), node.broadcastSig) {
		t.Fatalf("broadcast signature = %q", node.broadcastSig)
	}
	if len(node.broadcastMsg.EpochDataMsgSigs) != 2 {
		t.Fatalf("collected epoch signatures = %#v", node.broadcastMsg.EpochDataMsgSigs)
	}
	collectedSignatures := make(map[int][]byte, len(node.broadcastMsg.EpochDataMsgSigs))
	for _, msgSig := range node.broadcastMsg.EpochDataMsgSigs {
		collectedSignatures[msgSig.EpochDataMsg.From] = msgSig.Signature
	}
	if !bytes.Equal(collectedSignatures[1], []byte("sig-1")) || !bytes.Equal(collectedSignatures[3], []byte("sig-3")) {
		t.Fatalf("collected epoch signatures = %#v", node.broadcastMsg.EpochDataMsgSigs)
	}
	if len(node.storedGens) != 1 || node.storedGens[0] != 7 {
		t.Fatalf("aggregator must store the aware matrix locally once, got %v", node.storedGens)
	}
}

func TestEpochManagerFlushesAtAllNodesAndOnlyOnce(t *testing.T) {
	t.Chdir(t.TempDir())
	node := &epochNodeStub{nodeID: 4, quorum: 3, nodeCount: 4, forView: core.ViewID{Generation: 2, Counter: 1}, currAction: core.FixedAware}
	manager := NewEpochManager(logger.NewLogger(4, "node"), node)

	for _, from := range []int{1, 2, 3} {
		manager.HandleEpochDataMsg(core.EpochDataMsg{EpochGeneration: 2, From: from, RTTms: []float64{1, 2, 3, 4}}, []byte("s"))
	}
	if node.broadcasts != 0 || node.graceStarts != 1 {
		t.Fatalf("after 2f+1: broadcasts=%d graceStarts=%d", node.broadcasts, node.graceStarts)
	}
	manager.HandleEpochDataMsg(core.EpochDataMsg{EpochGeneration: 2, From: 4}, []byte("s"))
	if node.broadcasts != 1 || len(node.broadcastMsg.EpochDataMsgSigs) != 4 {
		t.Fatalf("all n must flush immediately with 4 vectors, broadcasts=%d msgs=%d", node.broadcasts, len(node.broadcastMsg.EpochDataMsgSigs))
	}
	for i, sig := range node.broadcastMsg.EpochDataMsgSigs {
		if sig.EpochDataMsg.From != i+1 {
			t.Fatalf("aggregate must be ordered by sender, got %v at %d", sig.EpochDataMsg.From, i)
		}
	}
	manager.onGraceTimeout()
	manager.HandleEpochDataMsg(core.EpochDataMsg{EpochGeneration: 2, From: 1}, []byte("s"))
	if node.broadcasts != 1 {
		t.Fatalf("aggregate sent %d times, want exactly once", node.broadcasts)
	}
}

func TestEpochManagerGraceTimeoutIgnoredAfterGenerationMoved(t *testing.T) {
	t.Chdir(t.TempDir())
	node := &epochNodeStub{nodeID: 4, quorum: 2, nodeCount: 3, forView: core.ViewID{Generation: 5, Counter: 1}, currAction: core.FixedAware}
	manager := NewEpochManager(logger.NewLogger(4, "node"), node)
	manager.HandleEpochDataMsg(core.EpochDataMsg{EpochGeneration: 5, From: 1}, []byte("s"))
	manager.HandleEpochDataMsg(core.EpochDataMsg{EpochGeneration: 5, From: 2}, []byte("s"))
	node.forView = core.ViewID{Generation: 6, Counter: 1}
	manager.onGraceTimeout()
	if node.broadcasts != 0 {
		t.Fatal("stale grace timer must not send an aggregate for an old generation")
	}
}

func TestEpochManagerGCBelow(t *testing.T) {
	em := &EpochManager{epochMsgSig: map[uint64]map[int]core.EpochDataMsgSig{
		1: {1: {}}, 2: {1: {}}, 3: {1: {}},
	}}
	em.GCBelow(3)
	if len(em.epochMsgSig) != 1 || em.epochMsgSig[3] == nil {
		t.Fatalf("only generation 3 may remain, got %v", em.epochMsgSig)
	}
}
