package client

import (
	"testing"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/logger"
	"github.com/michael112233/pbft/transportpb"
)

func TestHandleIncomingEnvelopeDispatchesLeaderUpdate(t *testing.T) {
	oldNodeAddr := config.NodeAddr
	config.NodeAddr = map[int]string{
		2: "localhost:28200",
	}
	defer func() {
		config.NodeAddr = oldNodeAddr
	}()

	client := &Client{
		log: logger.NewLogger(0, "client"),
	}
	hub := &ClientMessageHub{
		client_ref: client,
		log:        client.log,
	}

	hub.handleIncomingEnvelope("localhost:28200", &transportpb.Envelope{
		MsgType: core.MsgLeaderIdUpdateMessage,
		Body: &transportpb.Envelope_LeaderIdUpdate{
			LeaderIdUpdate: transportpb.LeaderIdUpdateToPB(core.LeaderIdUpdate{
				From:        "localhost:28200",
				To:          "localhost:20000",
				NewLeaderId: 2,
			}),
		},
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		client.leaderMu.RLock()
		leaderAddr := client.leaderAddr
		client.leaderMu.RUnlock()
		if leaderAddr == "localhost:28200" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	client.leaderMu.RLock()
	leaderAddr := client.leaderAddr
	client.leaderMu.RUnlock()
	t.Fatalf("leader address was not updated: got %q want %q", leaderAddr, "localhost:28200")
}
