package node

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/logger"
	"github.com/michael112233/pbft/transportpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
)

func TestBuildEnvelopeAndDeliverViewProtocolMessages(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate signing key: %v", err)
	}

	receiverNode := &Node{
		NodeID:             2,
		log:                logger.NewLogger(2, "node"),
		encryptionKeyStore: &KeyStore{publicKeys: map[int]ed25519.PublicKey{1: publicKey}},
		consensusMsgChan:   make(chan ConsensusMsg, 1),
		viewChangeMsgChan:  make(chan ViewChangeMsg, 1),
		checkpointMsgChan:  make(chan CheckpointMsg, 1),
		newViewMsgChan:     make(chan NewViewMsg, 20),
	}
	receiverHub := &NodeMessageHub{node_ref: receiverNode, log: receiverNode.log}
	senderHub := &NodeMessageHub{node_ref: &Node{NodeID: 1}}

	tests := []struct {
		name       string
		msgType    string
		msg        interface{}
		payload    func(*transportpb.Envelope) proto.Message
		assertSent func(*testing.T)
	}{
		{
			name:    "view change",
			msgType: core.MsgViewChangeMessage,
			msg: core.ViewChangeMsg{
				ViewNumber:     2,
				From:           1,
				Type:           core.VCTypeRoundRobin,
				RoundRobinData: &core.RoundRobinVCData{},
			},
			payload: func(env *transportpb.Envelope) proto.Message { return env.GetViewChange() },
			assertSent: func(t *testing.T) {
				delivered := <-receiverNode.viewChangeMsgChan
				msg, ok := delivered.Msg.(core.ViewChangeMsg)
				if !ok || msg.ViewNumber != 2 || msg.From != 1 {
					t.Fatalf("delivered view-change = %#v", delivered.Msg)
				}
			},
		},
		{
			name:    "checkpoint",
			msgType: core.MsgCheckpointMessage,
			msg: core.CheckpointMsg{
				SeqNum: 100,
				Digest: [32]byte{1, 2, 3},
				From:   1,
			},
			payload: func(env *transportpb.Envelope) proto.Message { return env.GetCheckpoint() },
			assertSent: func(t *testing.T) {
				delivered := <-receiverNode.checkpointMsgChan
				msg, ok := delivered.Msg.(core.CheckpointMsg)
				if !ok || msg.SeqNum != 100 || msg.From != 1 {
					t.Fatalf("delivered checkpoint = %#v", delivered.Msg)
				}
			},
		},
		{
			name:    "new view",
			msgType: core.MsgNewViewMessage,
			msg: core.NewViewMsg{
				NewViewNumber: 2,
				From:          1,
			},
			payload: func(env *transportpb.Envelope) proto.Message { return env.GetNewView() },
			assertSent: func(t *testing.T) {
				delivered := <-receiverNode.newViewMsgChan
				msg, ok := delivered.Msg.(core.NewViewMsg)
				if !ok || msg.NewViewNumber != 2 || msg.From != 1 {
					t.Fatalf("delivered new-view = %#v", delivered.Msg)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env, err := senderHub.buildEnvelope(tt.msgType, tt.msg, nil)
			if err != nil {
				t.Fatalf("buildEnvelope returned error: %v", err)
			}
			payloadBytes, err := marshalDeterministic(tt.payload(env))
			if err != nil {
				t.Fatalf("marshal signing payload: %v", err)
			}
			env.Signature = ed25519.Sign(privateKey, payloadBytes)

			ack, err := receiverHub.Deliver(context.Background(), env)
			if err != nil {
				t.Fatalf("Deliver returned error: %v", err)
			}
			if !ack.Ok {
				t.Fatalf("Deliver rejected message: %s", ack.Error)
			}
			tt.assertSent(t)
		})
	}
}

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

func TestNodeMessagesReusePersistentPeerStream(t *testing.T) {
	nodeLog := &logger.Logger{}
	receiver := &Node{
		NodeID: 2,
		log:    nodeLog,
	}
	receiverHub := NewNodeMessageHub()
	receiverHub.node_ref = receiver
	receiverHub.log = nodeLog
	receiverHub.streamCtx, receiverHub.streamCancel = context.WithCancel(context.Background())
	receiverHub.listener = bufconn.Listen(1024 * 1024)
	receiverHub.grpcSrv = grpc.NewServer()
	transportpb.RegisterPBFTTransportServer(receiverHub.grpcSrv, receiverHub)
	var receiverWG sync.WaitGroup
	receiverWG.Add(1)
	go func() {
		defer receiverWG.Done()
		_ = receiverHub.grpcSrv.Serve(receiverHub.listener)
	}()
	defer func() {
		receiverHub.Close()
		receiverWG.Wait()
	}()

	senderHub := NewNodeMessageHub()
	senderHub.node_ref = &Node{NodeID: 1, log: nodeLog}
	senderHub.log = nodeLog
	senderHub.streamCtx, senderHub.streamCancel = context.WithCancel(context.Background())
	senderHub.dialContext = func(context.Context, string) (net.Conn, error) {
		return receiverHub.listener.(*bufconn.Listener).Dial()
	}
	defer senderHub.Close()
	receiverAddr := "passthrough:///peer-2"

	sendClose := func(timestamp int64) {
		t.Helper()
		senderHub.Send(core.MsgCloseMessage, receiverAddr, core.CloseMessage{
			Timestamp: timestamp,
			From:      "node-1",
			To:        "node-2",
		}, nil)
	}

	waitForStream := func() *peerStreamState {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for {
			senderHub.mu.RLock()
			state := senderHub.peerStreams[receiverAddr]
			senderHub.mu.RUnlock()
			if state != nil {
				return state
			}
			if time.Now().After(deadline) {
				t.Fatal("persistent peer stream was not cached")
			}
			time.Sleep(time.Millisecond)
		}
	}

	sendClose(100)
	firstState := waitForStream()

	sendClose(200)

	senderHub.mu.RLock()
	secondState := senderHub.peerStreams[receiverAddr]
	streamCount := len(senderHub.peerStreams)
	senderHub.mu.RUnlock()
	if secondState != firstState {
		t.Fatal("second node message did not reuse the existing peer stream")
	}
	if streamCount != 1 {
		t.Fatalf("peer stream count = %d, want 1", streamCount)
	}

	receiverHub.clientStreamMu.RLock()
	clientStream := receiverHub.clientStream
	receiverHub.clientStreamMu.RUnlock()
	if clientStream != nil {
		t.Fatal("node stream replaced the dedicated client response stream")
	}

	receiverHub.Close()
	receiverWG.Wait()
	dropDeadline := time.Now().Add(2 * time.Second)
	for {
		senderHub.mu.RLock()
		remainingStreams := len(senderHub.peerStreams)
		senderHub.mu.RUnlock()
		if remainingStreams == 0 {
			break
		}
		if time.Now().After(dropDeadline) {
			t.Fatalf("peer stream was not removed after remote shutdown; remaining=%d", remainingStreams)
		}
		time.Sleep(time.Millisecond)
	}
}
