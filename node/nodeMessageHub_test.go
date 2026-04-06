package node

import (
	"testing"

	"github.com/michael112233/pbft/core"
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
}
