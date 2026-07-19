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
) *LearningAgentClient {
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

	client, err := NewLearningAgentClient(
		nodeID,
		"passthrough:///learning-agent-test",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
	)
	if err != nil {
		t.Fatalf("NewLearningAgentClient() error = %v", err)
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
}

func TestLearningAgentPythonIntegration(t *testing.T) {
	if os.Getenv("PBFT_LEARNING_AGENT_INTEGRATION") != "1" {
		t.Skip("set PBFT_LEARNING_AGENT_INTEGRATION=1 with four local Python servers running")
	}

	for nodeID := 1; nodeID <= 4; nodeID++ {
		nodeID := nodeID
		t.Run(fmt.Sprintf("node-%d", nodeID), func(t *testing.T) {
			client, err := NewLearningAgentClient(nodeID, fmt.Sprintf("127.0.0.1:%d", 29000+nodeID))
			if err != nil {
				t.Fatalf("NewLearningAgentClient() error = %v", err)
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
