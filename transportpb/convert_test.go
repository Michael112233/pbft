package transportpb

import (
	"math/big"
	"reflect"
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

func TestPreprepareMsgSigRoundTripIncludesActualMsg(t *testing.T) {
	in := core.PreprepareMsgSig{
		PreprepareMsgMini: core.PreprepareMsgMini{
			View:            7,
			SeqNum:          42,
			DigestClientMsg: [32]byte{1, 2, 3},
		},
		Signature: []byte{9, 8, 7},
		ActualMsg: core.ClientMsgSignature{
			Data: core.ClientMsg{
				Id:         99,
				Timestamp:  123456789,
				ClientName: "client-a",
				Txn: &core.Transaction{
					Sender:    "alice",
					Receiver:  "bob",
					Amount:    big.NewInt(50),
					Timestamp: 123456789,
				},
			},
			Signature: []byte{4, 5, 6},
		},
	}

	out, err := PreprepareMsgSigFromPB(PreprepareMsgSigToPB(in))
	if err != nil {
		t.Fatalf("PreprepareMsgSigFromPB returned error: %v", err)
	}

	if !reflect.DeepEqual(out, in) {
		t.Fatalf("round trip mismatch:\n got: %+v\nwant: %+v", out, in)
	}
}

func TestLeaderIdUpdateRoundTrip(t *testing.T) {
	in := core.LeaderIdUpdate{
		To:          "client-1",
		From:        "node-3",
		NewLeaderId: 4,
	}

	pb := LeaderIdUpdateToPB(in)
	out, err := LeaderIdUpdateFromPB(pb)
	if err != nil {
		t.Fatalf("LeaderIdUpdateFromPB returned error: %v", err)
	}

	if out.To != in.To {
		t.Fatalf("To mismatch: got %q want %q", out.To, in.To)
	}
	if out.From != in.From {
		t.Fatalf("From mismatch: got %q want %q", out.From, in.From)
	}
	if out.NewLeaderId != in.NewLeaderId {
		t.Fatalf("NewLeaderId mismatch: got %d want %d", out.NewLeaderId, in.NewLeaderId)
	}
}

func TestVCRunningStatusRoundTrip(t *testing.T) {
	in := core.VCRunningStatus{
		VCRunning: true,
		Txs: []core.ClientMsgSignature{
			{
				Data: core.ClientMsg{
					Id:         1,
					Timestamp:  123,
					ClientName: "client-a",
					Txn: &core.Transaction{
						Sender:    "alice",
						Receiver:  "bob",
						Amount:    big.NewInt(10),
						Timestamp: 123,
					},
				},
				Signature: []byte{1, 2, 3},
			},
			{
				Data: core.ClientMsg{
					Id:         2,
					Timestamp:  456,
					ClientName: "client-a",
					Txn: &core.Transaction{
						Sender:    "carol",
						Receiver:  "dave",
						Amount:    big.NewInt(20),
						Timestamp: 456,
					},
				},
				Signature: []byte{4, 5, 6},
			},
		},
	}

	for _, vcRunning := range []bool{true, false} {
		in.VCRunning = vcRunning
		out, err := VCRunningStatusFromPB(VCRunningStatusToPB(in))
		if err != nil {
			t.Fatalf("VCRunningStatusFromPB returned error: %v", err)
		}
		if !reflect.DeepEqual(out, in) {
			t.Fatalf("round trip mismatch:\n got: %+v\nwant: %+v", out, in)
		}
	}
}
