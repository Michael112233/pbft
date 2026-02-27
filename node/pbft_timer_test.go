package node

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/transportpb"
	"google.golang.org/protobuf/proto"
)

func newPBFTTimerTestNode(t *testing.T) (*Node, ed25519.PrivateKey) {
	t.Helper()

	config.NodeAddr = map[int]string{
		1: "localhost:28100",
	}
	config.ClientAddr = "localhost:20000"

	cfg := &config.Config{
		NodeNum:      4,
		MaxBlockSize: 1,
		InjectSpeed:  1,
		ExpireTime:   5,
		RaftTimeout:  5000,
		RaftInterval: 10000,
	}
	n := NewNode(1, cfg)
	n.messageHub.log = n.log
	n.pbftTimerManager.pbftTimeout = 60 * time.Millisecond
	clientPub, clientPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate test client key failed: %v", err)
	}
	n.encryptionKeyStore.clientKey = clientPub

	n.pbftTimerManager.pendingClientReqMu.Lock()
	n.pbftTimerManager.pbftTimer = time.NewTimer(n.pbftTimerManager.pbftTimeout)
	n.pbftTimerManager.stopPBFTTimerLocked()
	n.pbftTimerManager.pendingClientReqMu.Unlock()

	return n, clientPriv
}

func signedPreprepareMsg(t *testing.T, clientPriv ed25519.PrivateKey, view, seq, id int64, clientName string) core.PreprepareMsg {
	t.Helper()

	clientMsg := core.ClientMsg{
		Id:         id,
		ClientName: clientName,
	}
	clientMsgBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(transportpb.ClientMsgToPB(clientMsg))
	if err != nil {
		t.Fatalf("marshal client message failed: %v", err)
	}
	signature := ed25519.Sign(clientPriv, clientMsgBytes)

	digest, err := ComputeBatchDigest(clientMsg)
	if err != nil {
		t.Fatalf("compute digest failed: %v", err)
	}

	return core.PreprepareMsg{
		View:            view,
		SeqNum:          seq,
		DigestClientMsg: digest,
		ClientMsg: core.ClientMsgSignature{
			Data:      clientMsg,
			Signature: signature,
		},
	}
}

func makeExecutedSlot(msg core.PreprepareMsg) *consensusSlot {
	digest := msg.DigestClientMsg
	return &consensusSlot{
		view:       msg.View,
		prePrepare: &msg,
		digest:     digest,
		prepares:   map[int][32]byte{},
		commits: map[int][32]byte{
			1: digest,
			2: digest,
			3: digest,
		},
		commitSent: true,
	}
}

func TestHandlePrePrepareTracksAndStartsTimer(t *testing.T) {
	n, clientPriv := newPBFTTimerTestNode(t)

	msg := signedPreprepareMsg(t, clientPriv, 1, 1, 101, "client-a")
	n.HandlePrePrepare(msg)

	n.pbftTimerManager.pendingClientReqMu.Lock()
	defer n.pbftTimerManager.pendingClientReqMu.Unlock()

	if len(n.pbftTimerManager.pendingClientReq) != 1 {
		t.Fatalf("expected 1 pending request, got %d", len(n.pbftTimerManager.pendingClientReq))
	}
	if !n.pbftTimerManager.pbftTimerInitiated {
		t.Fatalf("expected timer to be initiated")
	}
}

func TestTrackPreprepareRequestIgnoresDuplicate(t *testing.T) {
	n, clientPriv := newPBFTTimerTestNode(t)

	msg := signedPreprepareMsg(t, clientPriv, 1, 1, 201, "client-dup").ClientMsg
	n.pbftTimerManager.trackPreprepareRequest(msg)
	n.pbftTimerManager.trackPreprepareRequest(msg)

	n.pbftTimerManager.pendingClientReqMu.Lock()
	defer n.pbftTimerManager.pendingClientReqMu.Unlock()
	if len(n.pbftTimerManager.pendingClientReq) != 1 {
		t.Fatalf("expected duplicate to be ignored, pending len=%d", len(n.pbftTimerManager.pendingClientReq))
	}
}

func TestTrackPreprepareRequestStartsOnceForFirstInsert(t *testing.T) {
	n, clientPriv := newPBFTTimerTestNode(t)

	msg1 := signedPreprepareMsg(t, clientPriv, 1, 1, 301, "client-a").ClientMsg
	msg2 := signedPreprepareMsg(t, clientPriv, 1, 2, 302, "client-b").ClientMsg

	n.pbftTimerManager.trackPreprepareRequest(msg1)
	firstTimer := n.pbftTimerManager.pbftTimer
	n.pbftTimerManager.trackPreprepareRequest(msg2)

	n.pbftTimerManager.pendingClientReqMu.Lock()
	defer n.pbftTimerManager.pendingClientReqMu.Unlock()
	if len(n.pbftTimerManager.pendingClientReq) != 2 {
		t.Fatalf("expected 2 pending requests, got %d", len(n.pbftTimerManager.pendingClientReq))
	}
	if !n.pbftTimerManager.pbftTimerInitiated {
		t.Fatalf("expected timer to remain initiated")
	}
	if n.pbftTimerManager.pbftTimer != firstTimer {
		t.Fatalf("timer instance should not change between first and second insert")
	}
}

