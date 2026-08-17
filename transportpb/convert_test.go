package transportpb

import (
	"math/big"
	"reflect"
	"testing"
	"time"

	"github.com/michael112233/pbft/core"
)

var testTimestamp = time.Unix(123456789, 987654321).UTC()

func TestRequestMessageRoundTripIncludesMsgType(t *testing.T) {
	in := core.RequestMessage{
		MsgType: "RetryRequestMessage",
		Txs: []core.ClientMsgSignature{
			{
				Data: core.ClientMsg{
					Id:         42,
					Timestamp:  testTimestamp,
					ClientName: "client-a",
					Txn: core.Transaction{
						Sender:   "alice",
						Receiver: "bob",
						Amount:   big.NewInt(99),
					},
				},
				Signature: []byte{1, 2, 3},
			},
		},
	}

	out, err := RequestFromPB(RequestToPB(in))
	if err != nil {
		t.Fatalf("RequestFromPB returned error: %v", err)
	}
	if !reflect.DeepEqual(out, in) {
		t.Fatalf("round trip mismatch:\n got: %+v\nwant: %+v", out, in)
	}
}

func TestCommitTpsRoundTrip(t *testing.T) {
	in := core.CommitTps{
		To:   "client-1",
		From: "node-2",
		ClientMsg: core.ClientMsgReply{
			Id:         42,
			Timestamp:  testTimestamp,
			ClientName: "client-a",
			Txn: core.Transaction{
				Sender:   "alice",
				Receiver: "bob",
				Amount:   big.NewInt(99),
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
	if out.ClientMsg.Txn.Amount == nil {
		t.Fatal("ClientMsg.Txn.Amount is nil")
	}
	if out.ClientMsg.Txn.Amount.Cmp(in.ClientMsg.Txn.Amount) != 0 {
		t.Fatalf("ClientMsg.Txn.Amount mismatch: got %s want %s", out.ClientMsg.Txn.Amount.String(), in.ClientMsg.Txn.Amount.String())
	}
}

func TestClientMsgRoundTripPreservesMissingTransaction(t *testing.T) {
	in := core.ClientMsg{Id: 42, Timestamp: testTimestamp}

	pb := ClientMsgToPB(in)
	if pb.Txn != nil {
		t.Fatalf("Txn = %+v, want nil for a zero-value transaction", pb.Txn)
	}

	out, err := ClientMsgFromPB(pb)
	if err != nil {
		t.Fatalf("ClientMsgFromPB returned error: %v", err)
	}
	if !reflect.DeepEqual(out, in) {
		t.Fatalf("round trip mismatch:\n got: %+v\nwant: %+v", out, in)
	}
}

func TestPreprepareMsgSigRoundTripIncludesActualMsg(t *testing.T) {
	in := core.PreprepareMsgSig{
		PreprepareMsgMini: core.PreprepareMsgMini{
			View:                       7,
			SeqNum:                     42,
			DigestClientMsg:            [32]byte{1, 2, 3},
			DigestIndividualClientMsgs: [][32]byte{{4, 5, 6}, {7, 8, 9}},
		},
		Signature: []byte{9, 8, 7},
		ActualMsg: core.ClientMsgSignature{
			Data: core.ClientMsg{
				Id:         99,
				Timestamp:  testTimestamp,
				ClientName: "client-a",
				Txn: core.Transaction{
					Sender:   "alice",
					Receiver: "bob",
					Amount:   big.NewInt(50),
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

func TestPreprepareMiniFromPBRejectsInvalidIndividualDigest(t *testing.T) {
	msg := PreprepareMiniToPB(core.PreprepareMsgMini{
		DigestClientMsg:            [32]byte{1},
		DigestIndividualClientMsgs: [][32]byte{{2}},
	})
	msg.DigestIndividualClientMsgs[0] = []byte{2}

	if _, err := PreprepareMiniFromPB(msg); err == nil {
		t.Fatal("PreprepareMiniFromPB accepted an invalid individual digest length")
	}
}

func TestPreprepareRoundTripIncludesClientMessageBatch(t *testing.T) {
	in := core.PreprepareMsg{
		View:                       3,
		SeqNum:                     17,
		DigestClientMsg:            [32]byte{1, 2, 3},
		DigestIndividualClientMsgs: [][32]byte{{4, 5, 6}, {7, 8, 9}},
		ClientMsg: []core.ClientMsgSignature{
			{
				Data: core.ClientMsg{
					Id:         1,
					Timestamp:  testTimestamp,
					ClientName: "client-a",
					Txn: core.Transaction{
						Sender:   "alice",
						Receiver: "bob",
						Amount:   big.NewInt(10),
					},
				},
				Signature: []byte{1, 2, 3},
			},
			{
				Data: core.ClientMsg{
					Id:         2,
					Timestamp:  testTimestamp.Add(time.Second),
					ClientName: "client-b",
					Txn: core.Transaction{
						Sender:   "carol",
						Receiver: "dave",
						Amount:   big.NewInt(20),
					},
				},
				Signature: []byte{4, 5, 6},
			},
		},
	}

	out, err := PreprepareFromPB(PreprepareToPB(in))
	if err != nil {
		t.Fatalf("PreprepareFromPB returned error: %v", err)
	}
	if !reflect.DeepEqual(out, in) {
		t.Fatalf("round trip mismatch:\n got: %+v\nwant: %+v", out, in)
	}
}

func TestPreprepareFromPBRejectsInvalidIndividualDigest(t *testing.T) {
	msg := PreprepareToPB(core.PreprepareMsg{
		DigestClientMsg:            [32]byte{1},
		DigestIndividualClientMsgs: [][32]byte{{2}},
	})
	msg.DigestIndividualClientMsgs[0] = []byte{2}

	if _, err := PreprepareFromPB(msg); err == nil {
		t.Fatal("PreprepareFromPB accepted an invalid individual digest length")
	}
}

func TestLeaderIdUpdateRoundTrip(t *testing.T) {
	in := core.LeaderIdUpdate{
		To:          "client-1",
		From:        "node-3",
		NewLeaderId: 4,
		View:        7,
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
	if out.View != in.View {
		t.Fatalf("View mismatch: got %d want %d", out.View, in.View)
	}
}

func TestVCRunningStatusRoundTrip(t *testing.T) {
	in := core.VCRunningStatus{
		VCRunning: true,
		Txs: []core.ClientMsgSignature{
			{
				Data: core.ClientMsg{
					Id:         1,
					Timestamp:  time.Unix(123, 0).UTC(),
					ClientName: "client-a",
					Txn: core.Transaction{
						Sender:   "alice",
						Receiver: "bob",
						Amount:   big.NewInt(10),
					},
				},
				Signature: []byte{1, 2, 3},
			},
			{
				Data: core.ClientMsg{
					Id:         2,
					Timestamp:  time.Unix(456, 0).UTC(),
					ClientName: "client-a",
					Txn: core.Transaction{
						Sender:   "carol",
						Receiver: "dave",
						Amount:   big.NewInt(20),
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
