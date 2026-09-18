package node

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net"
	"os"
	"strings"
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

type eventClientStream struct {
	grpc.ServerStream
	envelopes []*transportpb.Envelope
}

func (s *eventClientStream) Context() context.Context { return context.Background() }

func (s *eventClientStream) Send(*transportpb.Envelope) error { return nil }

func (s *eventClientStream) Recv() (*transportpb.Envelope, error) {
	if len(s.envelopes) == 0 {
		return nil, io.EOF
	}
	env := s.envelopes[0]
	s.envelopes = s.envelopes[1:]
	return env, nil
}

func TestClientStreamEventDispatch(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, dead := range []bool{false, true} {
		n := &Node{
			dead:               dead,
			log:                logger.NewLogger(98765, "node"),
			clientEventMsgChan: make(chan core.EventMsg, 2),
			eventLoopStopCh:    make(chan struct{}),
		}
		hub := &NodeMessageHub{node_ref: n, log: n.log}
		eventType := "live-event"
		if dead {
			eventType = "dead-event"
		}
		stream := &eventClientStream{envelopes: []*transportpb.Envelope{
			{MsgType: core.MsgEventMessage},
			{MsgType: core.MsgEventMessage, Body: &transportpb.Envelope_Request{Request: &transportpb.RequestMessage{}}},
			{MsgType: core.MsgEventMessage, Body: &transportpb.Envelope_Event{Event: &transportpb.EventMsg{EventType: eventType}}},
			{MsgType: core.MsgEventMessage, Body: &transportpb.Envelope_Event{Event: &transportpb.EventMsg{}}},
		}}
		if err := hub.ClientNodeChannel(stream); err != nil {
			t.Fatal(err)
		}
		if hub.clientStream != nil {
			t.Fatal("client stream was not cleared after EOF")
		}
	}
	contents, err := os.ReadFile("logs/node_98765.log")
	if err != nil {
		t.Fatal(err)
	}
	output := string(contents)
	if strings.Count(output, "event message received:") != 2 || !strings.Contains(output, "event message received: live-event") || strings.Contains(output, "dead-event") {
		t.Fatalf("unexpected event handler output: %s", output)
	}
}

func TestDeliverEvent(t *testing.T) {
	t.Run("accepted and enqueued", func(t *testing.T) {
		events := make(chan core.EventMsg, 1)
		n := &Node{
			log:                logger.NewLogger(98765, "node"),
			clientEventMsgChan: events,
			eventLoopStopCh:    make(chan struct{}),
		}
		hub := &NodeMessageHub{node_ref: n, log: n.log}

		ack, err := hub.Deliver(context.Background(), &transportpb.Envelope{
			MsgType: core.MsgEventMessage,
			Body: &transportpb.Envelope_Event{
				Event: &transportpb.EventMsg{EventType: "LeaderStall"},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if ack == nil || !ack.Ok {
			t.Fatalf("ack = %v, want successful acknowledgement", ack)
		}
		select {
		case event := <-events:
			if event.EventType != "LeaderStall" {
				t.Fatalf("event type = %q, want LeaderStall", event.EventType)
			}
		default:
			t.Fatal("event was acknowledged before being enqueued")
		}
	})

	invalid := []struct {
		name string
		node *Node
		env  *transportpb.Envelope
		want string
	}{
		{
			name: "missing body",
			node: &Node{clientEventMsgChan: make(chan core.EventMsg, 1), eventLoopStopCh: make(chan struct{})},
			env:  &transportpb.Envelope{MsgType: core.MsgEventMessage},
			want: "missing event body",
		},
		{
			name: "mismatched body",
			node: &Node{clientEventMsgChan: make(chan core.EventMsg, 1), eventLoopStopCh: make(chan struct{})},
			env: &transportpb.Envelope{
				MsgType: core.MsgEventMessage,
				Body:    &transportpb.Envelope_Request{Request: &transportpb.RequestMessage{}},
			},
			want: "missing event body",
		},
		{
			name: "dead node",
			node: &Node{dead: true, clientEventMsgChan: make(chan core.EventMsg, 1), eventLoopStopCh: make(chan struct{})},
			env: &transportpb.Envelope{
				MsgType: core.MsgEventMessage,
				Body: &transportpb.Envelope_Event{
					Event: &transportpb.EventMsg{EventType: "LeaderStall"},
				},
			},
			want: "node is dead",
		},
	}

	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			tt.node.log = logger.NewLogger(98765, "node")
			hub := &NodeMessageHub{node_ref: tt.node, log: tt.node.log}
			ack, err := hub.Deliver(context.Background(), tt.env)
			if err != nil {
				t.Fatal(err)
			}
			if ack == nil || ack.Ok || !strings.Contains(ack.Error, tt.want) {
				t.Fatalf("ack = %v, want rejection containing %q", ack, tt.want)
			}
			select {
			case event := <-tt.node.clientEventMsgChan:
				t.Fatalf("rejected event was enqueued: %+v", event)
			default:
			}
		})
	}
}

