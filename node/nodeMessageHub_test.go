package node

import (
	"crypto/ed25519"
	"testing"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	pbftcrypto "github.com/michael112233/pbft/crypto"
	"github.com/michael112233/pbft/logger"
	"github.com/michael112233/pbft/transportpb"
)

func TestBuildEnvelopeLeaderIdUpdate(t *testing.T) {
	hub := &NodeMessageHub{
		node_ref: &Node{NodeID: 1},
	}

	env, err := hub.buildEnvelope(core.MsgLeaderIdUpdateMessage, core.LeaderIdUpdate{
		From:        "localhost:28100",
		To:          "localhost:20000",
		NewLeaderId: 4,
		View:        7,
	}, nil)
	if err != nil {
		t.Fatalf("buildEnvelope returned error: %v", err)
	}

	body, ok := env.Body.(*transportpb.Envelope_LeaderIdUpdate)
	if !ok {
		t.Fatalf("env.Body type = %T, want *transportpb.Envelope_LeaderIdUpdate", env.Body)
	}

	data, err := transportpb.LeaderIdUpdateFromPB(body.LeaderIdUpdate)
	if err != nil {
		t.Fatalf("LeaderIdUpdateFromPB returned error: %v", err)
	}
	if data.NewLeaderId != 4 {
		t.Fatalf("NewLeaderId = %d, want 4", data.NewLeaderId)
	}
	if data.To != "localhost:20000" {
		t.Fatalf("To = %q, want %q", data.To, "localhost:20000")
	}
	if data.From != "localhost:28100" {
		t.Fatalf("From = %q, want %q", data.From, "localhost:28100")
	}
	if data.View != 7 {
		t.Fatalf("View = %d, want 7", data.View)
	}
}

func TestBuildEnvelopeVCRunningStatus(t *testing.T) {
	hub := &NodeMessageHub{
		node_ref: &Node{NodeID: 1},
	}

	env, err := hub.buildEnvelope(core.MsgVCRunningStatusMessage, core.VCRunningStatus{
		VCRunning: true,
		Txs: []core.ClientMsgSignature{
			{Data: core.ClientMsg{Id: 7, ClientName: "client-a"}, Signature: []byte{1, 2, 3}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("buildEnvelope returned error: %v", err)
	}

	body, ok := env.Body.(*transportpb.Envelope_VcRunningStatus)
	if !ok {
		t.Fatalf("env.Body type = %T, want *transportpb.Envelope_VcRunningStatus", env.Body)
	}

	data, err := transportpb.VCRunningStatusFromPB(body.VcRunningStatus)
	if err != nil {
		t.Fatalf("VCRunningStatusFromPB returned error: %v", err)
	}
	if !data.VCRunning {
		t.Fatal("VCRunning = false, want true")
	}
	if len(data.Txs) != 1 {
		t.Fatalf("len(Txs) = %d, want 1", len(data.Txs))
	}
}

func TestFairnessComplainDeliverAcceptsValidSignature(t *testing.T) {
	hub := newTestFairnessComplainHub(t)
	msg := core.FairnessComplain{
		Digest: [32]byte{1, 2, 3},
		View:   7,
		From:   1,
	}
	env := signedFairnessComplainEnvelope(t, hub, msg)

	ack, err := hub.Deliver(nil, env)
	if err != nil {
		t.Fatalf("Deliver returned error: %v", err)
	}
	if ack == nil || !ack.Ok {
		t.Fatalf("Deliver ack = %+v, want ok", ack)
	}

	key := fairnessComplainKey{digest: msg.Digest, view: msg.View}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		hub.node_ref.fairnessMu.RLock()
		received := hub.node_ref.complainBox[key][msg.From]
		hub.node_ref.fairnessMu.RUnlock()
		if received {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("complaint from node %d was not recorded", msg.From)
}

func TestFairnessComplainDeliverRejectsSenderMismatch(t *testing.T) {
	hub := newTestFairnessComplainHub(t)
	msg := core.FairnessComplain{
		Digest: [32]byte{1, 2, 3},
		View:   7,
		From:   1,
	}
	env := signedFairnessComplainEnvelope(t, hub, msg)
	env.From = 2

	ack, err := hub.Deliver(nil, env)
	if err != nil {
		t.Fatalf("Deliver returned error: %v", err)
	}
	if ack == nil || ack.Ok {
		t.Fatalf("Deliver ack = %+v, want rejection", ack)
	}
}

func TestFairnessComplainDeliverRejectsInvalidSignature(t *testing.T) {
	hub := newTestFairnessComplainHub(t)
	msg := core.FairnessComplain{
		Digest: [32]byte{1, 2, 3},
		View:   7,
		From:   1,
	}
	env := signedFairnessComplainEnvelope(t, hub, msg)
	env.Signature = []byte("invalid")

	ack, err := hub.Deliver(nil, env)
	if err != nil {
		t.Fatalf("Deliver returned error: %v", err)
	}
	if ack == nil || ack.Ok {
		t.Fatalf("Deliver ack = %+v, want rejection", ack)
	}
}

func TestInjectArtificialLatencyForFarNodeSend(t *testing.T) {
	oldNodeAddr := config.NodeAddr
	oldClientAddr := config.ClientAddr
	config.NodeAddr = map[int]string{
		1: "localhost:28100",
		4: "localhost:28400",
	}
	config.ClientAddr = "localhost:20000"
	defer func() {
		config.NodeAddr = oldNodeAddr
		config.ClientAddr = oldClientAddr
	}()

	hub := &NodeMessageHub{
		node_ref: &Node{
			NodeID: 1,
			cfg:    &config.Config{FarNodeID: 4, FarNodeDelayMs: 20},
		},
		log: logger.NewLogger(1, "node"),
	}

	start := time.Now()
	hub.injectArtificialLatency(core.MsgPrepareMessage, "localhost:28400")
	if elapsed := time.Since(start); elapsed < 15*time.Millisecond {
		t.Fatalf("injectArtificialLatency() elapsed = %s, want at least %s", elapsed, 15*time.Millisecond)
	}
}

func newTestFairnessComplainHub(t *testing.T) *NodeMessageHub {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey returned error: %v", err)
	}
	node := &Node{
		NodeID:      1,
		fNodes:      1,
		log:         logger.NewLogger(1, "node"),
		complainBox: make(map[fairnessComplainKey]map[int]bool),
		encryptionKeyStore: &KeyStore{
			privateKey: priv,
			publicKeys: map[int]ed25519.PublicKey{
				1: pub,
			},
		},
	}
	return &NodeMessageHub{
		node_ref: node,
		log:      node.log,
	}
}

func signedFairnessComplainEnvelope(t *testing.T, hub *NodeMessageHub, msg core.FairnessComplain) *transportpb.Envelope {
	t.Helper()
	pbMsg := transportpb.FairnessComplainToPB(msg)
	payloadBytes, err := marshalDeterministic(pbMsg)
	if err != nil {
		t.Fatalf("marshalDeterministic returned error: %v", err)
	}
	return &transportpb.Envelope{
		MsgType:   core.MsgFairnessComplain,
		From:      int32(msg.From),
		Signature: pbftcrypto.SignMessageEd25519(payloadBytes, hub.node_ref.encryptionKeyStore.GetPrivateKey()),
		Body:      &transportpb.Envelope_FairnessComplain{FairnessComplain: pbMsg},
	}
}
