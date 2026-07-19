package node

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/michael112233/pbft/learningagentpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	learningAgentStartupTimeout = 10 * time.Second
	learningAgentRPCTimeout     = time.Second
	learningAgentRetryInterval  = 250 * time.Millisecond
)

type LearningAgentClient struct {
	nodeID int
	conn   *grpc.ClientConn
	client learningagentpb.LearningAgentClient
}

func NewLearningAgentClient(nodeID int, address string, opts ...grpc.DialOption) (*LearningAgentClient, error) {
	if nodeID < 1 {
		return nil, fmt.Errorf("learning-agent node ID must be at least 1")
	}
	if address == "" {
		return nil, errors.New("learning-agent address is empty")
	}

	dialOptions := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
	dialOptions = append(dialOptions, opts...)
	conn, err := grpc.NewClient(address, dialOptions...)
	if err != nil {
		return nil, fmt.Errorf("create learning-agent client for node %d: %w", nodeID, err)
	}
	return &LearningAgentClient{
		nodeID: nodeID,
		conn:   conn,
		client: learningagentpb.NewLearningAgentClient(conn),
	}, nil
}

func (c *LearningAgentClient) Exchange(ctx context.Context, payload []byte) ([]byte, error) {
	response, err := c.client.Exchange(ctx, &learningagentpb.NodeRequest{
		NodeId:  int32(c.nodeID),
		Payload: payload,
	})
	if err != nil {
		return nil, err
	}
	if response.GetNodeId() != int32(c.nodeID) {
		return nil, fmt.Errorf(
			"learning-agent response node ID = %d, want %d",
			response.GetNodeId(),
			c.nodeID,
		)
	}
	return response.GetPayload(), nil
}

func (c *LearningAgentClient) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (n *Node) ExchangeWithLearningAgent(ctx context.Context, payload []byte) ([]byte, error) {
	if n.learningAgent == nil {
		return nil, errors.New("learning agent is not configured for this node")
	}
	return n.learningAgent.Exchange(ctx, payload)
}

func (n *Node) learningAgentHandshake(ctx context.Context, rpcTimeout, retryInterval time.Duration) error {
	payload := []byte(fmt.Sprintf("pbft-node-%d-startup", n.NodeID))
	var lastErr error

	for {
		callCtx, cancel := context.WithTimeout(ctx, rpcTimeout)
		response, err := n.ExchangeWithLearningAgent(callCtx, payload)
		cancel()
		if err == nil {
			if bytes.Equal(response, payload) {
				return nil
			}
			err = fmt.Errorf("learning-agent startup payload mismatch")
		}
		lastErr = err

		timer := time.NewTimer(retryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf(
				"learning-agent startup handshake for node %d failed: %w",
				n.NodeID,
				lastErr,
			)
		case <-timer.C:
		}
	}
}