func TestDeliverEventCancellationReleasesBlockedEnqueue(t *testing.T) {
	events := make(chan core.EventMsg, 1)
	events <- core.EventMsg{EventType: "already queued"}
	n := &Node{
		log:                logger.NewLogger(98765, "node"),
		clientEventMsgChan: events,
		eventLoopStopCh:    make(chan struct{}),
	}
	hub := &NodeMessageHub{node_ref: n, log: n.log}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan *transportpb.Ack, 1)

	go func() {
		ack, _ := hub.Deliver(ctx, &transportpb.Envelope{
			MsgType: core.MsgEventMessage,
			Body: &transportpb.Envelope_Event{
				Event: &transportpb.EventMsg{EventType: "LeaderStall"},
			},
		})
		done <- ack
	}()

	cancel()
	select {
	case ack := <-done:
		if ack == nil || ack.Ok || !strings.Contains(ack.Error, context.Canceled.Error()) {
			t.Fatalf("ack = %v, want context-canceled rejection", ack)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled event delivery remained blocked on a full event channel")
	}
}

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
		electionMsgChan:    make(chan ElectionMsg, 2),
		epochMsgChan:       make(chan EpochProtocolMsg, 2),
	}
	receiverHub := &NodeMessageHub{node_ref: receiverNode, log: receiverNode.log}
	senderHub := &NodeMessageHub{node_ref: &Node{NodeID: 1}}
	view := core.ViewID{Generation: 2, Counter: 3}

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
				ViewNumber: view,
				From:       1,
				Action:     core.PerformanceRoundRobin,
			},
			payload: func(env *transportpb.Envelope) proto.Message { return env.GetViewChange() },
			assertSent: func(t *testing.T) {
				delivered := <-receiverNode.viewChangeMsgChan
				msg, ok := delivered.Msg.(core.ViewChangeMsg)
				if !ok || msg.ViewNumber != view || msg.From != 1 || msg.Action != core.PerformanceRoundRobin {
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
				NewViewNumber: view,
				From:          1,
			},
			payload: func(env *transportpb.Envelope) proto.Message { return env.GetNewView() },
			assertSent: func(t *testing.T) {
				delivered := <-receiverNode.newViewMsgChan
				msg, ok := delivered.Msg.(core.NewViewMsg)
				if !ok || msg.NewViewNumber != view || msg.From != 1 {
					t.Fatalf("delivered new-view = %#v", delivered.Msg)
				}
			},
		},
		{
			name:    "request vote",
			msgType: core.MsgRequestVoteMessage,
			msg: core.RequestVoteMsg{
				From:       1,
				ViewNumber: view,
				Seed:       []byte("view-2"),
				DelaySteps: 500,
				Y:          []byte{1, 2, 3},
				VDFProof:   []byte{4, 5, 6},
				VRFProof:   []byte{7, 8, 9},
			},
			payload: func(env *transportpb.Envelope) proto.Message { return env.GetRequestVote() },
			assertSent: func(t *testing.T) {
				delivered := <-receiverNode.electionMsgChan
				msg, ok := delivered.Msg.(core.RequestVoteMsg)
				if !ok || msg.ViewNumber != view || msg.From != 1 {
					t.Fatalf("delivered request-vote = %#v", delivered.Msg)
				}
			},
		},
		{
			name:    "grant vote",
			msgType: core.MsgGrantVoteMessage,
			msg: core.GrantVoteMsg{
				From:       1,
				ViewNumber: view,
			},
			payload: func(env *transportpb.Envelope) proto.Message { return env.GetGrantVote() },
			assertSent: func(t *testing.T) {
				delivered := <-receiverNode.electionMsgChan
				msg, ok := delivered.Msg.(core.GrantVoteMsg)
				if !ok || msg.ViewNumber != view || msg.From != 1 {
					t.Fatalf("delivered grant-vote = %#v", delivered.Msg)
				}
			},
		},
		{
			name:    "epoch data",
			msgType: core.MsgEpochDataMessage,
			msg: core.EpochDataMsg{
				EpochGeneration: 3,
				From:            1,
			},
			payload: func(env *transportpb.Envelope) proto.Message { return env.GetEpochData() },
			assertSent: func(t *testing.T) {
				delivered := <-receiverNode.epochMsgChan
				msg, ok := delivered.Msg.(core.EpochDataMsg)
				if !ok || msg.EpochGeneration != 3 || msg.From != 1 {
					t.Fatalf("delivered epoch-data = %#v", delivered.Msg)
				}
			},
		},
		{
			name:    "epoch aggregate",
			msgType: core.MsgEpochAggregateMessage,
			msg: core.EpochAggregateMsg{
				EpochGeneration: 3,
				From:            1,
				CurrentAction:   core.PerformanceElection,
				EpochData: core.EpochData{
					Throughput:       250.5,
					ProposalInterval: 0.02,
					VCRate:           0.4,
					InactiveNodes:    2,
				},
				EpochDataMsgSigs: []core.EpochDataMsgSig{{
					EpochDataMsg: core.EpochDataMsg{EpochGeneration: 3, From: 1},
					Signature:    []byte{1, 2, 3},
				}},
			},
			payload: func(env *transportpb.Envelope) proto.Message {
				return epochAggregateSignPayload(env.GetEpochAggregate())
			},
			assertSent: func(t *testing.T) {
				delivered := <-receiverNode.epochMsgChan
				msg, ok := delivered.Msg.(core.EpochAggregateMsg)
				if !ok || msg.EpochGeneration != 3 || msg.From != 1 || msg.CurrentAction != core.PerformanceElection || msg.EpochData.VCRate != 0.4 || msg.EpochData.InactiveNodes != 2 || len(msg.EpochDataMsgSigs) != 1 {
					t.Fatalf("delivered epoch-aggregate = %#v", delivered.Msg)
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

func TestDeliverEpochAggregateVerifiesMiniPayload(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	receiverNode := &Node{
		log:                logger.NewLogger(2, "node"),
		encryptionKeyStore: &KeyStore{publicKeys: map[int]ed25519.PublicKey{1: publicKey}},
		epochMsgChan:       make(chan EpochProtocolMsg, 1),
	}
	hub := &NodeMessageHub{node_ref: receiverNode, log: receiverNode.log}
	aggregate := &transportpb.EpochAggregateMsg{
		EpochGeneration: 4,
		From:            1,
		EpochData:       &transportpb.EpochData{Throughput: 100},
		CurrentAction:   transportpb.ActionToPB(core.PerformanceElection),
	}
	payload, err := marshalDeterministic(epochAggregateSignPayload(aggregate))
	if err != nil {
		t.Fatal(err)
	}
	signature := ed25519.Sign(privateKey, payload)

	// Mutating a field from the signed mini after signing must invalidate the
	// signature even though the full message also contains unsigned proofs.
	aggregate.EpochData.Throughput = 200
	ack, err := hub.Deliver(context.Background(), &transportpb.Envelope{
		MsgType:   core.MsgEpochAggregateMessage,
		From:      1,
		Signature: signature,
		Body:      &transportpb.Envelope_EpochAggregate{EpochAggregate: aggregate},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ack.Ok || ack.Error != "signature verification failed" {
		t.Fatalf("ack = %#v, want signature verification failure", ack)
	}
}

func TestDeliverPreprepareVerifiesFullViewID(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	receiverNode := &Node{
		log:                logger.NewLogger(2, "node"),
		encryptionKeyStore: &KeyStore{publicKeys: map[int]ed25519.PublicKey{1: publicKey}},
		consensusMsgChan:   make(chan ConsensusMsg, 1),
	}
	hub := &NodeMessageHub{node_ref: receiverNode, log: receiverNode.log}
	view := core.ViewID{Generation: 3, Counter: 4}
	mutations := map[string]func(*transportpb.ViewID){
		"generation": func(view *transportpb.ViewID) { view.Generation++ },
		"counter":    func(view *transportpb.ViewID) { view.Counter++ },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			preprepare := &transportpb.PreprepareMsg{
				View:            transportpb.ViewIDToPB(view),
				SeqNum:          10,
				DigestClientMsg: make([]byte, 32),
			}
			payload, err := marshalDeterministic(preprepareSignPayload(view, preprepare.SeqNum, preprepare.DigestClientMsg))
			if err != nil {
				t.Fatal(err)
			}
			signature := ed25519.Sign(privateKey, payload)

			mutate(preprepare.View)
			ack, err := hub.Deliver(context.Background(), &transportpb.Envelope{
				MsgType:   core.MsgPreprepareMessage,
				From:      1,
				Signature: signature,
				Body:      &transportpb.Envelope_Preprepare{Preprepare: preprepare},
			})
			if err != nil {
				t.Fatal(err)
			}
			if ack.Ok || ack.Error != "signature verification failed" {
				t.Fatalf("ack = %#v, want signature verification failure", ack)
			}
		})
	}
}

func TestDeliverEpochAggregateVerifiesCurrentAction(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	receiverNode := &Node{
		log:                logger.NewLogger(2, "node"),
		encryptionKeyStore: &KeyStore{publicKeys: map[int]ed25519.PublicKey{1: publicKey}},
		epochMsgChan:       make(chan EpochProtocolMsg, 1),
	}
	hub := &NodeMessageHub{node_ref: receiverNode, log: receiverNode.log}
	aggregate := &transportpb.EpochAggregateMsg{
		EpochGeneration: 4,
		From:            1,
		EpochData:       &transportpb.EpochData{Throughput: 100},
		CurrentAction:   transportpb.ActionToPB(core.PerformanceElection),
	}
	payload, err := marshalDeterministic(epochAggregateSignPayload(aggregate))
	if err != nil {
		t.Fatal(err)
	}
	signature := ed25519.Sign(privateKey, payload)

	aggregate.CurrentAction.Policy = transportpb.Policy_POLICY_ROUND_ROBIN
	ack, err := hub.Deliver(context.Background(), &transportpb.Envelope{
		MsgType:   core.MsgEpochAggregateMessage,
		From:      1,
		Signature: signature,
		Body:      &transportpb.Envelope_EpochAggregate{EpochAggregate: aggregate},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ack.Ok || ack.Error != "signature verification failed" {
		t.Fatalf("ack = %#v, want signature verification failure", ack)
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
		View:        core.ViewID{Generation: 2, Counter: 7},
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
	if data.View != (core.ViewID{Generation: 2, Counter: 7}) {
		t.Fatalf("View = %v, want (2,7)", data.View)
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
