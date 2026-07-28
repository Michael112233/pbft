package node

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
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

type NodeLearningAgent interface {
	GetNodeID() int
	HandleDecisionFromLearningAgent(epoch int64, protocol string)
}

type LearningAgentHub struct {
	learningagentpb.UnimplementedLearningAgentNodeServer

	mu           sync.RWMutex
	nodeID       int
	agentAddress string
	dialOptions  []grpc.DialOption
	conn         *grpc.ClientConn
	client       learningagentpb.LearningAgentClient
	closed       bool
	node         NodeLearningAgent
}

func (c *LearningAgentHub) SendDecision(
	_ context.Context,
	request *learningagentpb.LearningDecision,
) (*learningagentpb.DecisionAck, error) {
	if request == nil {
		return &learningagentpb.DecisionAck{
			NodeId:   int32(c.nodeID),
			Accepted: false,
			Error:    "learning decision is missing",
		}, nil
	}

	ack := &learningagentpb.DecisionAck{
		NodeId:     int32(c.nodeID),
		SequenceId: request.GetSequenceId(),
	}

	if request.GetNodeId() != int32(c.nodeID) {
		ack.Error = fmt.Sprintf(
			"learning decision node ID = %d, want %d",
			request.GetNodeId(),
			c.nodeID,
		)
		return ack, nil
	}
	if request.GetNextProtocol() == "" {
		ack.Error = "learning decision next protocol is empty"
		return ack, nil
	}

	ack.Accepted = true
	go c.node.HandleDecisionFromLearningAgent(int64(request.GetSequenceId()), request.GetNextProtocol())
	return ack, nil
}

func NewLearningAgent(node NodeLearningAgent, address string, opts ...grpc.DialOption) (*LearningAgentHub, error) {
	if node == nil {
		return nil, errors.New("node is nil")
	}
	if node.GetNodeID() < 1 {
		return nil, errors.New("learning-agent node ID must be at least 1")
	}
	if address == "" {
		return nil, errors.New("learning-agent address is empty")
	}
	return &LearningAgentHub{
		nodeID:       node.GetNodeID(),
		agentAddress: address,
		dialOptions:  append([]grpc.DialOption(nil), opts...),
		node:         node,
	}, nil
}

func (c *LearningAgentHub) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return errors.New("learning-agent hub is closed")
	}
	if c.client != nil {
		return nil
	}
	dialOptions := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
	dialOptions = append(dialOptions, c.dialOptions...)
	conn, err := grpc.NewClient(c.agentAddress, dialOptions...)
	if err != nil {
		return fmt.Errorf("create learning-agent client for node %d: %w", c.nodeID, err)
	}
	c.conn = conn
	c.client = learningagentpb.NewLearningAgentClient(conn)
	return nil
}

func (c *LearningAgentHub) SendLearningData(ctx context.Context, epoch int64, throughput float64, proposalRate float64) error {
	if epoch < 0 {
		return fmt.Errorf("learning-data epoch must be nonnegative: %d", epoch)
	}
	c.mu.RLock()
	client := c.client
	closed := c.closed
	c.mu.RUnlock()
	if closed {
		return errors.New("learning-agent hub is closed")
	}
	if client == nil {
		return errors.New("learning-agent hub is not started")
	}

	request := &learningagentpb.LearningDecision{
		NodeId:       int32(c.nodeID),
		SequenceId:   uint64(epoch),
		NextProtocol: "", // This field can be set based on your requirements
		Data: map[string]float64{
			"reward":            throughput,
			"proposal_interval": proposalRate,
		},
	}
	response, err := client.SendLearningData(ctx, request)
	if err != nil {
		return err
	}
	if response.GetNodeId() != int32(c.nodeID) {
		return fmt.Errorf(
			"learning-agent response node ID = %d, want %d",
			response.GetNodeId(),
			c.nodeID,
		)
	}
	if !response.GetAccepted() {
		return fmt.Errorf("learning-agent decision not accepted: %s", response.GetError())
	}
	return nil
}

func (c *LearningAgentHub) Exchange(ctx context.Context, payload []byte) ([]byte, error) {
	c.mu.RLock()
	client := c.client
	closed := c.closed
	c.mu.RUnlock()
	if closed {
		return nil, errors.New("learning-agent hub is closed")
	}
	if client == nil {
		return nil, errors.New("learning-agent hub is not started")
	}

	response, err := client.Exchange(ctx, &learningagentpb.NodeRequest{
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

func (c *LearningAgentHub) Close() error {
	if c == nil {
		return nil
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	conn := c.conn
	c.conn = nil
	c.client = nil
	c.mu.Unlock()

	if conn == nil {
		return nil
	}
	return conn.Close()
}

func (n *Node) ExchangeWithLearningAgent(ctx context.Context, payload []byte) ([]byte, error) {
	if n.learningAgent == nil {
		return nil, errors.New("learning agent is not configured for this node")
	}
	return n.learningAgent.Exchange(ctx, payload)
}
func (n *Node) SendLearningDataToAgent(epoch int64, throughput float64, proposalRate float64) {
	if n.learningAgent == nil {
		n.log.Error("learning agent is not configured for this node")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), learningAgentRPCTimeout)
	defer cancel()
	err := n.learningAgent.SendLearningData(ctx, epoch, throughput, proposalRate)
	if err != nil {
		n.log.Error("Failed to send learning data to agent: %v", err)
	}
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
