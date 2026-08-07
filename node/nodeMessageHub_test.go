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

func TestBuildEnvelopeIntentToChangeView(t *testing.T) {
	hub := &NodeMessageHub{
		node_ref: &Node{NodeID: 2},
	}
	signature := []byte{1, 2, 3}
	intent := core.IntentToChangeViewMsg{
		ViewNumber: 9,
		From:       2,
	}

	env, err := hub.buildEnvelope(core.MsgIntentToChangeViewMessage, intent, signature)
	if err != nil {
		t.Fatalf("buildEnvelope returned error: %v", err)
	}
	if env.From != 2 {
		t.Fatalf("env.From = %d, want 2", env.From)
	}
	if string(env.Signature) != string(signature) {
		t.Fatalf("env.Signature = %v, want %v", env.Signature, signature)
	}

	body, ok := env.Body.(*transportpb.Envelope_IntentToChangeView)
	if !ok {
		t.Fatalf("env.Body type = %T, want *transportpb.Envelope_IntentToChangeView", env.Body)
	}
	data, err := transportpb.IntentToChangeViewFromPB(body.IntentToChangeView)
	if err != nil {
		t.Fatalf("IntentToChangeViewFromPB returned error: %v", err)
	}
	if data != intent {
		t.Fatalf("intent mismatch: got %+v want %+v", data, intent)
	}
}

func TestDeliverIntentToChangeView(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey returned error: %v", err)
	}

	nodeLog := &logger.Logger{}
	hub := &NodeMessageHub{
		node_ref: &Node{
			NodeID: 2,
			log:    nodeLog,
			encryptionKeyStore: &KeyStore{
				publicKeys: map[int]ed25519.PublicKey{1: publicKey},
			},
		},
		log: nodeLog,
	}

	deliver := func(t *testing.T, env *transportpb.Envelope) *transportpb.Ack {
		t.Helper()
		ack, err := hub.Deliver(context.Background(), env)
		if err != nil {
			t.Fatalf("Deliver returned error: %v", err)
		}
		if ack == nil {
			t.Fatal("Deliver returned nil ack")
		}
		return ack
	}

	t.Run("missing body", func(t *testing.T) {
		ack := deliver(t, &transportpb.Envelope{
			MsgType: core.MsgIntentToChangeViewMessage,
			From:    1,
		})
		if ack.Ok || ack.Error != "missing intent to change view body" {
			t.Fatalf("ack = %+v, want missing-body rejection", ack)
		}
	})

	t.Run("sender mismatch", func(t *testing.T) {
		ack := deliver(t, &transportpb.Envelope{
			MsgType: core.MsgIntentToChangeViewMessage,
			From:    1,
			Body: &transportpb.Envelope_IntentToChangeView{
				IntentToChangeView: transportpb.IntentToChangeViewToPB(core.IntentToChangeViewMsg{
					ViewNumber: 9,
					From:       2,
				}),
			},
		})
		if ack.Ok || ack.Error != "intent to change view sender mismatch" {
			t.Fatalf("ack = %+v, want sender-mismatch rejection", ack)
		}
	})

	t.Run("invalid signature", func(t *testing.T) {
		ack := deliver(t, &transportpb.Envelope{
			MsgType:   core.MsgIntentToChangeViewMessage,
			From:      1,
			Signature: []byte("invalid"),
			Body: &transportpb.Envelope_IntentToChangeView{
				IntentToChangeView: transportpb.IntentToChangeViewToPB(core.IntentToChangeViewMsg{
					ViewNumber: 9,
					From:       1,
				}),
			},
		})
		if ack.Ok || ack.Error != "signature verification failed" {
			t.Fatalf("ack = %+v, want signature rejection", ack)
		}
	})

	t.Run("valid signature", func(t *testing.T) {
		intent := transportpb.IntentToChangeViewToPB(core.IntentToChangeViewMsg{
			ViewNumber: 9,
			From:       1,
		})
		payload, err := marshalDeterministic(intent)
		if err != nil {
			t.Fatalf("marshalDeterministic returned error: %v", err)
		}
		ack := deliver(t, &transportpb.Envelope{
			MsgType:   core.MsgIntentToChangeViewMessage,
			From:      1,
			Signature: ed25519.Sign(privateKey, payload),
			Body: &transportpb.Envelope_IntentToChangeView{
				IntentToChangeView: intent,
			},
		})
		if !ack.Ok || ack.Error != "" {
			t.Fatalf("ack = %+v, want successful delivery", ack)
		}
	})
}

