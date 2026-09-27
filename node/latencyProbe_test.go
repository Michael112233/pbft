package node

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/logger"
	"github.com/michael112233/pbft/transportpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
)

func startProbeReceiver(t *testing.T) (*Node, *NodeMessageHub) {
	t.Helper()
	nodeLog := &logger.Logger{}
	receiver := &Node{NodeID: 2, log: nodeLog}
	hub := NewNodeMessageHub()
	hub.node_ref = receiver
	hub.log = nodeLog
	hub.streamCtx, hub.streamCancel = context.WithCancel(context.Background())
	hub.listener = bufconn.Listen(1024 * 1024)
	hub.grpcSrv = grpc.NewServer()
	transportpb.RegisterPBFTTransportServer(hub.grpcSrv, hub)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = hub.grpcSrv.Serve(hub.listener)
	}()
	t.Cleanup(func() {
		hub.Close()
		wg.Wait()
	})
	return receiver, hub
}

func newProbeSender(t *testing.T, receiverHub *NodeMessageHub) *Node {
	t.Helper()
	nodeLog := &logger.Logger{}
	sender := &Node{NodeID: 1, log: nodeLog, cfg: &config.Config{}, rttSampleCh: make(chan rttSample, 4)}
	hub := NewNodeMessageHub()
	hub.node_ref = sender
	hub.log = nodeLog
	hub.streamCtx, hub.streamCancel = context.WithCancel(context.Background())
	hub.dialContext = func(context.Context, string) (net.Conn, error) {
		return receiverHub.listener.(*bufconn.Listener).Dial()
	}
	sender.messageHub = hub
	t.Cleanup(hub.Close)

	savedAddrs := config.NodeAddr
	config.NodeAddr = map[int]string{2: "passthrough:///peer-2"}
	t.Cleanup(func() { config.NodeAddr = savedAddrs })
	return sender
}

func TestProbePeerProducesRTTSample(t *testing.T) {
	_, receiverHub := startProbeReceiver(t)
	sender := newProbeSender(t, receiverHub)

	sender.probePeer(2, 2*time.Second)
	select {
	case s := <-sender.rttSampleCh:
		if s.peer != 2 || s.rttMs <= 0 {
			t.Fatalf("sample = %+v", s)
		}
	default:
		t.Fatal("no rtt sample produced")
	}
}

func TestProbeDeadPeerProducesNoSample(t *testing.T) {
	receiver, receiverHub := startProbeReceiver(t)
	receiver.dead.Store(true)
	sender := newProbeSender(t, receiverHub)

	sender.probePeer(2, 500*time.Millisecond)
	select {
	case s := <-sender.rttSampleCh:
		t.Fatalf("dead peer produced sample %+v", s)
	default:
	}
}
