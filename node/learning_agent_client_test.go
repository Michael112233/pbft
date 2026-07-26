package node

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/michael112233/pbft/learningagentpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type testLearningAgentServer struct {
	learningagentpb.UnimplementedLearningAgentServer
	exchange func(context.Context, *learningagentpb.NodeRequest) (*learningagentpb.NodeResponse, error)
}

func (s *testLearningAgentServer) Exchange(
	ctx context.Context,
	request *learningagentpb.NodeRequest,
) (*learningagentpb.NodeResponse, error) {
	return s.exchange(ctx, request)
}

func newTestLearningAgentClient(
	t *testing.T,
	nodeID int,
	exchange func(context.Context, *learningagentpb.NodeRequest) (*learningagentpb.NodeResponse, error),
) *LearningAgentHub {
	t.Helper()

	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	learningagentpb.RegisterLearningAgentServer(server, &testLearningAgentServer{exchange: exchange})
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(server.Stop)
	t.Cleanup(func() {
		_ = listener.Close()
	})

	client, err := NewLearningAgent(
		&Node{NodeID: nodeID},
		"passthrough:///learning-agent-test",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
	)
	if err != nil {
		t.Fatalf("NewLearningAgent() error = %v", err)
	}
	if err := client.Start(); err != nil {
		t.Fatalf("LearningAgentHub.Start() error = %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
	})
	return client
}

func echoLearningAgent(
	_ context.Context,
	request *learningagentpb.NodeRequest,
) (*learningagentpb.NodeResponse, error) {
	return &learningagentpb.NodeResponse{
		NodeId:  request.GetNodeId(),
		Payload: request.GetPayload(),
	}, nil
}

func TestLearningAgentClientExchange(t *testing.T) {
	client := newTestLearningAgentClient(t, 2, echoLearningAgent)
	payload := []byte{0, 1, 2, 255}

	response, err := client.Exchange(context.Background(), payload)
	if err != nil {
		t.Fatalf("Exchange() error = %v", err)
	}
	if string(response) != string(payload) {
		t.Fatalf("Exchange() payload = %v, want %v", response, payload)
	}
}

func TestLearningAgentHubExchangeBeforeStart(t *testing.T) {
	hub, err := NewLearningAgent(&Node{NodeID: 1}, "passthrough:///learning-agent-test")
	if err != nil {
		t.Fatalf("NewLearningAgent() error = %v", err)
	}
	t.Cleanup(func() {
		_ = hub.Close()
	})

	_, err = hub.Exchange(context.Background(), []byte("payload"))
	if err == nil || !strings.Contains(err.Error(), "not started") {
		t.Fatalf("Exchange() error = %v, want not-started error", err)
	}
}

func TestLearningAgentHubStartIsIdempotent(t *testing.T) {
	hub := newTestLearningAgentClient(t, 1, echoLearningAgent)
	firstConn := hub.conn

	if err := hub.Start(); err != nil {
		t.Fatalf("second Start() error = %v", err)
	}
	if hub.conn != firstConn {
		t.Fatal("second Start() replaced the existing connection")
	}
}

func TestNewLearningAgentValidatesConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		node    *Node
		address string
	}{
		{name: "nil node", address: "passthrough:///learning-agent-test"},
		{name: "invalid node ID", node: &Node{NodeID: 0}, address: "passthrough:///learning-agent-test"},
		{name: "empty address", node: &Node{NodeID: 1}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewLearningAgent(test.node, test.address); err == nil {
				t.Fatal("NewLearningAgent() error = nil, want validation error")
			}
		})
	}
}

func TestLearningAgentClientRejectsWrongResponseNode(t *testing.T) {
	client := newTestLearningAgentClient(
		t,
		2,
		func(_ context.Context, request *learningagentpb.NodeRequest) (*learningagentpb.NodeResponse, error) {
			return &learningagentpb.NodeResponse{
				NodeId:  request.GetNodeId() + 1,
				Payload: request.GetPayload(),
			}, nil
		},
	)

	_, err := client.Exchange(context.Background(), []byte("payload"))
	if err == nil || !strings.Contains(err.Error(), "response node ID") {
		t.Fatalf("Exchange() error = %v, want response node ID error", err)
	}
}

