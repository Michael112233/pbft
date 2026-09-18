package client

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/logger"
	"github.com/michael112233/pbft/transportpb"
	"google.golang.org/grpc"
)

type stubPBFTTransportClient struct {
	deliver func(context.Context, *transportpb.Envelope) (*transportpb.Ack, error)
}

func (c *stubPBFTTransportClient) Deliver(ctx context.Context, env *transportpb.Envelope, _ ...grpc.CallOption) (*transportpb.Ack, error) {
	return c.deliver(ctx, env)
}

func (*stubPBFTTransportClient) ClientNodeChannel(context.Context, ...grpc.CallOption) (transportpb.PBFTTransport_ClientNodeChannelClient, error) {
	return nil, errors.New("unexpected ClientNodeChannel call")
}

type recordingClientStream struct {
	grpc.ClientStream
	sent chan *transportpb.Envelope
}

func (s *recordingClientStream) Send(env *transportpb.Envelope) error {
	s.sent <- env
	return nil
}

func (*recordingClientStream) Recv() (*transportpb.Envelope, error) {
	return nil, io.EOF
}

func newRoutingTestHub(addr string, deliver func(context.Context, *transportpb.Envelope) (*transportpb.Ack, error)) (*ClientMessageHub, *nodeStreamState, <-chan *transportpb.Envelope) {
	streamSends := make(chan *transportpb.Envelope, 1)
	state := &nodeStreamState{
		client: &stubPBFTTransportClient{deliver: deliver},
		stream: &recordingClientStream{sent: streamSends},
	}
	return &ClientMessageHub{
		log:     logger.NewLogger(0, "client"),
		streams: map[string]*nodeStreamState{addr: state},
		ctx:     context.Background(),
	}, state, streamSends
}

func TestBuildEventEnvelope(t *testing.T) {
	hub := &ClientMessageHub{}
	for _, eventType := range []string{"test-event", ""} {
		env, err := hub.buildEnvelope(core.MsgEventMessage, core.EventMsg{EventType: eventType})
		if err != nil {
			t.Fatal(err)
		}
		if env.MsgType != core.MsgEventMessage || env.GetEvent() == nil || env.GetEvent().EventType != eventType {
			t.Fatalf("unexpected event envelope: %v", env)
		}
	}
	if _, err := hub.buildEnvelope(core.MsgEventMessage, core.RequestMessage{}); err == nil {
		t.Fatal("expected incorrect event payload to be rejected")
	}
}

func TestSendRoutesEventUnaryAndRequestStream(t *testing.T) {
	const addr = "node-1"
	unarySends := make(chan *transportpb.Envelope, 1)
	hub, _, streamSends := newRoutingTestHub(addr, func(_ context.Context, env *transportpb.Envelope) (*transportpb.Ack, error) {
		unarySends <- env
		return &transportpb.Ack{Ok: true}, nil
	})

	hub.Send(core.MsgEventMessage, "client", addr, core.EventMsg{EventType: "LeaderStall"}, nil)
	select {
	case env := <-unarySends:
		if env.GetEvent() == nil || env.GetEvent().EventType != "LeaderStall" {
			t.Fatalf("unexpected unary event envelope: %v", env)
		}
	default:
		t.Fatal("event was not sent through unary Deliver")
	}
	select {
	case env := <-streamSends:
		t.Fatalf("event was also sent through the stream: %v", env)
	default:
	}

	hub.Send(core.MsgRequestMessage, "client", addr, core.RequestMessage{MsgType: "RequestMessage"}, nil)
	select {
	case env := <-streamSends:
		if env.GetRequest() == nil {
			t.Fatalf("unexpected streamed request envelope: %v", env)
		}
	default:
		t.Fatal("request was not sent through ClientNodeChannel")
	}
	select {
	case env := <-unarySends:
		t.Fatalf("request was also sent through unary Deliver: %v", env)
	default:
	}
}

