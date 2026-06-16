package node

import (
	"testing"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
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

func TestMessagePriorityClassification(t *testing.T) {
	tests := []struct {
		msgType  string
		priority hubPriority
		lane     outboundLane
	}{
		{core.MsgNewViewMessage, priorityCritical, outboundLaneControl},
		{core.MsgViewChangeMessage, priorityCritical, outboundLaneControl},
		{core.MsgPrepareMessage, priorityHigh, outboundLaneControl},
		{core.MsgCommitMessage, priorityHigh, outboundLaneControl},
		{core.MsgCheckpointMessage, priorityMedium, outboundLaneControl},
		{core.MsgRequestStateTransfer, priorityMedium, outboundLaneControl},
		{core.MsgStateTransfer, priorityMedium, outboundLaneControl},
		{core.MsgPreprepareMessage, priorityLow, outboundLaneData},
		{core.MsgRequestMessage, priorityLow, outboundLaneData},
	}

	for _, tt := range tests {
		t.Run(tt.msgType, func(t *testing.T) {
			priority := messagePriority(tt.msgType)
			if priority != tt.priority {
				t.Fatalf("messagePriority(%s) = %v, want %v", tt.msgType, priority, tt.priority)
			}
			if lane := outboundLaneForPriority(priority); lane != tt.lane {
				t.Fatalf("outboundLaneForPriority(%v) = %v, want %v", priority, lane, tt.lane)
			}
		})
	}
}

func TestPriorityQueueDequeuesCriticalBeforeLow(t *testing.T) {
	q := newPriorityJobQueue([priorityCount]int{
		priorityCritical: 10,
		priorityHigh:     10,
		priorityMedium:   10,
		priorityLow:      10,
	})

	if !q.enqueue(outboundJob{msgType: core.MsgPreprepareMessage}, priorityLow, nil, "test", core.MsgPreprepareMessage) {
		t.Fatal("enqueue low returned false")
	}
	if !q.enqueue(outboundJob{msgType: core.MsgPreprepareMessage}, priorityLow, nil, "test", core.MsgPreprepareMessage) {
		t.Fatal("enqueue low returned false")
	}
	if !q.enqueue(outboundJob{msgType: core.MsgNewViewMessage}, priorityCritical, nil, "test", core.MsgNewViewMessage) {
		t.Fatal("enqueue critical returned false")
	}

	raw, priority, ok := q.dequeue()
	if !ok {
		t.Fatal("dequeue returned closed")
	}
	job := raw.(outboundJob)
	if priority != priorityCritical || job.msgType != core.MsgNewViewMessage {
		t.Fatalf("first dequeue = (%s, %v), want (%s, %v)", job.msgType, priority, core.MsgNewViewMessage, priorityCritical)
	}
}

func TestPriorityQueueWeightedOrderDoesNotStarveLow(t *testing.T) {
	q := newPriorityJobQueue([priorityCount]int{
		priorityCritical: 10,
		priorityHigh:     32,
		priorityMedium:   10,
		priorityLow:      10,
	})

	for i := 0; i < 20; i++ {
		if !q.enqueue(outboundJob{msgType: core.MsgPrepareMessage}, priorityHigh, nil, "test", core.MsgPrepareMessage) {
			t.Fatal("enqueue high returned false")
		}
	}
	if !q.enqueue(outboundJob{msgType: core.MsgPreprepareMessage}, priorityLow, nil, "test", core.MsgPreprepareMessage) {
		t.Fatal("enqueue low returned false")
	}

	lowSeen := false
	for i := 0; i < 9; i++ {
		raw, _, ok := q.dequeue()
		if !ok {
			t.Fatal("dequeue returned closed")
		}
		if raw.(outboundJob).msgType == core.MsgPreprepareMessage {
			lowSeen = true
			break
		}
	}
	if !lowSeen {
		t.Fatal("low-priority job was not dequeued within the weighted high:low window")
	}
}
