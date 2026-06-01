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
				View:        1,
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

func TestHandleIncomingEnvelopeDispatchesVCRunningStatus(t *testing.T) {
	client := &Client{
		log:       logger.NewLogger(0, "client"),
		vcrunChan: make(chan core.VCRunningStatus, 1),
	}
	hub := &ClientMessageHub{
		client_ref: client,
		log:        client.log,
	}

	in := core.VCRunningStatus{
		VCRunning: true,
		Txs: []core.ClientMsgSignature{
			{Data: core.ClientMsg{Id: 1, ClientName: "client-a"}, Signature: []byte{1}},
			{Data: core.ClientMsg{Id: 2, ClientName: "client-a"}, Signature: []byte{2}},
		},
	}
	hub.handleIncomingEnvelope("localhost:28100", &transportpb.Envelope{
		MsgType: core.MsgVCRunningStatusMessage,
		Body: &transportpb.Envelope_VcRunningStatus{
			VcRunningStatus: transportpb.VCRunningStatusToPB(in),
		},
	})

	select {
	case got := <-client.vcrunChan:
		if !got.VCRunning {
			t.Fatal("VCRunning = false, want true")
		}
		if len(got.Txs) != 2 {
			t.Fatalf("len(Txs) = %d, want 2", len(got.Txs))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for VCRunningStatus dispatch")
	}
}

func TestInjectArtificialLatencyForFarNodeRequest(t *testing.T) {
	oldNodeAddr := config.NodeAddr
	config.NodeAddr = map[int]string{
		4: "localhost:28400",
	}
	defer func() {
		config.NodeAddr = oldNodeAddr
	}()

	client := &Client{
		log:    logger.NewLogger(0, "client"),
		config: &config.Config{FarNodeID: 4, FarNodeDelayMs: 20},
	}
	hub := &ClientMessageHub{
		client_ref: client,
		log:        client.log,
	}

	start := time.Now()
	hub.injectArtificialLatency(core.MsgRequestMessage, "localhost:20000", "localhost:28400")
	if elapsed := time.Since(start); elapsed < 15*time.Millisecond {
		t.Fatalf("injectArtificialLatency() elapsed = %s, want at least %s", elapsed, 15*time.Millisecond)
	}
}