func TestBuildEnvelopeEpochDataForAggregation(t *testing.T) {
	hub := &NodeMessageHub{
		node_ref: &Node{NodeID: 2},
	}
	signature := []byte{1, 2, 3}
	epochData := core.EpochDataForAggregation{
		EpochNumber:  9,
		Throughput:   1234.5,
		ProposalRate: 678.25,
		From:         2,
	}

	env, err := hub.buildEnvelope(core.MsgEpochAggDataMessage, epochData, signature)
	if err != nil {
		t.Fatalf("buildEnvelope returned error: %v", err)
	}
	if env.From != 2 {
		t.Fatalf("env.From = %d, want 2", env.From)
	}
	if string(env.Signature) != string(signature) {
		t.Fatalf("env.Signature = %v, want %v", env.Signature, signature)
	}

	body, ok := env.Body.(*transportpb.Envelope_EpochDataForAggregation)
	if !ok {
		t.Fatalf("env.Body type = %T, want *transportpb.Envelope_EpochDataForAggregation", env.Body)
	}
	data, err := transportpb.EpochDataForAggregationFromPB(body.EpochDataForAggregation)
	if err != nil {
		t.Fatalf("EpochDataForAggregationFromPB returned error: %v", err)
	}
	if data != epochData {
		t.Fatalf("epoch data mismatch: got %+v want %+v", data, epochData)
	}
}

