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
	signedMini   core.EpochAggregateMsgMini
	broadcastMsg core.EpochAggregateMsg
	broadcastSig []byte
}

func (n *epochNodeStub) GetNodeID() int { return n.nodeID }

func (n *epochNodeStub) QuorumSize() int { return n.quorum }

func (n *epochNodeStub) signEpochAggregateMsg(msg core.EpochAggregateMsgMini) ([]byte, error) {
	n.signedMini = msg
	return []byte("aggregate-signature"), nil
}

func (n *epochNodeStub) asyncBroadCast(msgType string, msg interface{}, signature []byte) {
	if msgType != core.MsgEpochAggregateMessage {
		return
	}
	n.broadcastMsg = msg.(core.EpochAggregateMsg)
	n.broadcastSig = append([]byte(nil), signature...)
}

func TestEpochManagerBroadcastsSignedAggregateAtQuorum(t *testing.T) {
	t.Chdir(t.TempDir())
	node := &epochNodeStub{nodeID: 4, quorum: 2}
	manager := NewEpochManager(logger.NewLogger(4, "node"), node)

	manager.HandleEpochDataMsg(core.EpochDataMsg{EpochGeneration: 7, From: 3}, []byte("sig-3"))
	if node.broadcastMsg.EpochGeneration != 0 {
		t.Fatal("aggregate broadcast before quorum")
	}
	manager.HandleEpochDataMsg(core.EpochDataMsg{EpochGeneration: 7, From: 1}, []byte("sig-1"))

	if node.broadcastMsg.EpochGeneration != 7 || node.broadcastMsg.From != 4 {
		t.Fatalf("broadcast aggregate = %#v", node.broadcastMsg)
	}
	if node.signedMini.EpochGeneration != node.broadcastMsg.EpochGeneration || node.signedMini.From != node.broadcastMsg.From || node.signedMini.EpochData != node.broadcastMsg.EpochData {
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
}