func TestTryExecuteRemovesExecutedRequest(t *testing.T) {
	n, clientPriv := newPBFTTimerTestNode(t)

	pp := signedPreprepareMsg(t, clientPriv, 1, 1, 401, "client-exec")
	n.pbftTimerManager.trackPreprepareRequest(pp.ClientMsg)
	slot := makeExecutedSlot(pp)

	n.tryExecute(slot, pp.SeqNum)

	n.pbftTimerManager.pendingClientReqMu.Lock()
	defer n.pbftTimerManager.pendingClientReqMu.Unlock()
	if len(n.pbftTimerManager.pendingClientReq) != 0 {
		t.Fatalf("expected pending queue to be empty after execute, got %d", len(n.pbftTimerManager.pendingClientReq))
	}
	if n.pbftTimerManager.pbftTimerInitiated {
		t.Fatalf("expected timer to stop when queue becomes empty")
	}
}

func TestTryExecuteResetsTimerWhenPendingStillExists(t *testing.T) {
	n, clientPriv := newPBFTTimerTestNode(t)

	pp1 := signedPreprepareMsg(t, clientPriv, 1, 1, 501, "client-a")
	pp2 := signedPreprepareMsg(t, clientPriv, 1, 2, 502, "client-b")

	n.pbftTimerManager.trackPreprepareRequest(pp1.ClientMsg)
	n.pbftTimerManager.trackPreprepareRequest(pp2.ClientMsg)

	time.Sleep(35 * time.Millisecond)
	slot := makeExecutedSlot(pp1)
	n.tryExecute(slot, pp1.SeqNum)

	select {
	case <-n.pbftTimerManager.pbftTimer.C:
		t.Fatalf("timer fired too early; expected reset on execute")
	case <-time.After(40 * time.Millisecond):
	}

	n.pbftTimerManager.pendingClientReqMu.Lock()
	defer n.pbftTimerManager.pendingClientReqMu.Unlock()
	if len(n.pbftTimerManager.pendingClientReq) != 1 {
		t.Fatalf("expected one pending request remaining, got %d", len(n.pbftTimerManager.pendingClientReq))
	}
	if !n.pbftTimerManager.pbftTimerInitiated {
		t.Fatalf("expected timer to remain initiated after reset with non-empty queue")
	}
}

func TestHandlePBFTTimerExpiryEmptyQueueNoTrigger(t *testing.T) {
	n, _ := newPBFTTimerTestNode(t)

	n.pbftTimerManager.handlePBFTTimerExpiry()

	if n.pbftTimerManager.viewChangeTimeoutDummyCount.Load() != 0 {
		t.Fatalf("expected no dummy trigger for empty queue")
	}
	n.pbftTimerManager.pendingClientReqMu.Lock()
	defer n.pbftTimerManager.pendingClientReqMu.Unlock()
	if n.pbftTimerManager.pbftTimerInitiated {
		t.Fatalf("expected timer initiated flag to be false after expiry with empty queue")
	}
}

func TestHandlePBFTTimerExpiryNonEmptyQueueTriggersDummy(t *testing.T) {
	n, clientPriv := newPBFTTimerTestNode(t)

	pp := signedPreprepareMsg(t, clientPriv, 1, 1, 601, "client-timeout")
	n.pbftTimerManager.trackPreprepareRequest(pp.ClientMsg)
	n.pbftTimerManager.handlePBFTTimerExpiry()

	if n.pbftTimerManager.viewChangeTimeoutDummyCount.Load() != 1 {
		t.Fatalf("expected one dummy trigger, got %d", n.pbftTimerManager.viewChangeTimeoutDummyCount.Load())
	}
	n.pbftTimerManager.pendingClientReqMu.Lock()
	defer n.pbftTimerManager.pendingClientReqMu.Unlock()
	if n.pbftTimerManager.pbftTimerInitiated {
		t.Fatalf("expected timer initiated flag to be false after expiry")
	}
}

func TestPostExpiryNextFirstPreprepareStartsTimerAgain(t *testing.T) {
	n, clientPriv := newPBFTTimerTestNode(t)

	pp1 := signedPreprepareMsg(t, clientPriv, 1, 1, 701, "client-a")
	pp2 := signedPreprepareMsg(t, clientPriv, 1, 2, 702, "client-b")

	n.pbftTimerManager.trackPreprepareRequest(pp1.ClientMsg)
	n.pbftTimerManager.handlePBFTTimerExpiry()
	if n.pbftTimerManager.pbftTimerInitiated {
		t.Fatalf("expected timer to be inactive right after expiry")
	}

	n.pbftTimerManager.trackPreprepareRequest(pp2.ClientMsg)

	n.pbftTimerManager.pendingClientReqMu.Lock()
	defer n.pbftTimerManager.pendingClientReqMu.Unlock()
	if !n.pbftTimerManager.pbftTimerInitiated {
		t.Fatalf("expected timer to start again for next new preprepare")
	}
}
