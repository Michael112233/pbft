package node

import (
	"crypto/ed25519"
	cryptorand "crypto/rand"
	"testing"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/crypto"
	"github.com/michael112233/pbft/logger"
)

func TestNewNodeInitializesLeaderForViewOne(t *testing.T) {
	n := NewNode(1, &config.Config{
		NodeNum:        4,
		NodesDead:      map[int]bool{},
		LeaderTypeEnum: core.VCTypeRoundRobin,
	})

	if n.view != 1 {
		t.Fatalf("view = %d, want 1", n.view)
	}
	if n.forView != 1 {
		t.Fatalf("forView = %d, want 1", n.forView)
	}
	if n.leaderId != 1 {
		t.Fatalf("leaderId = %d, want 1", n.leaderId)
	}
	if got := n.leaderIdForView[1]; got != 1 {
		t.Fatalf("leaderIdForView[1] = %d, want 1", got)
	}
}

func newInMemoryKeyStore(t *testing.T, nodeID int, nodeNum int) (*KeyStore, map[int]ed25519.PrivateKey) {
	t.Helper()

	publicKeys := make(map[int]ed25519.PublicKey, nodeNum)
	privateKeys := make(map[int]ed25519.PrivateKey, nodeNum)
	for i := 1; i <= nodeNum; i++ {
		pub, priv, err := ed25519.GenerateKey(cryptorand.Reader)
		if err != nil {
			t.Fatalf("generate key for node %d: %v", i, err)
		}
		publicKeys[i] = pub
		privateKeys[i] = priv
	}
	clientPub, _, err := ed25519.GenerateKey(cryptorand.Reader)
	if err != nil {
		t.Fatalf("generate client key: %v", err)
	}

	return &KeyStore{
		privateKey: privateKeys[nodeID],
		publicKeys: publicKeys,
		clientKey:  clientPub,
	}, privateKeys
}

func newTestNodeWithKeys(t *testing.T, nodeID int, nodeNum int64) (*Node, map[int]ed25519.PrivateKey) {
	t.Helper()

	keyStore, privateKeys := newInMemoryKeyStore(t, nodeID, int(nodeNum))
	log := logger.NewLogger(nodeID, "node")
	hub := NewNodeMessageHub()
	n := &Node{
		NodeID:               nodeID,
		cfg:                  &config.Config{NodeNum: nodeNum, LeaderTypeEnum: core.VCTypeRoundRobin},
		log:                  log,
		messageHub:           hub,
		encryptionKeyStore:   keyStore,
		view:                 1,
		forView:              1,
		leaderId:             1,
		leaderIdForView:      map[int64]int{1: 1},
		consensusLog:         NewConsensusLog(),
		fNodes:               (int(nodeNum) - 1) / 3,
		pbftTimerManager:     NewTimerManager(log),
		viewChangeMsgsLog:    make(map[int64][]*core.ViewChangeMsgSig),
		voteLog:              make(map[int64][]int),
		pool:                 NewPool(),
		pendingExecutions:    make(map[int64]pendingExecution),
		checkpoints:          make(map[checkpoint]checkpointVotes),
		lastStableCheckpoint: checkpoint{seq: 0, digest: [32]byte{}},
		vcType:               core.VCTypeRoundRobin,
	}
	hub.node_ref = n
	hub.log = log
	return n, privateKeys
}

func TestNewViewStoresLeaderForView(t *testing.T) {
	n, _ := newTestNodeWithKeys(t, 2, 4)
	n.forView = 2
	n.viewChangeMsgsLog[2] = []*core.ViewChangeMsgSig{}

	n.newView()

	if got := n.leaderIdForView[2]; got != 2 {
		t.Fatalf("leaderIdForView[2] = %d, want 2", got)
	}
}

func TestHandleNewViewStoresLeaderForView(t *testing.T) {
	n, _ := newTestNodeWithKeys(t, 1, 4)

	viewChangeLog := []*core.ViewChangeMsgSig{
		{
			ViewChangeMsg: core.ViewChangeMsg{
				ViewNumber:          2,
				CheckpointSeqNumber: 0,
				From:                1,
				PreparedCerts:       map[int64]*core.PreparedCert{},
				Type:                core.VCTypeRoundRobin,
				RoundRobinData:      &core.RoundRobinVCData{},
			},
		},
		{
			ViewChangeMsg: core.ViewChangeMsg{
				ViewNumber:          2,
				CheckpointSeqNumber: 0,
				From:                2,
				PreparedCerts:       map[int64]*core.PreparedCert{},
				Type:                core.VCTypeRoundRobin,
				RoundRobinData:      &core.RoundRobinVCData{},
			},
		},
		{
			ViewChangeMsg: core.ViewChangeMsg{
				ViewNumber:          2,
				CheckpointSeqNumber: 0,
				From:                3,
				PreparedCerts:       map[int64]*core.PreparedCert{},
				Type:                core.VCTypeRoundRobin,
				RoundRobinData:      &core.RoundRobinVCData{},
			},
		},
	}
	n.viewChangeMsgsLog[2] = viewChangeLog

	n.HandleNewView(core.NewViewMsg{
		NewViewNumber: 2,
		From:          2,
		PreprepareLog: []core.PreprepareMsgSig{},
		ViewChangeLog: viewChangeLog,
	}, nil)

	if got := n.leaderIdForView[2]; got != 2 {
		t.Fatalf("leaderIdForView[2] = %d, want 2", got)
	}
}

func TestVerifyPreprepareUsesHistoricalLeaderForView(t *testing.T) {
	n, privateKeys := newTestNodeWithKeys(t, 2, 4)
	n.leaderId = 2
	n.leaderIdForView = map[int64]int{1: 1, 2: 2}

	var digest [32]byte
	digest[0] = 7

	payloadBytes, err := marshalDeterministic(preprepareSignPayload(1, 9, digest[:]))
	if err != nil {
		t.Fatalf("marshal preprepare payload: %v", err)
	}
	signature := crypto.SignMessageEd25519(payloadBytes, privateKeys[1])

	ok, view, seq, gotDigest := n.verifyPreprepare(core.PreprepareMsgSig{
		PreprepareMsgMini: core.PreprepareMsgMini{
			View:            1,
			SeqNum:          9,
			DigestClientMsg: digest,
		},
		Signature: signature,
	})
	if !ok {
		t.Fatal("verifyPreprepare returned false, want true")
	}
	if view != 1 {
		t.Fatalf("view = %d, want 1", view)
	}
	if seq != 9 {
		t.Fatalf("seq = %d, want 9", seq)
	}
	if gotDigest != digest {
		t.Fatalf("digest = %x, want %x", gotDigest, digest)
	}
}
