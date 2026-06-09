package node

import (
	"crypto/ed25519"
	"testing"

	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/crypto"
	"github.com/michael112233/pbft/transportpb"
	"google.golang.org/protobuf/proto"
)

func TestFutureViewMessagesBufferInArrivalOrderDuringViewChange(t *testing.T) {
	n, _ := newTestNodeWithKeys(t, 1, 4)
	n.view = 1
	n.viewChangeRunning = true

	preprepareSig := []byte{1, 2, 3}
	prepareSig := []byte{4, 5, 6}

	n.HandlePrePrepare(core.PreprepareMsg{View: 2, SeqNum: 10}, preprepareSig)
	n.HandlePrepare(core.PrepareMsg{View: 3, SeqNum: 11, From: 2}, prepareSig)
	n.HandleCommit(core.CommitMsg{View: 2, SeqNum: 12, From: 3})

	preprepareSig[0] = 9
	prepareSig[0] = 9

	if got := len(n.bufferedMsgs); got != 3 {
		t.Fatalf("buffered message count = %d, want 3", got)
	}
	if n.bufferedMsgs[0].kind != bufferedPrePrepare || n.bufferedMsgs[0].view != 2 {
		t.Fatalf("first buffered message = %+v, want preprepare for view 2", n.bufferedMsgs[0])
	}
	if n.bufferedMsgs[1].kind != bufferedPrepare || n.bufferedMsgs[1].view != 3 {
		t.Fatalf("second buffered message = %+v, want prepare for view 3", n.bufferedMsgs[1])
	}
	if n.bufferedMsgs[2].kind != bufferedCommit || n.bufferedMsgs[2].view != 2 {
		t.Fatalf("third buffered message = %+v, want commit for view 2", n.bufferedMsgs[2])
	}
	if n.bufferedMsgs[0].signature[0] != 1 {
		t.Fatalf("buffered preprepare signature was not copied")
	}
	if n.bufferedMsgs[1].signature[0] != 4 {
		t.Fatalf("buffered prepare signature was not copied")
	}
}

func TestReplayBufferedMessagesForViewDrainsOnlyMatchingView(t *testing.T) {
	n, _ := newTestNodeWithKeys(t, 1, 4)
	n.view = 2
	n.forView = 2
	n.viewChangeRunning = false

	n.bufferedMsgs = []bufferedConsensusMessage{
		{
			kind:      bufferedPrepare,
			view:      2,
			prepare:   core.PrepareMsg{View: 2, SeqNum: 20, Digest: [32]byte{1}, From: 2},
			signature: []byte{1},
		},
		{
			kind:   bufferedCommit,
			view:   2,
			commit: core.CommitMsg{View: 2, SeqNum: 20, Digest: [32]byte{1}, From: 3},
		},
		{
			kind:      bufferedPrepare,
			view:      3,
			prepare:   core.PrepareMsg{View: 3, SeqNum: 21, Digest: [32]byte{2}, From: 4},
			signature: []byte{2},
		},
	}

	n.replayBufferedMessagesForView(2)

	slot := n.consensusLog.getOrCreateLog(20, 2)
	slot.mu.Lock()
	_, hasPrepare := slot.prepares[2]
	commitDigest, hasCommit := slot.commits[3]
	slot.mu.Unlock()

	if !hasPrepare {
		t.Fatal("expected replayed prepare to be stored in slot")
	}
	if !hasCommit || commitDigest != [32]byte{1} {
		t.Fatal("expected replayed commit to be stored in slot")
	}
	if got := len(n.bufferedMsgs); got != 1 {
		t.Fatalf("remaining buffered message count = %d, want 1", got)
	}
	if n.bufferedMsgs[0].view != 3 || n.bufferedMsgs[0].kind != bufferedPrepare {
		t.Fatalf("remaining buffered message = %+v, want prepare for view 3", n.bufferedMsgs[0])
	}
}

func TestAdvancePreprepareSeqNumberOnlyIncreases(t *testing.T) {
	n, _ := newTestNodeWithKeys(t, 1, 4)

	n.advancePreprepareSeqNumber(10)
	n.advancePreprepareSeqNumber(7)
	n.advancePreprepareSeqNumber(12)

	if got := n.preprepareSeqNumber.Load(); got != 12 {
		t.Fatalf("preprepareSeqNumber = %d, want 12", got)
	}
}

func TestHandlePrePrepareAdvancesSeqNumberOnlyUpward(t *testing.T) {
	n, _ := newTestNodeWithKeys(t, 2, 4)
	clientPub, clientPriv, err := crypto.GenerateEd25519Keypair()
	if err != nil {
		t.Fatalf("generate client key: %v", err)
	}
	n.encryptionKeyStore.clientKey = clientPub

	for _, seq := range []int64{10, 7, 12} {
		slot := n.consensusLog.getOrCreateLog(seq, n.view)
		slot.mu.Lock()
		slot.prepareSent = true
		slot.mu.Unlock()

		msg := signedPreprepareForSeq(t, n.view, seq, clientPriv)
		n.HandlePrePrepare(msg, []byte{byte(seq)})
	}

	if got := n.preprepareSeqNumber.Load(); got != 12 {
		t.Fatalf("preprepareSeqNumber = %d, want 12", got)
	}
}

func signedPreprepareForSeq(t *testing.T, view, seq int64, clientPriv ed25519.PrivateKey) core.PreprepareMsg {
	t.Helper()

	clientMsg := core.ClientMsg{
		Id:         seq,
		ClientName: "client",
	}
	clientMsgBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(transportpb.ClientMsgToPB(clientMsg))
	if err != nil {
		t.Fatalf("marshal client message: %v", err)
	}
	clientMsgSig := core.ClientMsgSignature{
		Data:      clientMsg,
		Signature: crypto.SignMessageEd25519(clientMsgBytes, clientPriv),
	}
	digest, err := ComputeBatchDigest(clientMsg)
	if err != nil {
		t.Fatalf("compute digest: %v", err)
	}

	return core.PreprepareMsg{
		View:            view,
		SeqNum:          seq,
		DigestClientMsg: digest,
		ClientMsg:       clientMsgSig,
	}
}
