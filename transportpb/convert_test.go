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
		ClientMsg: core.ClientMsgReply{
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

func TestReqMissingClientMsgRoundTrip(t *testing.T) {
	in := core.ReqMissingClientMsg{
		MissingClientMsgs: [][32]byte{
			{1, 2, 3},
			{4, 5, 6},
		},
		From: 2,
	}

	out, err := ReqMissingClientMsgFromPB(ReqMissingClientMsgToPB(in))
	if err != nil {
		t.Fatalf("ReqMissingClientMsgFromPB returned error: %v", err)
	}

	if !reflect.DeepEqual(out, in) {
		t.Fatalf("round trip mismatch:\n got: %+v\nwant: %+v", out, in)
	}
}

func TestReplyMissingClientMsgRoundTrip(t *testing.T) {
	digest := [32]byte{9, 8, 7}
	in := core.ReplyMissingClientMsg{
		MissingClientMsgs: []core.MissingClientData{
			{
				Digest: digest,
				Msg: core.ClientMsgSignature{
					Data: core.ClientMsg{
						Id:         101,
						Timestamp:  123456789,
						ClientName: "client-a",
						Txn: &core.Transaction{
							Sender:    "alice",
							Receiver:  "bob",
							Amount:    big.NewInt(75),
							Timestamp: 123456789,
						},
					},
					Signature: []byte{1, 2, 3},
				},
			},
		},
		From: 3,
	}

	out, err := ReplyMissingClientMsgFromPB(ReplyMissingClientMsgToPB(in))
	if err != nil {
		t.Fatalf("ReplyMissingClientMsgFromPB returned error: %v", err)
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

func TestNewViewRoundTripIncludesThroughput(t *testing.T) {
	in := core.NewViewMsg{
		NewViewNumber: 7,
		From:          2,
		Throughput:    321.45,
	}

	pb := NewViewToPB(in)
	out, err := NewViewFromPB(pb)
	if err != nil {
		t.Fatalf("NewViewFromPB returned error: %v", err)
	}

	if out.NewViewNumber != in.NewViewNumber {
		t.Fatalf("NewViewNumber mismatch: got %d want %d", out.NewViewNumber, in.NewViewNumber)
	}
	if out.From != in.From {
		t.Fatalf("From mismatch: got %d want %d", out.From, in.From)
	}
	if out.Throughput != in.Throughput {
		t.Fatalf("Throughput mismatch: got %f want %f", out.Throughput, in.Throughput)
	}
}

func TestViewChangeWRRRoundTrip(t *testing.T) {
	in := core.ViewChangeMsg{
		ViewNumber:          7,
		CheckpointSeqNumber: 40,
		From:                2,
		PreparedCerts:       map[int64]*core.PreparedCert{},
		Type:                core.VCTypeWRR,
		WRRData: &core.WRRVCData{
			Throughput: 123.45,
		},
	}

	out, err := ViewChangeFromPB(ViewChangeToPB(in))
	if err != nil {
		t.Fatalf("ViewChangeFromPB returned error: %v", err)
	}

	if !reflect.DeepEqual(out, in) {
		t.Fatalf("round trip mismatch:\n got: %+v\nwant: %+v", out, in)
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

func TestIntentToChangeViewRoundTrip(t *testing.T) {
	in := core.IntentToChangeViewMsg{
		ViewNumber: 12,
		From:       3,
	}

	out, err := IntentToChangeViewFromPB(IntentToChangeViewToPB(in))
	if err != nil {
		t.Fatalf("IntentToChangeViewFromPB returned error: %v", err)
	}
	if !reflect.DeepEqual(out, in) {
		t.Fatalf("round trip mismatch:\n got: %+v\nwant: %+v", out, in)
	}
}

func TestEpochDataForAggregationRoundTrip(t *testing.T) {
	in := core.EpochDataForAggregation{
		EpochNumber:  12,
		Throughput:   1234.5,
		ProposalRate: 678.25,
		From:         3,
	}

	out, err := EpochDataForAggregationFromPB(EpochDataForAggregationToPB(in))
	if err != nil {
		t.Fatalf("EpochDataForAggregationFromPB returned error: %v", err)
	}
	if !reflect.DeepEqual(out, in) {
		t.Fatalf("round trip mismatch:\n got: %+v\nwant: %+v", out, in)
	}
}