func TestLearningAgentHandshakeRetriesThenSucceeds(t *testing.T) {
	var attempts atomic.Int32
	client := newTestLearningAgentClient(
		t,
		3,
		func(_ context.Context, request *learningagentpb.NodeRequest) (*learningagentpb.NodeResponse, error) {
			if attempts.Add(1) < 3 {
				return nil, status.Error(codes.Unavailable, "not ready")
			}
			return echoLearningAgent(context.Background(), request)
		},
	)
	node := &Node{NodeID: 3, learningAgent: client}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := node.learningAgentHandshake(ctx, 50*time.Millisecond, time.Millisecond); err != nil {
		t.Fatalf("learningAgentHandshake() error = %v", err)
	}
	if got := attempts.Load(); got != 3 {
		t.Fatalf("handshake attempts = %d, want 3", got)
	}
}

func TestLearningAgentHandshakeRejectsBadEchoUntilDeadline(t *testing.T) {
	client := newTestLearningAgentClient(
		t,
		1,
		func(_ context.Context, request *learningagentpb.NodeRequest) (*learningagentpb.NodeResponse, error) {
			return &learningagentpb.NodeResponse{
				NodeId:  request.GetNodeId(),
				Payload: []byte("not-the-request"),
			}, nil
		},
	)
	node := &Node{NodeID: 1, learningAgent: client}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()

	err := node.learningAgentHandshake(ctx, 10*time.Millisecond, 5*time.Millisecond)
	if err == nil {
		t.Fatal("learningAgentHandshake() error = nil, want bad echo to prevent startup")
	}
}

func TestLearningAgentHandshakeHonorsRPCTimeout(t *testing.T) {
	client := newTestLearningAgentClient(
		t,
		1,
		func(ctx context.Context, _ *learningagentpb.NodeRequest) (*learningagentpb.NodeResponse, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	)
	node := &Node{NodeID: 1, learningAgent: client}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Millisecond)
	defer cancel()

	started := time.Now()
	if err := node.learningAgentHandshake(ctx, 10*time.Millisecond, 2*time.Millisecond); err == nil {
		t.Fatal("learningAgentHandshake() error = nil, want timeout")
	}
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("learningAgentHandshake() took %v, want bounded timeout", elapsed)
	}
}

func TestNodeStartFailsWhenLearningAgentIsUnavailable(t *testing.T) {
	client := newTestLearningAgentClient(
		t,
		1,
		func(_ context.Context, _ *learningagentpb.NodeRequest) (*learningagentpb.NodeResponse, error) {
			return nil, status.Error(codes.Unavailable, "offline")
		},
	)
	node := &Node{NodeID: 1, learningAgent: client}

	oldStartupTimeout := learningAgentStartupTimeout
	oldRPCTimeout := learningAgentRPCTimeout
	oldRetryInterval := learningAgentRetryInterval
	learningAgentStartupTimeout = 40 * time.Millisecond
	learningAgentRPCTimeout = 10 * time.Millisecond
	learningAgentRetryInterval = 5 * time.Millisecond
	defer func() {
		learningAgentStartupTimeout = oldStartupTimeout
		learningAgentRPCTimeout = oldRPCTimeout
		learningAgentRetryInterval = oldRetryInterval
	}()

	if err := node.Start(); err == nil {
		t.Fatal("Start() error = nil, want unavailable learning agent to fail startup")
	}
}

