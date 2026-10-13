package node

import (
	"context"
	"sync"
	"testing"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/logger"
	"github.com/michael112233/pbft/transportpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// recordingClientStream records what the node sends on a client stream.
type recordingClientStream struct {
	grpc.ServerStream
	ctx  context.Context
	mu   sync.Mutex
	sent []*transportpb.Envelope
}

func (s *recordingClientStream) Context() context.Context { return s.ctx }

func (s *recordingClientStream) Send(env *transportpb.Envelope) error {
	s.mu.Lock()
	s.sent = append(s.sent, env)
	s.mu.Unlock()
	return nil
}

func (s *recordingClientStream) Recv() (*transportpb.Envelope, error) { return nil, context.Canceled }

func (s *recordingClientStream) sentTypes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	types := make([]string, len(s.sent))
	for i, env := range s.sent {
		types[i] = env.MsgType
	}
	return types
}

func newClientStreamTestHub(t *testing.T) *NodeMessageHub {
	t.Helper()
	t.Chdir(t.TempDir())
	n := &Node{NodeID: 1, log: logger.NewLogger(98766, "node")}
	return &NodeMessageHub{node_ref: n, log: n.log}
}

func commitTpsFor(client string, id int64) core.CommitTps {
	return core.CommitTps{ClientMsg: core.ClientMsgReply{Id: id, ClientName: client}}
}

// CommitTps goes only to the client that sent the request; leader updates go to
// every client.
func TestClientStreamsRouteCommitAndBroadcastLeaderUpdate(t *testing.T) {
	hub := newClientStreamTestHub(t)
	c1 := &recordingClientStream{ctx: context.Background()}
	c2 := &recordingClientStream{ctx: context.Background()}
	hub.setClientStream("client1", &clientStreamState{stream: c1})
	hub.setClientStream("client2", &clientStreamState{stream: c2})

	hub.Send(core.MsgCommitTpsMessage, "", commitTpsFor("client2", 7), nil)
	if got := c1.sentTypes(); len(got) != 0 {
		t.Fatalf("client1 got %v, want nothing", got)
	}
	if got := c2.sentTypes(); len(got) != 1 || got[0] != core.MsgCommitTpsMessage {
		t.Fatalf("client2 got %v, want one CommitTps", got)
	}

	hub.Send(core.MsgLeaderIdUpdateMessage, "", core.LeaderIdUpdate{NewLeaderId: 2, View: core.ViewID{Generation: 1, Counter: 2}}, nil)
	for name, s := range map[string]*recordingClientStream{"client1": c1, "client2": c2} {
		got := s.sentTypes()
		if len(got) == 0 || got[len(got)-1] != core.MsgLeaderIdUpdateMessage {
			t.Fatalf("%s got %v, want a LeaderIdUpdate last", name, got)
		}
	}

	// a reply for a client with no stream reaches nobody
	hub.Send(core.MsgCommitTpsMessage, "", commitTpsFor("client3", 1), nil)
	if len(c1.sentTypes()) != 1 || len(c2.sentTypes()) != 2 {
		t.Fatalf("CommitTps for an unconnected client was delivered: client1 %v client2 %v", c1.sentTypes(), c2.sentTypes())
	}
}

// A reconnect replaces only that client's stream, and clearing a stale stream
// leaves the newer one in place.
func TestClientStreamReconnectReplacesOnlyItsOwnEntry(t *testing.T) {
	hub := newClientStreamTestHub(t)
	old1 := &clientStreamState{stream: &recordingClientStream{ctx: context.Background()}}
	c2 := &clientStreamState{stream: &recordingClientStream{ctx: context.Background()}}
	new1 := &clientStreamState{stream: &recordingClientStream{ctx: context.Background()}}

	hub.setClientStream("client1", old1)
	hub.setClientStream("client2", c2)
	hub.setClientStream("client1", new1)
	hub.clearClientStream("client1", old1)

	if hub.clientStreams["client1"] != new1 || hub.clientStreams["client2"] != c2 {
		t.Fatalf("streams after reconnect = %v, want client1=new client2 unchanged", hub.clientStreams)
	}
}

// A stream that announces no name belongs to the legacy single client; one that
// does is registered under its name.
func TestClientNameFromStreamMetadata(t *testing.T) {
	if got := clientNameFromContext(context.Background()); got != config.LegacyClientName {
		t.Fatalf("no metadata: name = %q, want %q", got, config.LegacyClientName)
	}
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(transportpb.ClientNameMetadataKey, "client4"))
	if got := clientNameFromContext(ctx); got != "client4" {
		t.Fatalf("name = %q, want client4", got)
	}

	hub := newClientStreamTestHub(t)
	stream := &recordingClientStream{ctx: ctx}
	done := make(chan error, 1)
	go func() { done <- hub.ClientNodeChannel(stream) }()
	if err := <-done; err != context.Canceled {
		t.Fatalf("ClientNodeChannel returned %v, want the stream's Recv error", err)
	}
	if len(hub.clientStreams) != 0 {
		t.Fatalf("stream not cleared after it ended: %v", hub.clientStreams)
	}
}