func TestEventUnarySendBypassesStreamSendMutex(t *testing.T) {
	const addr = "node-1"
	hub, state, _ := newRoutingTestHub(addr, func(context.Context, *transportpb.Envelope) (*transportpb.Ack, error) {
		return &transportpb.Ack{Ok: true}, nil
	})

	state.sendMu.Lock()
	defer state.sendMu.Unlock()

	done := make(chan struct{})
	go hub.Send(core.MsgEventMessage, "client", addr, core.EventMsg{EventType: "LeaderStall"}, func(...interface{}) {
		close(done)
	})

	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("unary event send blocked behind the stream send mutex")
	}
}

func TestSendUnaryToNodeFailures(t *testing.T) {
	const addr = "node-1"
	env := &transportpb.Envelope{MsgType: core.MsgEventMessage}

	t.Run("connection unavailable", func(t *testing.T) {
		hub := &ClientMessageHub{streams: make(map[string]*nodeStreamState), ctx: context.Background()}
		if err := hub.sendUnaryToNode(addr, env); err == nil || !strings.Contains(err.Error(), "connection not ready") {
			t.Fatalf("error = %v, want connection-not-ready error", err)
		}
	})

	tests := []struct {
		name    string
		deliver func(context.Context, *transportpb.Envelope) (*transportpb.Ack, error)
		want    string
	}{
		{
			name: "rpc error",
			deliver: func(context.Context, *transportpb.Envelope) (*transportpb.Ack, error) {
				return nil, errors.New("transport failed")
			},
			want: "transport failed",
		},
		{
			name: "nil acknowledgement",
			deliver: func(context.Context, *transportpb.Envelope) (*transportpb.Ack, error) {
				return nil, nil
			},
			want: "nil acknowledgement",
		},
		{
			name: "rejected acknowledgement",
			deliver: func(context.Context, *transportpb.Envelope) (*transportpb.Ack, error) {
				return &transportpb.Ack{Ok: false, Error: "node is dead"}, nil
			},
			want: "node is dead",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hub, _, _ := newRoutingTestHub(addr, tt.deliver)
			if err := hub.sendUnaryToNode(addr, env); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want error containing %q", err, tt.want)
			}
		})
	}
}

func TestSendUnaryToNodeDeadline(t *testing.T) {
	const addr = "node-1"
	hub, _, _ := newRoutingTestHub(addr, func(ctx context.Context, _ *transportpb.Envelope) (*transportpb.Ack, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})

	start := time.Now()
	err := hub.sendUnaryToNode(addr, &transportpb.Envelope{MsgType: core.MsgEventMessage})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(start); elapsed < controlRPCTimeout-100*time.Millisecond || elapsed > controlRPCTimeout+time.Second {
		t.Fatalf("unary timeout elapsed = %s, want approximately %s", elapsed, controlRPCTimeout)
	}
}

func TestBuildRequestEnvelopePreservesRequestMsgType(t *testing.T) {
	hub := &ClientMessageHub{}
	in := core.RequestMessage{
		MsgType: "RetryRequestMessage",
		Txs: []core.ClientMsgSignature{
			{Data: core.ClientMsg{Id: 7, ClientName: "client-a"}, Signature: []byte{1, 2, 3}},
		},
	}

	env, err := hub.buildEnvelope(core.MsgRequestMessage, in)
	if err != nil {
		t.Fatalf("buildEnvelope returned error: %v", err)
	}
	if env.MsgType != core.MsgRequestMessage {
		t.Fatalf("envelope MsgType = %q, want %q", env.MsgType, core.MsgRequestMessage)
	}

	out, err := transportpb.RequestFromPB(env.GetRequest())
	if err != nil {
		t.Fatalf("RequestFromPB returned error: %v", err)
	}
	if out.MsgType != in.MsgType {
		t.Fatalf("request MsgType = %q, want %q", out.MsgType, in.MsgType)
	}
	if len(out.Txs) != 1 || out.Txs[0].Data.Id != 7 {
		t.Fatalf("request transactions = %+v, want one transaction with ID 7", out.Txs)
	}
}

