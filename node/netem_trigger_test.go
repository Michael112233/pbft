package node

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/netem"
)

func testNetemRequest() netem.ExecutionEventRequest {
	return netem.ExecutionEventRequest{
		Version: netem.ProtocolVersion,
		Type:    config.NetemExecutionEvent,
		RuleID:  "delay",
		NodeID:  1,
		Seq:     2,
	}
}

func TestSendNetemExecutionEventRoundTrip(t *testing.T) {
	request := testNetemRequest()
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()

	serverErr := make(chan error, 1)
	go func() {
		defer serverConn.Close()
		var got netem.ExecutionEventRequest
		if err := json.NewDecoder(serverConn).Decode(&got); err != nil {
			serverErr <- err
			return
		}
		response := netem.EventResponse{
			Version:  netem.ProtocolVersion,
			RuleID:   got.RuleID,
			NodeID:   got.NodeID,
			Seq:      got.Seq,
			Accepted: true,
			Applied:  true,
		}
		serverErr <- json.NewEncoder(serverConn).Encode(response)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	response, err := exchangeNetemExecutionEvent(ctx, clientConn, request)
	if err != nil {
		t.Fatalf("sendNetemExecutionEvent returned error: %v", err)
	}
	if !response.Accepted || !response.Applied {
		t.Fatalf("response = %#v", response)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("server returned error: %v", err)
	}
}

func TestSendNetemExecutionEventReturnsControllerRejection(t *testing.T) {
	request := testNetemRequest()
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()

	go func() {
		defer serverConn.Close()
		var got netem.ExecutionEventRequest
		_ = json.NewDecoder(serverConn).Decode(&got)
		_ = json.NewEncoder(serverConn).Encode(netem.EventResponse{
			Version: netem.ProtocolVersion,
			RuleID:  got.RuleID,
			NodeID:  got.NodeID,
			Seq:     got.Seq,
			Error:   "event rejected",
		})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	response, err := exchangeNetemExecutionEvent(ctx, clientConn, request)
	if err != nil {
		t.Fatalf("sendNetemExecutionEvent returned transport error: %v", err)
	}
	if response.Accepted || response.Error != "event rejected" {
		t.Fatalf("response = %#v", response)
	}
}

func TestSendNetemExecutionEventMissingControllerReturnsQuickly(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started := time.Now()
	_, err := sendNetemExecutionEvent(ctx, filepath.Join(t.TempDir(), "missing.sock"), testNetemRequest())
	if err == nil {
		t.Fatal("sendNetemExecutionEvent returned nil error for missing controller")
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("missing controller blocked for %s", elapsed)
	}
}

func TestNotifyNetemExecutionEventQueuesMatchingRulesInExecutionOrder(t *testing.T) {
	cfg := &config.Config{
		Netem: config.NetemConfig{
			Enabled:    true,
			SocketPath: "logs/netem.sock",
			Rules: []config.NetemRule{
				{
					ID:     "first",
					Event:  config.NetemEventConfig{Type: config.NetemExecutionEvent, NodeID: 1, Seq: 2},
					Action: config.NetemAction{DelayMs: 100, Lifetime: config.NetemLifetimeUntilNextEvent},
				},
				{
					ID:     "second",
					Event:  config.NetemEventConfig{Type: config.NetemExecutionEvent, NodeID: 1, Seq: 3},
					Action: config.NetemAction{DelayMs: 250, Lifetime: config.NetemLifetimeUntilNextEvent},
				},
			},
		},
	}
	node := &Node{NodeID: 1, cfg: cfg, netemEventChan: make(chan netemEvent, 3)}
	node.notifyNetemExecutionEvent(2)
	node.notifyNetemExecutionEvent(3)

	first := <-node.netemEventChan
	second := <-node.netemEventChan
	if first.request.RuleID != "first" || second.request.RuleID != "second" {
		t.Fatalf("queued rule order = %q, %q", first.request.RuleID, second.request.RuleID)
	}
}