func TestLearningAgentClientCloseStopsFurtherExchanges(t *testing.T) {
	client := newTestLearningAgentClient(t, 1, echoLearningAgent)
	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := client.Exchange(ctx, []byte("after-close")); err == nil {
		t.Fatal("Exchange() after Close() error = nil, want an error")
	}
	if err := client.Start(); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("Start() after Close() error = %v, want closed error", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func TestLearningAgentHubSendDecision(t *testing.T) {
	hub, err := NewLearningAgent(&Node{NodeID: 2}, "passthrough:///learning-agent-test")
	if err != nil {
		t.Fatalf("NewLearningAgent() error = %v", err)
	}
	t.Cleanup(func() {
		_ = hub.Close()
	})

	tests := []struct {
		name         string
		request      *learningagentpb.LearningDecision
		wantSequence uint64
		wantAccepted bool
		wantError    bool
	}{
		{
			name: "valid",
			request: &learningagentpb.LearningDecision{
				NodeId:       2,
				SequenceId:   7,
				NextProtocol: "pbft",
			},
			wantSequence: 7,
			wantAccepted: true,
		},
		{
			name:      "missing request",
			request:   nil,
			wantError: true,
		},
		{
			name: "wrong node",
			request: &learningagentpb.LearningDecision{
				NodeId:       3,
				SequenceId:   8,
				NextProtocol: "pbft",
			},
			wantSequence: 8,
			wantError:    true,
		},
		{
			name: "empty protocol",
			request: &learningagentpb.LearningDecision{
				NodeId:     2,
				SequenceId: 9,
			},
			wantSequence: 9,
			wantError:    true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ack, err := hub.SendDecision(context.Background(), test.request)
			if err != nil {
				t.Fatalf("SendDecision() error = %v", err)
			}
			if ack.GetNodeId() != 2 {
				t.Fatalf("SendDecision() node ID = %d, want 2", ack.GetNodeId())
			}
			if ack.GetSequenceId() != test.wantSequence {
				t.Fatalf(
					"SendDecision() sequence ID = %d, want %d",
					ack.GetSequenceId(),
					test.wantSequence,
				)
			}
			if ack.GetAccepted() != test.wantAccepted {
				t.Fatalf(
					"SendDecision() accepted = %t, want %t",
					ack.GetAccepted(),
					test.wantAccepted,
				)
			}
			if gotError := ack.GetError() != ""; gotError != test.wantError {
				t.Fatalf(
					"SendDecision() error field = %q, wantError=%t",
					ack.GetError(),
					test.wantError,
				)
			}
		})
	}
}

func TestLearningAgentHubSendDecisionOverGRPC(t *testing.T) {
	hub, err := NewLearningAgent(&Node{NodeID: 4}, "passthrough:///learning-agent-test")
	if err != nil {
		t.Fatalf("NewLearningAgent() error = %v", err)
	}
	t.Cleanup(func() {
		_ = hub.Close()
	})

	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	learningagentpb.RegisterLearningAgentNodeServer(server, hub)
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(server.Stop)
	t.Cleanup(func() {
		_ = listener.Close()
	})

	conn, err := grpc.NewClient(
		"passthrough:///learning-agent-node-test",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient() error = %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ack, err := learningagentpb.NewLearningAgentNodeClient(conn).SendDecision(
		ctx,
		&learningagentpb.LearningDecision{
			NodeId:       4,
			SequenceId:   11,
			NextProtocol: "hotstuff",
		},
	)
	if err != nil {
		t.Fatalf("SendDecision() RPC error = %v", err)
	}
	if ack.GetNodeId() != 4 || ack.GetSequenceId() != 11 || !ack.GetAccepted() || ack.GetError() != "" {
		t.Fatalf("SendDecision() acknowledgement = %+v, want accepted node 4 sequence 11", ack)
	}
}

func TestLearningAgentPythonIntegration(t *testing.T) {
	if os.Getenv("PBFT_LEARNING_AGENT_INTEGRATION") != "1" {
		t.Skip("set PBFT_LEARNING_AGENT_INTEGRATION=1 with four local Python servers running")
	}

	for nodeID := 1; nodeID <= 4; nodeID++ {
		nodeID := nodeID
		t.Run(fmt.Sprintf("node-%d", nodeID), func(t *testing.T) {
			client, err := NewLearningAgent(
				&Node{NodeID: nodeID},
				fmt.Sprintf("127.0.0.1:%d", 29000+nodeID),
			)
			if err != nil {
				t.Fatalf("NewLearningAgent() error = %v", err)
			}
			if err := client.Start(); err != nil {
				t.Fatalf("LearningAgentHub.Start() error = %v", err)
			}
			defer client.Close()

			payload := []byte(fmt.Sprintf("pbft-node-%d-startup", nodeID))
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			response, err := client.Exchange(ctx, payload)
			if err != nil {
				t.Fatalf("Exchange() error = %v", err)
			}
			if string(response) != string(payload) {
				t.Fatalf("Exchange() payload = %q, want %q", response, payload)
			}
		})
	}
}
