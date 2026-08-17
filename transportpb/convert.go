package transportpb

import (
	"fmt"
	"math/big"
	"time"

	"github.com/michael112233/pbft/core"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TransactionToPB(tx core.Transaction) *Transaction {
	if tx.Sender == "" && tx.Receiver == "" && tx.Amount == nil {
		return nil
	}

	amount := ""
	if tx.Amount != nil {
		amount = tx.Amount.String()
	}
	return &Transaction{
		Sender:   tx.Sender,
		Receiver: tx.Receiver,
		Amount:   amount,
	}
}

func TransactionFromPB(tx *Transaction) (core.Transaction, error) {
	if tx == nil {
		return core.Transaction{}, nil
	}
	var amt *big.Int
	if tx.Amount != "" {
		var ok bool
		amt, ok = new(big.Int).SetString(tx.Amount, 10)
		if !ok {
			return core.Transaction{}, fmt.Errorf("invalid amount %q", tx.Amount)
		}
	}
	return core.Transaction{
		Sender:   tx.Sender,
		Receiver: tx.Receiver,
		Amount:   amt,
	}, nil
}

func timeToPB(timestamp time.Time) *timestamppb.Timestamp {
	return timestamppb.New(timestamp)
}

func timeFromPB(timestamp *timestamppb.Timestamp) (time.Time, error) {
	if timestamp == nil {
		return time.Time{}, nil
	}
	if err := timestamp.CheckValid(); err != nil {
		return time.Time{}, fmt.Errorf("invalid timestamp: %w", err)
	}
	return timestamp.AsTime(), nil
}

func ClientMsgToPB(msg core.ClientMsg) *ClientMsg {
	return &ClientMsg{
		Id:         msg.Id,
		Timestamp:  timeToPB(msg.Timestamp),
		Txn:        TransactionToPB(msg.Txn),
		ClientName: msg.ClientName,
		Padding:    msg.Padding,
	}
}

func ClientMsgFromPB(msg *ClientMsg) (core.ClientMsg, error) {
	if msg == nil {
		return core.ClientMsg{}, nil
	}
	timestamp, err := timeFromPB(msg.Timestamp)
	if err != nil {
		return core.ClientMsg{}, err
	}
	txn, err := TransactionFromPB(msg.Txn)
	if err != nil {
		return core.ClientMsg{}, err
	}
	return core.ClientMsg{
		Id:         msg.Id,
		Timestamp:  timestamp,
		Txn:        txn,
		ClientName: msg.ClientName,
		Padding:    msg.Padding,
	}, nil
}

func ClientMsgReplyToPB(msg core.ClientMsgReply) *ClientMsgReply {
	return &ClientMsgReply{
		Id:         msg.Id,
		Timestamp:  timeToPB(msg.Timestamp),
		Txn:        TransactionToPB(msg.Txn),
		ClientName: msg.ClientName,
	}
}

func ClientMsgReplyFromPB(msg *ClientMsgReply) (core.ClientMsgReply, error) {
	if msg == nil {
		return core.ClientMsgReply{}, nil
	}
	timestamp, err := timeFromPB(msg.Timestamp)
	if err != nil {
		return core.ClientMsgReply{}, err
	}
	txn, err := TransactionFromPB(msg.Txn)
	if err != nil {
		return core.ClientMsgReply{}, err
	}
	return core.ClientMsgReply{
		Id:         msg.Id,
		Timestamp:  timestamp,
		Txn:        txn,
		ClientName: msg.ClientName,
	}, nil
}

func ClientMsgSigToPB(msg core.ClientMsgSignature) *ClientMsgSignature {
	return &ClientMsgSignature{
		Data:      ClientMsgToPB(msg.Data),
		Signature: msg.Signature,
	}
}

func ClientMsgSigFromPB(msg *ClientMsgSignature) (core.ClientMsgSignature, error) {
	if msg == nil {
		return core.ClientMsgSignature{}, nil
	}
	data, err := ClientMsgFromPB(msg.Data)
	if err != nil {
		return core.ClientMsgSignature{}, err
	}
	return core.ClientMsgSignature{
		Data:      data,
		Signature: msg.Signature,
	}, nil
}

func RequestToPB(msg core.RequestMessage) *RequestMessage {
	out := &RequestMessage{
		Txs:     make([]*ClientMsgSignature, 0, len(msg.Txs)),
		MsgType: msg.MsgType,
	}
	for _, tx := range msg.Txs {
		out.Txs = append(out.Txs, ClientMsgSigToPB(tx))
	}
	return out
}

func RequestFromPB(msg *RequestMessage) (core.RequestMessage, error) {
	if msg == nil {
		return core.RequestMessage{}, nil
	}
	out := core.RequestMessage{
		Txs:     make([]core.ClientMsgSignature, 0, len(msg.Txs)),
		MsgType: msg.MsgType,
	}
	for _, tx := range msg.Txs {
		coreTx, err := ClientMsgSigFromPB(tx)
		if err != nil {
			return core.RequestMessage{}, err
		}
		out.Txs = append(out.Txs, coreTx)
	}
	return out, nil
}

func VCRunningStatusToPB(msg core.VCRunningStatus) *VCRunningStatus {
	out := &VCRunningStatus{
		Txs:       make([]*ClientMsgSignature, 0, len(msg.Txs)),
		VcRunning: msg.VCRunning,
	}
	for _, tx := range msg.Txs {
		out.Txs = append(out.Txs, ClientMsgSigToPB(tx))
	}
	return out
}

func VCRunningStatusFromPB(msg *VCRunningStatus) (core.VCRunningStatus, error) {
	if msg == nil {
		return core.VCRunningStatus{}, nil
	}
	out := core.VCRunningStatus{
		Txs:       make([]core.ClientMsgSignature, 0, len(msg.Txs)),
		VCRunning: msg.VcRunning,
	}
	for _, tx := range msg.Txs {
		coreTx, err := ClientMsgSigFromPB(tx)
		if err != nil {
			return core.VCRunningStatus{}, err
		}
		out.Txs = append(out.Txs, coreTx)
	}
	return out, nil
}

func PreprepareToPB(msg core.PreprepareMsg) *PreprepareMsg {
	clientMsgs := make([]*ClientMsgSignature, 0, len(msg.ClientMsg))
	for _, clientMsg := range msg.ClientMsg {
		clientMsgs = append(clientMsgs, ClientMsgSigToPB(clientMsg))
	}

	return &PreprepareMsg{
		View:                       msg.View,
		SeqNum:                     msg.SeqNum,
		ClientMsg:                  clientMsgs,
		DigestClientMsg:            digestToPB(msg.DigestClientMsg),
		DigestIndividualClientMsgs: digestsToPB(msg.DigestIndividualClientMsgs),
	}
}

func PreprepareMiniToPB2(msg core.PreprepareMsgMini) *PreprepareMsg {
	return &PreprepareMsg{
		View:                       msg.View,
		SeqNum:                     msg.SeqNum,
		DigestClientMsg:            digestToPB(msg.DigestClientMsg),
		DigestIndividualClientMsgs: digestsToPB(msg.DigestIndividualClientMsgs),
	}
}

func PreprepareFromPB(msg *PreprepareMsg) (core.PreprepareMsg, error) {
	if msg == nil {
		return core.PreprepareMsg{}, nil
	}
	clientMsgs := make([]core.ClientMsgSignature, 0, len(msg.ClientMsg))
	for _, clientMsg := range msg.ClientMsg {
		coreClientMsg, err := ClientMsgSigFromPB(clientMsg)
		if err != nil {
			return core.PreprepareMsg{}, err
		}
		clientMsgs = append(clientMsgs, coreClientMsg)
	}
	digest, err := digestFromPB(msg.DigestClientMsg)
	if err != nil {
		return core.PreprepareMsg{}, err
	}
	individualDigests, err := digestsFromPB(msg.DigestIndividualClientMsgs)
	if err != nil {
		return core.PreprepareMsg{}, fmt.Errorf("individual client-message digests: %w", err)
	}
	return core.PreprepareMsg{
		View:                       msg.View,
		SeqNum:                     msg.SeqNum,
		ClientMsg:                  clientMsgs,
		DigestClientMsg:            digest,
		DigestIndividualClientMsgs: individualDigests,
	}, nil
}

func digestToPB(digest [32]byte) []byte {
	buf := make([]byte, 32)
	copy(buf, digest[:])
	return buf
}

func digestFromPB(digest []byte) ([32]byte, error) {
	var out [32]byte
	if len(digest) != len(out) {
		return out, fmt.Errorf("digest length mismatch: got=%d want=%d", len(digest), len(out))
	}
	copy(out[:], digest)
	return out, nil
}

func digestsToPB(digests [][32]byte) [][]byte {
	out := make([][]byte, 0, len(digests))
	for _, digest := range digests {
		out = append(out, digestToPB(digest))
	}
	return out
}

func digestsFromPB(digests [][]byte) ([][32]byte, error) {
	out := make([][32]byte, 0, len(digests))
	for i, digest := range digests {
		converted, err := digestFromPB(digest)
		if err != nil {
			return nil, fmt.Errorf("digest at index %d: %w", i, err)
		}
		out = append(out, converted)
	}
	return out, nil
}

func PrepareToPB(msg core.PrepareMsg) *PrepareMsg {
	return &PrepareMsg{
		View:   msg.View,
		SeqNum: msg.SeqNum,
		Digest: digestToPB(msg.Digest),
		From:   int32(msg.From),
		// To:     msg.To,
	}
}

func PrepareFromPB(msg *PrepareMsg) (core.PrepareMsg, error) {
	if msg == nil {
		return core.PrepareMsg{}, nil
	}
	digest, err := digestFromPB(msg.Digest)
	if err != nil {
		return core.PrepareMsg{}, err
	}
	return core.PrepareMsg{
		View:   msg.View,
		SeqNum: msg.SeqNum,
		Digest: digest,
		From:   int(msg.From),
		// To:     msg.To,
	}, nil
}

func CommitToPB(msg core.CommitMsg) *CommitMsg {
	return &CommitMsg{
		View:   msg.View,
		SeqNum: msg.SeqNum,
		Digest: digestToPB(msg.Digest),
		From:   int32(msg.From),
		// To:     msg.To,
	}
}

func CommitFromPB(msg *CommitMsg) (core.CommitMsg, error) {
	if msg == nil {
		return core.CommitMsg{}, nil
	}
	digest, err := digestFromPB(msg.Digest)
	if err != nil {
		return core.CommitMsg{}, err
	}
	return core.CommitMsg{
		View:   msg.View,
		SeqNum: msg.SeqNum,
		Digest: digest,
		From:   int(msg.From),
		// To:     msg.To,
	}, nil
}

func ReplyToPB(msg core.ReplyMessage) *ReplyMessage {
	return &ReplyMessage{
		To:             msg.To,
		From:           msg.From,
		ClientMsg:      ClientMsgToPB(msg.ClientMsg),
		Success:        msg.Result.Success,
		Error:          msg.Result.Error,
		ExecutedSeqNum: msg.Result.ExecutedSeqNum,
	}
}

func ReplyFromPB(msg *ReplyMessage) (core.ReplyMessage, error) {
	if msg == nil {
		return core.ReplyMessage{}, nil
	}
	clientMsg, err := ClientMsgFromPB(msg.ClientMsg)
	if err != nil {
		return core.ReplyMessage{}, err
	}
	return core.ReplyMessage{
		To:        msg.To,
		From:      msg.From,
		ClientMsg: clientMsg,
		Result: core.ExecutionResult{
			Success:        msg.Success,
			Error:          msg.Error,
			ExecutedSeqNum: msg.ExecutedSeqNum,
		},
	}, nil
}

func CommitTpsToPB(msg core.CommitTps) *CommitTps {
	return &CommitTps{
		To:        msg.To,
		From:      msg.From,
		ClientMsg: ClientMsgReplyToPB(msg.ClientMsg),
	}
}

func CommitTpsFromPB(msg *CommitTps) (core.CommitTps, error) {
	if msg == nil {
		return core.CommitTps{}, nil
	}
	clientMsg, err := ClientMsgReplyFromPB(msg.ClientMsg)
	if err != nil {
		return core.CommitTps{}, err
	}
	return core.CommitTps{
		To:        msg.To,
		From:      msg.From,
		ClientMsg: clientMsg,
	}, nil
}

func LeaderIdUpdateToPB(msg core.LeaderIdUpdate) *LeaderIdUpdate {
	return &LeaderIdUpdate{
		To:          msg.To,
		From:        msg.From,
		NewLeaderId: int32(msg.NewLeaderId),
		View:        msg.View,
	}
}

func LeaderIdUpdateFromPB(msg *LeaderIdUpdate) (core.LeaderIdUpdate, error) {
	if msg == nil {
		return core.LeaderIdUpdate{}, nil
	}
	return core.LeaderIdUpdate{
		To:          msg.To,
		From:        msg.From,
		NewLeaderId: int(msg.NewLeaderId),
		View:        msg.View,
	}, nil
}

func CloseToPB(msg core.CloseMessage) *CloseMessage {
	return &CloseMessage{
		Timestamp: msg.Timestamp,
		From:      msg.From,
		To:        msg.To,
	}
}

func CloseFromPB(msg *CloseMessage) core.CloseMessage {
	if msg == nil {
		return core.CloseMessage{}
	}
	return core.CloseMessage{
		Timestamp: msg.Timestamp,
		From:      msg.From,
		To:        msg.To,
	}
}

func PreprepareMiniToPB(msg core.PreprepareMsgMini) *PreprepareMsgMini {
	return &PreprepareMsgMini{
		View:                       msg.View,
		SeqNum:                     msg.SeqNum,
		DigestClientMsg:            digestToPB(msg.DigestClientMsg),
		DigestIndividualClientMsgs: digestsToPB(msg.DigestIndividualClientMsgs),
	}
}

func PreprepareMiniFromPB(msg *PreprepareMsgMini) (core.PreprepareMsgMini, error) {
	if msg == nil {
		return core.PreprepareMsgMini{}, nil
	}
	digest, err := digestFromPB(msg.DigestClientMsg)
	if err != nil {
		return core.PreprepareMsgMini{}, err
	}
	individualDigests, err := digestsFromPB(msg.DigestIndividualClientMsgs)
	if err != nil {
		return core.PreprepareMsgMini{}, fmt.Errorf("individual client-message digests: %w", err)
	}
	return core.PreprepareMsgMini{
		View:                       msg.View,
		SeqNum:                     msg.SeqNum,
		DigestClientMsg:            digest,
		DigestIndividualClientMsgs: individualDigests,
	}, nil
}

func PreprepareMsgSigToPB(msg core.PreprepareMsgSig) *PreprepareMsgSig {
	return &PreprepareMsgSig{
		PreprepareMsgMini: PreprepareMiniToPB(msg.PreprepareMsgMini),
		Signature:         append([]byte(nil), msg.Signature...),
		ActualMsg:         ClientMsgSigToPB(msg.ActualMsg),
	}
}

func PreprepareMsgSigFromPB(msg *PreprepareMsgSig) (core.PreprepareMsgSig, error) {
	if msg == nil {
		return core.PreprepareMsgSig{}, nil
	}
	mini, err := PreprepareMiniFromPB(msg.PreprepareMsgMini)
	if err != nil {
		return core.PreprepareMsgSig{}, err
	}
	actualMsg, err := ClientMsgSigFromPB(msg.ActualMsg)
	if err != nil {
		return core.PreprepareMsgSig{}, err
	}
	return core.PreprepareMsgSig{
		PreprepareMsgMini: mini,
		Signature:         append([]byte(nil), msg.Signature...),
		ActualMsg:         actualMsg,
	}, nil
}

func PrepareMsgSigToPB(msg *core.PrepareMsgSig) *PrepareMsgSig {
	if msg == nil {
		return nil
	}
	return &PrepareMsgSig{
		PrepareMsg: PrepareToPB(msg.PrepareMsg),
		Signature:  append([]byte(nil), msg.Signature...),
	}
}

func PrepareMsgSigFromPB(msg *PrepareMsgSig) (*core.PrepareMsgSig, error) {
	if msg == nil {
		return nil, nil
	}
	prepareMsg, err := PrepareFromPB(msg.PrepareMsg)
	if err != nil {
		return nil, err
	}
	return &core.PrepareMsgSig{
		PrepareMsg: prepareMsg,
		Signature:  append([]byte(nil), msg.Signature...),
	}, nil
}
