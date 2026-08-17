package node

import (
	"testing"
	"time"

	"github.com/michael112233/pbft/core"
)

func TestNodeEventLoopStopsOnSignal(t *testing.T) {
	n := &Node{
		eventLoopStopCh:                make(chan struct{}),
		eventLoopDoneCh:                make(chan struct{}),
		receiveVerifiedClientRequestCh: make(chan core.ClientMsgSignature),
		pendingRequests:                NewRequestQueue(1),
	}

	n.startEventLoop()
	n.startEventLoop()
	n.ReceiveVerifiedClientRequestCh(core.ClientMsgSignature{})

	select {
	case <-n.eventLoopDoneCh:
		t.Fatal("event loop exited after receiving a client request")
	default:
	}

	stopped := make(chan struct{})
	go func() {
		n.stopEventLoop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("event loop did not stop")
	}

	if n.pendingRequests.Len() != 1 {
		t.Fatalf("pending queue length = %d, want 1", n.pendingRequests.Len())
	}

	// A repeated stop must not panic or block.
	n.stopEventLoop()
}
