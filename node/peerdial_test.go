package node

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/logger"
	"github.com/michael112233/pbft/transportpb"
	"google.golang.org/grpc"
)

// A peer server that only accepts streams, enough for getOrCreatePeerStream.
func startDialTestPeer(t *testing.T, lis net.Listener) {
	t.Helper()
	peer := &Node{NodeID: 99, log: logger.NewLogger(99, "node")}
	srv := grpc.NewServer()
	transportpb.RegisterPBFTTransportServer(srv, &NodeMessageHub{node_ref: peer, log: peer.log})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
}

// dialPeersAtStart opens a stream to every peer, including one that starts
// listening only after the first attempts failed, and to no one else.
func TestDialPeersAtStartRetriesUntilEveryPeerIsUp(t *testing.T) {
	early, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	startDialTestPeer(t, early)
	// Reserve an address for the late peer, then free it so the first dials fail.
	late, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	lateAddr := late.Addr().String()
	late.Close()

	old := config.NodeAddr
	config.NodeAddr = map[int]string{1: "127.0.0.1:0", 2: early.Addr().String(), 3: lateAddr}
	t.Cleanup(func() { config.NodeAddr = old })

	n := &Node{NodeID: 1, log: logger.NewLogger(1, "node"), eventLoopStopCh: make(chan struct{})}
	hub := &NodeMessageHub{node_ref: n, log: n.log, peerStreams: make(map[string]*peerStreamState)}
	hub.streamCtx, hub.streamCancel = context.WithCancel(context.Background())
	t.Cleanup(hub.Close)
	n.messageHub = hub

	done := make(chan struct{})
	go func() { n.dialPeersAtStart(); close(done) }()

	time.Sleep(3 * peerDialRetryInterval)
	select {
	case <-done:
		t.Fatal("returned before the late peer was up")
	default:
	}
	lis, err := net.Listen("tcp", lateAddr)
	if err != nil {
		t.Skipf("could not re-listen on %s: %v", lateAddr, err)
	}
	startDialTestPeer(t, lis)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("did not connect to the late peer")
	}
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	if len(hub.peerStreams) != 2 || hub.peerStreams[early.Addr().String()] == nil || hub.peerStreams[lateAddr] == nil {
		t.Fatalf("peer streams = %v, want exactly nodes 2 and 3", hub.peerStreams)
	}
}

// A node that stops before its peers come up stops dialing.
func TestDialPeersAtStartStopsWithTheNode(t *testing.T) {
	gone, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := gone.Addr().String()
	gone.Close()

	old := config.NodeAddr
	config.NodeAddr = map[int]string{1: "127.0.0.1:0", 2: addr}
	t.Cleanup(func() { config.NodeAddr = old })

	n := &Node{NodeID: 1, log: logger.NewLogger(1, "node"), eventLoopStopCh: make(chan struct{})}
	hub := &NodeMessageHub{node_ref: n, log: n.log, peerStreams: make(map[string]*peerStreamState)}
	hub.streamCtx, hub.streamCancel = context.WithCancel(context.Background())
	t.Cleanup(hub.Close)
	n.messageHub = hub

	done := make(chan struct{})
	go func() { n.dialPeersAtStart(); close(done) }()
	time.Sleep(2 * peerDialRetryInterval)
	close(n.eventLoopStopCh)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("still dialing after the node stopped")
	}
}