func TestDeliverEpochDataForAggregation(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey returned error: %v", err)
	}

	nodeLog := &logger.Logger{}
	testNode := &Node{
		NodeID: 2,
		log:    nodeLog,
		encryptionKeyStore: &KeyStore{
			publicKeys: map[int]ed25519.PublicKey{1: publicKey},
		},
		epochManager: &EpochManager{currentEpoch: 9},
	}
	testNode.epochAggregator = &EpochAggregator{
		node:        testNode,
		log:         nodeLog,
		epochAggLog: make(map[int64]map[int]EpochAggData),
	}
	hub := &NodeMessageHub{
		node_ref: testNode,
		log:      nodeLog,
	}

	deliver := func(t *testing.T, env *transportpb.Envelope) *transportpb.Ack {
		t.Helper()
		ack, err := hub.Deliver(context.Background(), env)
		if err != nil {
			t.Fatalf("Deliver returned error: %v", err)
		}
		if ack == nil {
			t.Fatal("Deliver returned nil ack")
		}
		return ack
	}

	t.Run("missing body", func(t *testing.T) {
		ack := deliver(t, &transportpb.Envelope{
			MsgType: core.MsgEpochAggDataMessage,
			From:    1,
		})
		if ack.Ok || ack.Error != "missing epoch data for aggregation body" {
			t.Fatalf("ack = %+v, want missing-body rejection", ack)
		}
	})

	t.Run("sender mismatch", func(t *testing.T) {
		ack := deliver(t, &transportpb.Envelope{
			MsgType: core.MsgEpochAggDataMessage,
			From:    1,
			Body: &transportpb.Envelope_EpochDataForAggregation{
				EpochDataForAggregation: transportpb.EpochDataForAggregationToPB(core.EpochDataForAggregation{
					EpochNumber: 9,
					From:        2,
				}),
			},
		})
		if ack.Ok || ack.Error != "epoch data for aggregation sender mismatch" {
			t.Fatalf("ack = %+v, want sender-mismatch rejection", ack)
		}
	})

	t.Run("invalid signature", func(t *testing.T) {
		ack := deliver(t, &transportpb.Envelope{
			MsgType:   core.MsgEpochAggDataMessage,
			From:      1,
			Signature: []byte("invalid"),
			Body: &transportpb.Envelope_EpochDataForAggregation{
				EpochDataForAggregation: transportpb.EpochDataForAggregationToPB(core.EpochDataForAggregation{
					EpochNumber: 9,
					From:        1,
				}),
			},
		})
		if ack.Ok || ack.Error != "signature verification failed" {
			t.Fatalf("ack = %+v, want signature rejection", ack)
		}
	})

	t.Run("valid signature", func(t *testing.T) {
		epochData := transportpb.EpochDataForAggregationToPB(core.EpochDataForAggregation{
			EpochNumber:  9,
			Throughput:   1234.5,
			ProposalRate: 678.25,
			From:         1,
		})
		payload, err := marshalDeterministic(epochData)
		if err != nil {
			t.Fatalf("marshalDeterministic returned error: %v", err)
		}
		ack := deliver(t, &transportpb.Envelope{
			MsgType:   core.MsgEpochAggDataMessage,
			From:      1,
			Signature: ed25519.Sign(privateKey, payload),
			Body: &transportpb.Envelope_EpochDataForAggregation{
				EpochDataForAggregation: epochData,
			},
		})
		if !ack.Ok || ack.Error != "" {
			t.Fatalf("ack = %+v, want successful delivery", ack)
		}

		deadline := time.Now().Add(time.Second)
		for {
			testNode.epochAggregator.mu.Lock()
			received := testNode.epochAggregator.epochAggLog[9][1]
			testNode.epochAggregator.mu.Unlock()
			if received.throughput == 1234.5 && received.proposalRate == 678.25 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("epoch data was not delivered: got %+v", received)
			}
			time.Sleep(time.Millisecond)
		}
	})
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
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey returned error: %v", err)
	}

	nodeLog := &logger.Logger{}
	receiver := &Node{
		NodeID:       2,
		fNodes:       1,
		log:          nodeLog,
		cfg:          &config.Config{NodeNum: 4},
		epochManager: &EpochManager{currentEpoch: 9},
		encryptionKeyStore: &KeyStore{
			publicKeys: map[int]ed25519.PublicKey{1: publicKey},
		},
	}
	receiver.epochAggregator = &EpochAggregator{
		node:        receiver,
		log:         nodeLog,
		epochAggLog: make(map[int64]map[int]EpochAggData),
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

	sendEpochData := func(throughput float64) {
		t.Helper()
		msg := core.EpochDataForAggregation{
			EpochNumber:  9,
			Throughput:   throughput,
			ProposalRate: throughput / 2,
			From:         1,
		}
		pbMsg := transportpb.EpochDataForAggregationToPB(msg)
		payload, marshalErr := marshalDeterministic(pbMsg)
		if marshalErr != nil {
			t.Fatalf("marshalDeterministic returned error: %v", marshalErr)
		}
		senderHub.Send(core.MsgEpochAggDataMessage, receiverAddr, msg, ed25519.Sign(privateKey, payload))
	}

	waitForThroughput := func(want float64) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for {
			receiver.epochAggregator.mu.Lock()
			got := receiver.epochAggregator.epochAggLog[9][1].throughput
			receiver.epochAggregator.mu.Unlock()
			if got == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("streamed epoch throughput = %f, want %f", got, want)
			}
			time.Sleep(time.Millisecond)
		}
	}

	sendEpochData(100)
	waitForThroughput(100)

	senderHub.mu.RLock()
	firstState := senderHub.peerStreams[receiverAddr]
	senderHub.mu.RUnlock()
	if firstState == nil {
		t.Fatal("persistent peer stream was not cached")
	}

	sendEpochData(200)
	waitForThroughput(200)

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