func TestHandleIncomingEnvelopeDispatchesLeaderUpdate(t *testing.T) {
	oldNodeAddr := config.NodeAddr
	config.NodeAddr = map[int]string{
		2: "localhost:28200",
	}
	defer func() {
		config.NodeAddr = oldNodeAddr
	}()

	client := &Client{
		log:             logger.NewLogger(0, "client"),
		fNodes:          1,
		newLeaderQuorum: make(map[LeaderUpdate]int),
	}
	hub := &ClientMessageHub{
		client_ref: client,
		log:        client.log,
	}

	for range 2 {
		hub.handleIncomingEnvelope("localhost:28200", &transportpb.Envelope{
			MsgType: core.MsgLeaderIdUpdateMessage,
			Body: &transportpb.Envelope_LeaderIdUpdate{
				LeaderIdUpdate: transportpb.LeaderIdUpdateToPB(core.LeaderIdUpdate{
					From:        "localhost:28200",
					To:          "localhost:20000",
					NewLeaderId: 2,
					View:        core.ViewID{Generation: 1, Counter: 1},
				}),
			},
		})
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		client.leaderMu.RLock()
		leaderAddr := client.leaderAddr
		client.leaderMu.RUnlock()
		if leaderAddr == "localhost:28200" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	client.leaderMu.RLock()
	leaderAddr := client.leaderAddr
	client.leaderMu.RUnlock()
	t.Fatalf("leader address was not updated: got %q want %q", leaderAddr, "localhost:28200")
}

func TestHandleIncomingEnvelopeDispatchesVCRunningStatus(t *testing.T) {
	client := &Client{
		log:       logger.NewLogger(0, "client"),
		vcrunChan: make(chan core.VCRunningStatus, 1),
	}
	hub := &ClientMessageHub{
		client_ref: client,
		log:        client.log,
	}

	in := core.VCRunningStatus{
		VCRunning: true,
		Txs: []core.ClientMsgSignature{
			{Data: core.ClientMsg{Id: 1, ClientName: "client-a"}, Signature: []byte{1}},
			{Data: core.ClientMsg{Id: 2, ClientName: "client-a"}, Signature: []byte{2}},
		},
	}
	hub.handleIncomingEnvelope("localhost:28100", &transportpb.Envelope{
		MsgType: core.MsgVCRunningStatusMessage,
		Body: &transportpb.Envelope_VcRunningStatus{
			VcRunningStatus: transportpb.VCRunningStatusToPB(in),
		},
	})

	select {
	case got := <-client.vcrunChan:
		if !got.VCRunning {
			t.Fatal("VCRunning = false, want true")
		}
		if len(got.Txs) != 2 {
			t.Fatalf("len(Txs) = %d, want 2", len(got.Txs))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for VCRunningStatus dispatch")
	}
}

func TestInjectArtificialLatencyForFarNodeRequest(t *testing.T) {
	oldNodeAddr := config.NodeAddr
	config.NodeAddr = map[int]string{
		4: "localhost:28400",
	}
	defer func() {
		config.NodeAddr = oldNodeAddr
	}()

	client := &Client{
		log:    logger.NewLogger(0, "client"),
		config: &config.Config{FarNodeID: 4, FarNodeDelayMs: 20},
	}
	hub := &ClientMessageHub{
		client_ref: client,
		log:        client.log,
	}

	start := time.Now()
	hub.injectArtificialLatency(core.MsgRequestMessage, "localhost:20000", "localhost:28400")
	if elapsed := time.Since(start); elapsed < 15*time.Millisecond {
		t.Fatalf("injectArtificialLatency() elapsed = %s, want at least %s", elapsed, 15*time.Millisecond)
	}
}
