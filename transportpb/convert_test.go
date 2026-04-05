package transportpb

import (
	"math/big"
	"testing"

	"github.com/michael112233/pbft/core"
)

func TestCommitTpsRoundTrip(t *testing.T) {
	in := core.CommitTps{
		To:   "client-1",
		From: "node-2",
		ClientMsg: core.ClientMsg{
			Id:         42,
			Timestamp:  123456789,
			ClientName: "client-a",
			Txn: &core.Transaction{
				Sender:    "alice",
				Receiver:  "bob",
				Amount:    big.NewInt(99),
				Timestamp: 123456789,
			},
		},
	}

	pb := CommitTpsToPB(in)
	out, err := CommitTpsFromPB(pb)
	if err != nil {
		t.Fatalf("CommitTpsFromPB returned error: %v", err)
	}

	if out.To != in.To {
		t.Fatalf("To mismatch: got %q want %q", out.To, in.To)
	}
	if out.From != in.From {
		t.Fatalf("From mismatch: got %q want %q", out.From, in.From)
	}
	if out.ClientMsg.Id != in.ClientMsg.Id {
		t.Fatalf("ClientMsg.Id mismatch: got %d want %d", out.ClientMsg.Id, in.ClientMsg.Id)
	}
	if out.ClientMsg.ClientName != in.ClientMsg.ClientName {
		t.Fatalf("ClientMsg.ClientName mismatch: got %q want %q", out.ClientMsg.ClientName, in.ClientMsg.ClientName)
	}
	if out.ClientMsg.Txn == nil {
		t.Fatal("ClientMsg.Txn is nil")
	}
	if out.ClientMsg.Txn.Amount == nil {
		t.Fatal("ClientMsg.Txn.Amount is nil")
	}
	if out.ClientMsg.Txn.Amount.Cmp(in.ClientMsg.Txn.Amount) != 0 {
		t.Fatalf("ClientMsg.Txn.Amount mismatch: got %s want %s", out.ClientMsg.Txn.Amount.String(), in.ClientMsg.Txn.Amount.String())
	}
}
