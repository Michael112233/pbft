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
	actualMsgs := make([]*ClientMsgSignature, 0, len(msg.ActualMsg))
	for _, actualMsg := range msg.ActualMsg {
		actualMsgs = append(actualMsgs, ClientMsgSigToPB(actualMsg))
	}
	return &PreprepareMsgSig{
		PreprepareMsgMini: PreprepareMiniToPB(msg.PreprepareMsgMini),
		Signature:         append([]byte(nil), msg.Signature...),
		ActualMsg:         actualMsgs,
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
	actualMsgs := make([]core.ClientMsgSignature, 0, len(msg.ActualMsg))
	for _, actualMsg := range msg.ActualMsg {
		converted, err := ClientMsgSigFromPB(actualMsg)
		if err != nil {
			return core.PreprepareMsgSig{}, err
		}
		actualMsgs = append(actualMsgs, converted)
	}
	return core.PreprepareMsgSig{
		PreprepareMsgMini: mini,
		Signature:         append([]byte(nil), msg.Signature...),
		ActualMsg:         actualMsgs,
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

func CheckpointToPB(msg core.CheckpointMsg) *CheckpointMsg {
	return &CheckpointMsg{
		SeqNum: msg.SeqNum,
		Digest: digestToPB(msg.Digest),
		From:   int32(msg.From),
	}
}

func CheckpointFromPB(msg *CheckpointMsg) (core.CheckpointMsg, error) {
	if msg == nil {
		return core.CheckpointMsg{}, nil
	}
	digest, err := digestFromPB(msg.Digest)
	if err != nil {
		return core.CheckpointMsg{}, err
	}
	return core.CheckpointMsg{
		SeqNum: msg.SeqNum,
		Digest: digest,
		From:   int(msg.From),
	}, nil
}

func CheckpointMsgSigToPB(msg core.CheckpointMsgSig) *CheckpointMsgSig {
	return &CheckpointMsgSig{
		CheckpointMsg: CheckpointToPB(msg.CheckpointMsg),
		Signature:     append([]byte(nil), msg.Signature...),
	}
}

func CheckpointMsgSigFromPB(msg *CheckpointMsgSig) (core.CheckpointMsgSig, error) {
	if msg == nil {
		return core.CheckpointMsgSig{}, nil
	}
	checkpointMsg, err := CheckpointFromPB(msg.CheckpointMsg)
	if err != nil {
		return core.CheckpointMsgSig{}, err
	}
	return core.CheckpointMsgSig{
		CheckpointMsg: checkpointMsg,
		Signature:     append([]byte(nil), msg.Signature...),
	}, nil
}

func PreparedCertToPB(cert *core.PreparedCert) *PreparedCert {
	if cert == nil {
		return nil
	}
	prepareLog := make(map[int32]*PrepareMsgSig, len(cert.PrepareLog))
	for from, prepare := range cert.PrepareLog {
		prepareCopy := prepare
		prepareLog[int32(from)] = PrepareMsgSigToPB(&prepareCopy)
	}
	return &PreparedCert{
		PreprepareMsg: PreprepareMsgSigToPB(cert.PreprepareMsg),
		PrepareLog:    prepareLog,
	}
}

func PreparedCertFromPB(cert *PreparedCert) (*core.PreparedCert, error) {
	if cert == nil {
		return nil, nil
	}
	preprepareMsg, err := PreprepareMsgSigFromPB(cert.PreprepareMsg)
	if err != nil {
		return nil, fmt.Errorf("preprepare message: %w", err)
	}
	prepareLog := make(map[int]core.PrepareMsgSig, len(cert.PrepareLog))
	for from, prepare := range cert.PrepareLog {
		converted, err := PrepareMsgSigFromPB(prepare)
		if err != nil {
			return nil, fmt.Errorf("prepare from node %d: %w", from, err)
		}
		if converted != nil {
			prepareLog[int(from)] = *converted
		}
	}
	return &core.PreparedCert{
		PreprepareMsg: preprepareMsg,
		PrepareLog:    prepareLog,
	}, nil
}

func vcTypeToPB(vcType core.VCType) ViewChangeMsg_VCType {
	switch vcType {
	case core.VCTypeElection:
		return ViewChangeMsg_VC_TYPE_ELECTION
	case core.VCTypeRoundRobin:
		return ViewChangeMsg_VC_TYPE_ROUND_ROBIN
	case core.VCTypeWRR:
		return ViewChangeMsg_VC_TYPE_WRR
	default:
		return ViewChangeMsg_VC_TYPE_UNSPECIFIED
	}
}

func vcTypeFromPB(vcType ViewChangeMsg_VCType) (core.VCType, error) {
	switch vcType {
	case ViewChangeMsg_VC_TYPE_ELECTION:
		return core.VCTypeElection, nil
	case ViewChangeMsg_VC_TYPE_ROUND_ROBIN:
		return core.VCTypeRoundRobin, nil
	case ViewChangeMsg_VC_TYPE_WRR:
		return core.VCTypeWRR, nil
	default:
		return 0, fmt.Errorf("invalid view-change type %d", vcType)
	}
}

func balancesToPB(balances map[string]*big.Int) map[string]string {
	if balances == nil {
		return nil
	}
	out := make(map[string]string, len(balances))
	for account, balance := range balances {
		if balance == nil {
			out[account] = ""
			continue
		}
		out[account] = balance.String()
	}
	return out
}

func balancesFromPB(balances map[string]string) (map[string]*big.Int, error) {
	if balances == nil {
		return nil, nil
	}
	out := make(map[string]*big.Int, len(balances))
	for account, encoded := range balances {
		if encoded == "" {
			out[account] = nil
			continue
		}
		balance, ok := new(big.Int).SetString(encoded, 10)
		if !ok {
			return nil, fmt.Errorf("invalid checkpoint balance for account %q: %q", account, encoded)
		}
		out[account] = balance
	}
	return out, nil
}

func ViewChangeToPB(msg core.ViewChangeMsg) *ViewChangeMsg {
	preparedCerts := make(map[int64]*PreparedCert, len(msg.PreparedCerts))
	for seqNum, cert := range msg.PreparedCerts {
		preparedCerts[seqNum] = PreparedCertToPB(cert)
	}
	checkpointProof := make([]*CheckpointMsgSig, 0, len(msg.CheckpointProof))
	for _, checkpoint := range msg.CheckpointProof {
		checkpointProof = append(checkpointProof, CheckpointMsgSigToPB(checkpoint))
	}

	out := &ViewChangeMsg{
		ViewNumber:          msg.ViewNumber,
		CheckpointSeqNumber: msg.CheckpointSeqNumber,
		From:                int32(msg.From),
		PreparedCerts:       preparedCerts,
		VcType:              vcTypeToPB(msg.Type),
		CheckpointDigest:    digestToPB(msg.CheckpointDigest),
		CheckpointProof:     checkpointProof,
		CheckpointBalances:  balancesToPB(msg.CheckpointBalances),
	}
	switch msg.Type {
	case core.VCTypeElection:
		if msg.ElectionData != nil {
			out.VcData = &ViewChangeMsg_Election{Election: &ElectionVCData{
				ReqVote:   msg.ElectionData.ReqVote,
				GrantVote: msg.ElectionData.GrantVote,
				GrantTo:   int32(msg.ElectionData.GrantTo),
			}}
		}
	case core.VCTypeRoundRobin:
		if msg.RoundRobinData != nil {
			out.VcData = &ViewChangeMsg_RoundRobin{RoundRobin: &RoundRobinVCData{
				GrantVote: msg.RoundRobinData.GrantVote,
			}}
		}
	case core.VCTypeWRR:
		if msg.WRRData != nil {
			out.VcData = &ViewChangeMsg_Wrr{Wrr: &WRRVCData{
				Throughput: msg.WRRData.Throughput,
			}}
		}
	}
	return out
}

func ViewChangeFromPB(msg *ViewChangeMsg) (core.ViewChangeMsg, error) {
	if msg == nil {
		return core.ViewChangeMsg{}, nil
	}
	vcType, err := vcTypeFromPB(msg.VcType)
	if err != nil {
		return core.ViewChangeMsg{}, err
	}
	checkpointDigest, err := digestFromPB(msg.CheckpointDigest)
	if err != nil {
		return core.ViewChangeMsg{}, fmt.Errorf("checkpoint digest: %w", err)
	}
	checkpointProof := make([]core.CheckpointMsgSig, 0, len(msg.CheckpointProof))
	for i, checkpoint := range msg.CheckpointProof {
		converted, err := CheckpointMsgSigFromPB(checkpoint)
		if err != nil {
			return core.ViewChangeMsg{}, fmt.Errorf("checkpoint proof at index %d: %w", i, err)
		}
		checkpointProof = append(checkpointProof, converted)
	}
	preparedCerts := make(map[int64]*core.PreparedCert, len(msg.PreparedCerts))
	for seqNum, cert := range msg.PreparedCerts {
		converted, err := PreparedCertFromPB(cert)
		if err != nil {
			return core.ViewChangeMsg{}, fmt.Errorf("prepared certificate at sequence %d: %w", seqNum, err)
		}
		preparedCerts[seqNum] = converted
	}
	balances, err := balancesFromPB(msg.CheckpointBalances)
	if err != nil {
		return core.ViewChangeMsg{}, err
	}

	out := core.ViewChangeMsg{
		ViewNumber:          msg.ViewNumber,
		CheckpointSeqNumber: msg.CheckpointSeqNumber,
		CheckpointDigest:    checkpointDigest,
		CheckpointProof:     checkpointProof,
		CheckpointBalances:  balances,
		From:                int(msg.From),
		PreparedCerts:       preparedCerts,
		Type:                vcType,
	}
	switch data := msg.VcData.(type) {
	case nil:
	case *ViewChangeMsg_Election:
		if vcType != core.VCTypeElection {
			return core.ViewChangeMsg{}, fmt.Errorf("view-change type %d has election data", vcType)
		}
		if data.Election != nil {
			out.ElectionData = &core.ElectionVCData{
				ReqVote:   data.Election.ReqVote,
				GrantVote: data.Election.GrantVote,
				GrantTo:   int(data.Election.GrantTo),
			}
		}
	case *ViewChangeMsg_RoundRobin:
		if vcType != core.VCTypeRoundRobin {
			return core.ViewChangeMsg{}, fmt.Errorf("view-change type %d has round-robin data", vcType)
		}
		if data.RoundRobin != nil {
			out.RoundRobinData = &core.RoundRobinVCData{GrantVote: data.RoundRobin.GrantVote}
		}
	case *ViewChangeMsg_Wrr:
		if vcType != core.VCTypeWRR {
			return core.ViewChangeMsg{}, fmt.Errorf("view-change type %d has WRR data", vcType)
		}
		if data.Wrr != nil {
			out.WRRData = &core.WRRVCData{Throughput: data.Wrr.Throughput}
		}
	default:
		return core.ViewChangeMsg{}, fmt.Errorf("unsupported view-change data %T", data)
	}
	return out, nil
}

func ViewChangeMsgSigToPB(msg core.ViewChangeMsgSig) *ViewChangeMsgSig {
	return &ViewChangeMsgSig{
		ViewChangeMsg: ViewChangeToPB(msg.ViewChangeMsg),
		Signature:     append([]byte(nil), msg.Signature...),
	}
}

func ViewChangeMsgSigFromPB(msg *ViewChangeMsgSig) (core.ViewChangeMsgSig, error) {
	if msg == nil {
		return core.ViewChangeMsgSig{}, nil
	}
	viewChangeMsg, err := ViewChangeFromPB(msg.ViewChangeMsg)
	if err != nil {
		return core.ViewChangeMsgSig{}, err
	}
	return core.ViewChangeMsgSig{
		ViewChangeMsg: viewChangeMsg,
		Signature:     append([]byte(nil), msg.Signature...),
	}, nil
}

func NewViewToPB(msg core.NewViewMsg) *NewViewMsg {
	var preprepareLog []*PreprepareMsgSig
	if msg.PreprepareLog != nil {
		preprepareLog = make([]*PreprepareMsgSig, 0, len(msg.PreprepareLog))
		for _, preprepare := range msg.PreprepareLog {
			preprepareLog = append(preprepareLog, PreprepareMsgSigToPB(preprepare))
		}
	}

	var viewChangeLog []*ViewChangeMsgSig
	if msg.ViewChangeLog != nil {
		viewChangeLog = make([]*ViewChangeMsgSig, 0, len(msg.ViewChangeLog))
		for _, viewChange := range msg.ViewChangeLog {
			if viewChange == nil {
				viewChangeLog = append(viewChangeLog, nil)
				continue
			}
			viewChangeLog = append(viewChangeLog, ViewChangeMsgSigToPB(*viewChange))
		}
	}

	return &NewViewMsg{
		PreprepareLog: preprepareLog,
		ViewChangeLog: viewChangeLog,
		NewViewNumber: msg.NewViewNumber,
		From:          int32(msg.From),
		Throughput:    msg.Throughput,
	}
}

func NewViewFromPB(msg *NewViewMsg) (core.NewViewMsg, error) {
	if msg == nil {
		return core.NewViewMsg{}, nil
	}

	var preprepareLog []core.PreprepareMsgSig
	if msg.PreprepareLog != nil {
		preprepareLog = make([]core.PreprepareMsgSig, 0, len(msg.PreprepareLog))
		for i, preprepare := range msg.PreprepareLog {
			converted, err := PreprepareMsgSigFromPB(preprepare)
			if err != nil {
				return core.NewViewMsg{}, fmt.Errorf("preprepare log at index %d: %w", i, err)
			}
			preprepareLog = append(preprepareLog, converted)
		}
	}

	var viewChangeLog []*core.ViewChangeMsgSig
	if msg.ViewChangeLog != nil {
		viewChangeLog = make([]*core.ViewChangeMsgSig, 0, len(msg.ViewChangeLog))
		for i, viewChange := range msg.ViewChangeLog {
			if viewChange == nil {
				viewChangeLog = append(viewChangeLog, nil)
				continue
			}
			converted, err := ViewChangeMsgSigFromPB(viewChange)
			if err != nil {
				return core.NewViewMsg{}, fmt.Errorf("view-change log at index %d: %w", i, err)
			}
			convertedCopy := converted
			viewChangeLog = append(viewChangeLog, &convertedCopy)
		}
	}

	return core.NewViewMsg{
		PreprepareLog: preprepareLog,
		ViewChangeLog: viewChangeLog,
		NewViewNumber: msg.NewViewNumber,
		Throughput:    msg.Throughput,
		From:          int(msg.From),
	}, nil
}

func NewViewMsgSigToPB(msg core.NewViewMsgSig) *NewViewMsgSig {
	return &NewViewMsgSig{
		NewViewMsg: NewViewToPB(msg.NewViewMsg),
		Signature:  append([]byte(nil), msg.Signature...),
	}
}

func NewViewMsgSigFromPB(msg *NewViewMsgSig) (core.NewViewMsgSig, error) {
	if msg == nil {
		return core.NewViewMsgSig{}, nil
	}
	newViewMsg, err := NewViewFromPB(msg.NewViewMsg)
	if err != nil {
		return core.NewViewMsgSig{}, err
	}
	return core.NewViewMsgSig{
		NewViewMsg: newViewMsg,
		Signature:  append([]byte(nil), msg.Signature...),
	}, nil
}

func RequestVoteToPB(msg core.RequestVoteMsg) *RequestVoteMsg {
	return &RequestVoteMsg{
		From:       int32(msg.From),
		ViewNumber: msg.ViewNumber,
		Seed:       append([]byte(nil), msg.Seed...),
		DelaySteps: msg.DelaySteps,
		Y:          append([]byte(nil), msg.Y...),
		VdfProof:   append([]byte(nil), msg.VDFProof...),
		VrfProof:   append([]byte(nil), msg.VRFProof...),
	}
}

func RequestVoteFromPB(msg *RequestVoteMsg) (core.RequestVoteMsg, error) {
	if msg == nil {
		return core.RequestVoteMsg{}, nil
	}

	return core.RequestVoteMsg{
		From:       int(msg.From),
		ViewNumber: msg.ViewNumber,
		Seed:       append([]byte(nil), msg.Seed...),
		DelaySteps: msg.DelaySteps,
		Y:          append([]byte(nil), msg.Y...),
		VDFProof:   append([]byte(nil), msg.VdfProof...),
		VRFProof:   append([]byte(nil), msg.VrfProof...),
	}, nil
}

func RequestVoteMsgSigToPB(msg core.RequestVoteMsgSig) *RequestVoteMsgSig {
	return &RequestVoteMsgSig{
		RequestVoteMsg: RequestVoteToPB(msg.RequestVoteMsg),
		Signature:      append([]byte(nil), msg.Signature...),
	}
}

func RequestVoteMsgSigFromPB(msg *RequestVoteMsgSig) (core.RequestVoteMsgSig, error) {
	if msg == nil {
		return core.RequestVoteMsgSig{}, nil
	}

	requestVoteMsg, err := RequestVoteFromPB(msg.RequestVoteMsg)
	if err != nil {
		return core.RequestVoteMsgSig{}, err
	}

	return core.RequestVoteMsgSig{
		RequestVoteMsg: requestVoteMsg,
		Signature:      append([]byte(nil), msg.Signature...),
	}, nil
}

func GrantVoteToPB(msg core.GrantVoteMsg) *GrantVoteMsg {
	return &GrantVoteMsg{
		From:       int32(msg.From),
		ViewNumber: msg.ViewNumber,
	}
}

func GrantVoteFromPB(msg *GrantVoteMsg) (core.GrantVoteMsg, error) {
	if msg == nil {
		return core.GrantVoteMsg{}, nil
	}

	return core.GrantVoteMsg{
		From:       int(msg.From),
		ViewNumber: msg.ViewNumber,
	}, nil
}

func GrantVoteMsgSigToPB(msg core.GrantVoteMsgSig) *GrantVoteMsgSig {
	return &GrantVoteMsgSig{
		GrantVoteMsg: GrantVoteToPB(msg.GrantVoteMsg),
		Signature:    append([]byte(nil), msg.Signature...),
	}
}

func GrantVoteMsgSigFromPB(msg *GrantVoteMsgSig) (core.GrantVoteMsgSig, error) {
	if msg == nil {
		return core.GrantVoteMsgSig{}, nil
	}

	grantVoteMsg, err := GrantVoteFromPB(msg.GrantVoteMsg)
	if err != nil {
		return core.GrantVoteMsgSig{}, err
	}

	return core.GrantVoteMsgSig{
		GrantVoteMsg: grantVoteMsg,
		Signature:    append([]byte(nil), msg.Signature...),
	}, nil
}
