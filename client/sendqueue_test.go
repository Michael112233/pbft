package client

import (
	"context"
	"testing"
	"time"

	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/logger"
	"github.com/michael112233/pbft/transportpb"
)

func newTestSendHub(sendFn func(string, *transportpb.Envelope) error, addrs ...string) *ClientMessageHub {
	hub := &ClientMessageHub{log: &logger.Logger{}, sendFn: sendFn, senders: map[string]*nodeSender{}}
	hub.ctx, hub.cancel = context.WithCancel(context.Background())
	for _, a := range addrs {
		hub.senders[a] = newNodeSender(a)
	}
	return hub
}

func testBatch(n int) core.RequestMessage {
	return core.RequestMessage{Txs: make([]core.ClientMsgSignature, n), MsgType: normalRequestMessageType}
}

func TestSendQueueDropsWhenFull(t *testing.T) {
	hub := newTestSendHub(nil, "A") // no sender loop: nothing drains the queue
	for i := 0; i < sendQueueBatches; i++ {
		if !hub.EnqueueRequest("A", testBatch(200)) {
			t.Fatalf("enqueue %d rejected before the queue was full", i)
		}
	}
	if hub.EnqueueRequest("A", testBatch(200)) {
		t.Fatal("enqueue accepted on a full queue")
	}
	if dropped, _ := hub.SendQueueStats(); dropped != 200 {
		t.Fatalf("dropped=%d, want 200", dropped)
	}
}

func TestSendQueueDiscardQueued(t *testing.T) {
	hub := newTestSendHub(nil, "A")
	hub.EnqueueRequest("A", testBatch(200))
	hub.EnqueueRequest("A", testBatch(50))
	if n := hub.DiscardQueued("A"); n != 250 {
		t.Fatalf("discarded %d, want 250", n)
	}
	if len(hub.senders["A"].ch) != 0 {
		t.Fatal("queue not empty after discard")
	}
	if _, discarded := hub.SendQueueStats(); discarded != 250 {
		t.Fatalf("discarded counter=%d, want 250", discarded)
	}
}

// A node whose stream blocks must not delay sends to another node.
func TestSendQueueBlockedNodeDoesNotBlockOthers(t *testing.T) {
	release := make(chan struct{})
	sentB := make(chan struct{}, 1)
	hub := newTestSendHub(func(addr string, _ *transportpb.Envelope) error {
		if addr == "A" {
			<-release
			return nil
		}
		sentB <- struct{}{}
		return nil
	}, "A", "B")
	defer func() { close(release); hub.cancel(); hub.workersWG.Wait() }()
	for _, s := range hub.senders {
		hub.workersWG.Add(1)
		go hub.senderLoop(s)
	}

	hub.EnqueueRequest("A", testBatch(200)) // A's sender blocks on this one
	start := time.Now()
	if !hub.EnqueueRequest("B", testBatch(200)) {
		t.Fatal("enqueue to B rejected")
	}
	select {
	case <-sentB:
		if d := time.Since(start); d > 100*time.Millisecond {
			t.Fatalf("send to B took %v while A was blocked", d)
		}
	case <-time.After(time.Second):
		t.Fatal("send to B never happened while A was blocked")
	}
}
