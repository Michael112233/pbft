package transportpb

import (
	"fmt"
	"math/big"

	"github.com/michael112233/pbft/core"
)

func TransactionToPB(tx *core.Transaction) *Transaction {
	if tx == nil {
		return nil
	}
	amount := "0"
	if tx.Amount != nil {
		amount = tx.Amount.String()
	}
	return &Transaction{
		Sender:    tx.Sender,
		Receiver:  tx.Receiver,
		Amount:    amount,
		Timestamp: tx.Timestamp,
	}
}

func TransactionFromPB(tx *Transaction) (*core.Transaction, error) {
	if tx == nil {
		return nil, nil
	}
	amt := big.NewInt(0)
	if tx.Amount != "" {
		var ok bool
		amt, ok = new(big.Int).SetString(tx.Amount, 10)
		if !ok {
			return nil, fmt.Errorf("invalid amount %q", tx.Amount)
		}
	}
	return &core.Transaction{
		Sender:    tx.Sender,
		Receiver:  tx.Receiver,
		Amount:    amt,
		Timestamp: tx.Timestamp,
	}, nil
}

func ClientMsgToPB(msg core.ClientMsg) *ClientMsg {
	return &ClientMsg{
		Id:         msg.Id,
		Timestamp:  msg.Timestamp,
		Txn:        TransactionToPB(msg.Txn),
		ClientName: msg.ClientName,
		Padding:    msg.Padding,
	}
}

func ClientMsgFromPB(msg *ClientMsg) (core.ClientMsg, error) {
	if msg == nil {
		return core.ClientMsg{}, nil
	}
	txn, err := TransactionFromPB(msg.Txn)
	if err != nil {
		return core.ClientMsg{}, err
	}
	return core.ClientMsg{
		Id:         msg.Id,
		Timestamp:  msg.Timestamp,
		Txn:        txn,
		ClientName: msg.ClientName,
		Padding:    msg.Padding,
	}, nil
}

func ClientMsgReplyToPB(msg core.ClientMsgReply) *ClientMsgReply {
	return &ClientMsgReply{
		Id:         msg.Id,
		Timestamp:  msg.Timestamp,
		Txn:        TransactionToPB(msg.Txn),
		ClientName: msg.ClientName,
	}
}

func ClientMsgReplyFromPB(msg *ClientMsgReply) (core.ClientMsgReply, error) {
	if msg == nil {
		return core.ClientMsgReply{}, nil
	}
	txn, err := TransactionFromPB(msg.Txn)
	if err != nil {
		return core.ClientMsgReply{}, err
	}
	return core.ClientMsgReply{
		Id:         msg.Id,
		Timestamp:  msg.Timestamp,
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
		Txs: make([]*ClientMsgSignature, 0, len(msg.Txs)),
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
		Txs: make([]core.ClientMsgSignature, 0, len(msg.Txs)),
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

// func VCToPB(msg core.ViewChangeMsg) *ViewChangeMsg {

func PreprepareToPB(msg core.PreprepareMsg) *PreprepareMsg {
	return &PreprepareMsg{
		View:      msg.View,
		SeqNum:    msg.SeqNum,
		ClientMsg: ClientMsgSigToPB(msg.ClientMsg),
		// To:              msg.To,
		DigestClientMsg: digestToPB(msg.DigestClientMsg),
	}
}

func PreprepareMiniToPB2(msg core.PreprepareMsgMini) *PreprepareMsg {
	return &PreprepareMsg{
		View:   msg.View,
		SeqNum: msg.SeqNum,
		// ClientMsg: ClientMsgSigToPB(msg.ClientMsg),
		DigestClientMsg: digestToPB(msg.DigestClientMsg),
	}
}

func PreprepareFromPB(msg *PreprepareMsg) (core.PreprepareMsg, error) {
	if msg == nil {
		return core.PreprepareMsg{}, nil
	}
	clientMsg, err := ClientMsgSigFromPB(msg.ClientMsg)
	if err != nil {
		return core.PreprepareMsg{}, err
	}
	digest, err := digestFromPB(msg.DigestClientMsg)
	if err != nil {
		return core.PreprepareMsg{}, err
	}
	return core.PreprepareMsg{
		View:      msg.View,
		SeqNum:    msg.SeqNum,
		ClientMsg: clientMsg,
		// To:              msg.To,
		DigestClientMsg: digest,
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

func RequestStateTransferToPB(msg core.RequestStateTransferMsg) *RequestStateTransferMsg {
	return &RequestStateTransferMsg{
		SeqNum: msg.SeqNum,
		Digest: digestToPB(msg.Digest),
		From:   int32(msg.From),
	}
}

func RequestStateTransferFromPB(msg *RequestStateTransferMsg) (core.RequestStateTransferMsg, error) {
	if msg == nil {
		return core.RequestStateTransferMsg{}, nil
	}
	digest, err := digestFromPB(msg.Digest)
	if err != nil {
		return core.RequestStateTransferMsg{}, err
	}
	return core.RequestStateTransferMsg{
		SeqNum: msg.SeqNum,
		Digest: digest,
		From:   int(msg.From),
	}, nil
}

func ReqMissingClientMsgToPB(msg core.ReqMissingClientMsg) *ReqMissingClientMsg {
	out := &ReqMissingClientMsg{
		MissingClientMsgs: make([][]byte, 0, len(msg.MissingClientMsgs)),
		From:              int32(msg.From),
	}
	for _, digest := range msg.MissingClientMsgs {
		out.MissingClientMsgs = append(out.MissingClientMsgs, digestToPB(digest))
	}
	return out
}

func ReqMissingClientMsgFromPB(msg *ReqMissingClientMsg) (core.ReqMissingClientMsg, error) {
	if msg == nil {
		return core.ReqMissingClientMsg{}, nil
	}
	out := core.ReqMissingClientMsg{
		MissingClientMsgs: make([][32]byte, 0, len(msg.MissingClientMsgs)),
		From:              int(msg.From),
	}
	for _, digestBytes := range msg.MissingClientMsgs {
		digest, err := digestFromPB(digestBytes)
		if err != nil {
			return core.ReqMissingClientMsg{}, err
		}
		out.MissingClientMsgs = append(out.MissingClientMsgs, digest)
	}
	return out, nil
}

func ReplyMissingClientMsgToPB(msg core.ReplyMissingClientMsg) *ReplyMissingClientMsg {
	out := &ReplyMissingClientMsg{
		MissingClientMsgs: make([]*MissingClientData, 0, len(msg.MissingClientMsgs)),
		From:              int32(msg.From),
	}
	for _, clientData := range msg.MissingClientMsgs {
		out.MissingClientMsgs = append(out.MissingClientMsgs, &MissingClientData{
			Digest: digestToPB(clientData.Digest),
			Msg:    ClientMsgSigToPB(clientData.Msg),
		})
	}
	return out
}

func ReplyMissingClientMsgFromPB(msg *ReplyMissingClientMsg) (core.ReplyMissingClientMsg, error) {
	if msg == nil {
		return core.ReplyMissingClientMsg{}, nil
	}
	out := core.ReplyMissingClientMsg{
		MissingClientMsgs: make([]core.MissingClientData, 0, len(msg.MissingClientMsgs)),
		From:              int(msg.From),
	}
	for _, clientData := range msg.MissingClientMsgs {
		digest, err := digestFromPB(clientData.Digest)
		if err != nil {
			return core.ReplyMissingClientMsg{}, err
		}
		coreClientMsg, err := ClientMsgSigFromPB(clientData.Msg)
		if err != nil {
			return core.ReplyMissingClientMsg{}, err
		}
		out.MissingClientMsgs = append(out.MissingClientMsgs, core.MissingClientData{
			Digest: digest,
			Msg:    coreClientMsg,
		})
	}
	return out, nil
}

func StateTransferToPB(msg core.StateTransferMsg) *StateTransferMsg {
	out := &StateTransferMsg{
		SeqNum:   msg.SeqNum,
		Digest:   digestToPB(msg.Digest),
		From:     int32(msg.From),
		Balances: make(map[string]string, len(msg.Balances)),
	}
	for account, balance := range msg.Balances {
		if balance == nil {
			out.Balances[account] = "0"
			continue
		}
		out.Balances[account] = balance.String()
	}
	return out
}

func StateTransferFromPB(msg *StateTransferMsg) (core.StateTransferMsg, error) {
	if msg == nil {
		return core.StateTransferMsg{}, nil
	}
	digest, err := digestFromPB(msg.Digest)
	if err != nil {
		return core.StateTransferMsg{}, err
	}
	out := core.StateTransferMsg{
		SeqNum:   msg.SeqNum,
		Digest:   digest,
		From:     int(msg.From),
		Balances: make(map[string]*big.Int, len(msg.Balances)),
	}
	for account, balance := range msg.Balances {
		value, ok := new(big.Int).SetString(balance, 10)
		if !ok {
			return core.StateTransferMsg{}, fmt.Errorf("invalid balance for %q: %q", account, balance)
		}
		out.Balances[account] = value
	}
	return out, nil
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
		View:            msg.View,
		SeqNum:          msg.SeqNum,
		DigestClientMsg: digestToPB(msg.DigestClientMsg),
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
	return core.PreprepareMsgMini{
		View:            msg.View,
		SeqNum:          msg.SeqNum,
		DigestClientMsg: digest,
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

func PreparedCertToPB(msg core.PreparedCert) *PreparedCert {
	out := &PreparedCert{
		PreprepareMsg: PreprepareMsgSigToPB(msg.PreprepareMsg),
		PrepareLog:    make(map[int32]*PrepareMsgSig, len(msg.PrepareLog)),
	}
	for k, v := range msg.PrepareLog {
		out.PrepareLog[int32(k)] = PrepareMsgSigToPB(v)
	}
	return out
}

func PreparedCertFromPB(msg *PreparedCert) (core.PreparedCert, error) {
	if msg == nil {
		return core.PreparedCert{}, nil
	}
	pp, err := PreprepareMsgSigFromPB(msg.PreprepareMsg)
	if err != nil {
		return core.PreparedCert{}, err
	}
	out := core.PreparedCert{
		PreprepareMsg: pp,
		PrepareLog:    make(map[int]*core.PrepareMsgSig, len(msg.PrepareLog)),
	}
	for k, v := range msg.PrepareLog {
		p, err := PrepareMsgSigFromPB(v)
		if err != nil {
			return core.PreparedCert{}, err
		}
		out.PrepareLog[int(k)] = p
	}
	return out, nil
}

func ViewChangeToPB(msg core.ViewChangeMsg) *ViewChangeMsg {
	out := &ViewChangeMsg{
		ViewNumber:          msg.ViewNumber,
		CheckpointSeqNumber: msg.CheckpointSeqNumber,
		CheckpointDigest:    digestToPB(msg.CheckpointDigest),
		From:                int32(msg.From),
		PreparedCerts:       make(map[int64]*PreparedCert, len(msg.PreparedCerts)),
		VcType:              ViewChangeMsg_VCType(msg.Type),
	}
	if len(msg.CheckpointProof) > 0 {
		out.CheckpointProof = make([]*CheckpointMsgSig, 0, len(msg.CheckpointProof))
		for _, proof := range msg.CheckpointProof {
			out.CheckpointProof = append(out.CheckpointProof, CheckpointMsgSigToPB(proof))
		}
	}
	for k, v := range msg.PreparedCerts {
		if v == nil {
			out.PreparedCerts[k] = nil
			continue
		}
		out.PreparedCerts[k] = PreparedCertToPB(*v)
	}

	switch msg.Type {
	case core.VCTypeElection:
		if msg.ElectionData != nil {
			out.VcData = &ViewChangeMsg_Election{
				Election: &ElectionVCData{
					ReqVote:   msg.ElectionData.ReqVote,
					GrantVote: msg.ElectionData.GrantVote,
					GrantTo:   int32(msg.ElectionData.GrantTo),
				},
			}
		}
	case core.VCTypeRoundRobin:
		if msg.RoundRobinData != nil {
			out.VcData = &ViewChangeMsg_RoundRobin{
				RoundRobin: &RoundRobinVCData{
					GrantVote: msg.RoundRobinData.GrantVote,
				},
			}
		}
	case core.VCTypeWRR:
		if msg.WRRData != nil {
			out.VcData = &ViewChangeMsg_Wrr{
				Wrr: &WRRVCData{
					Throughput: msg.WRRData.Throughput,
				},
			}
		}
	}

	return out
}

func ViewChangeFromPB(msg *ViewChangeMsg) (core.ViewChangeMsg, error) {
	if msg == nil {
		return core.ViewChangeMsg{}, nil
	}
	checkpointDigest, err := digestFromPB(msg.CheckpointDigest)
	if err != nil {
		return core.ViewChangeMsg{}, err
	}
	out := core.ViewChangeMsg{
		ViewNumber:          msg.ViewNumber,
		CheckpointSeqNumber: msg.CheckpointSeqNumber,
		CheckpointDigest:    checkpointDigest,
		From:                int(msg.From),
		PreparedCerts:       make(map[int64]*core.PreparedCert, len(msg.PreparedCerts)),
		Type:                core.VCType(msg.VcType),
	}
	if len(msg.CheckpointProof) > 0 {
		out.CheckpointProof = make([]core.CheckpointMsgSig, 0, len(msg.CheckpointProof))
		for _, proof := range msg.CheckpointProof {
			checkpointProof, err := CheckpointMsgSigFromPB(proof)
			if err != nil {
				return core.ViewChangeMsg{}, err
			}
			out.CheckpointProof = append(out.CheckpointProof, checkpointProof)
		}
	}
	for k, v := range msg.PreparedCerts {
		if v == nil {
			out.PreparedCerts[k] = nil
			continue
		}
		cert, err := PreparedCertFromPB(v)
		if err != nil {
			return core.ViewChangeMsg{}, err
		}
		out.PreparedCerts[k] = &cert
	}

	switch data := msg.VcData.(type) {
	case *ViewChangeMsg_Election:
		out.ElectionData = &core.ElectionVCData{
			ReqVote:   data.Election.ReqVote,
			GrantVote: data.Election.GrantVote,
			GrantTo:   int(data.Election.GrantTo),
		}
	case *ViewChangeMsg_RoundRobin:
		out.RoundRobinData = &core.RoundRobinVCData{
			GrantVote: data.RoundRobin.GrantVote,
		}
	case *ViewChangeMsg_Wrr:
		out.WRRData = &core.WRRVCData{
			Throughput: data.Wrr.Throughput,
		}
	case nil:
	default:
		return core.ViewChangeMsg{}, fmt.Errorf("unknown view change data type %T", data)
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
	vc, err := ViewChangeFromPB(msg.ViewChangeMsg)
	if err != nil {
		return core.ViewChangeMsgSig{}, err
	}
	return core.ViewChangeMsgSig{
		ViewChangeMsg: vc,
		Signature:     append([]byte(nil), msg.Signature...),
	}, nil
}

func GrantVoteToPB(msg core.GrantVoteMsg) *GrantVoteMsg {
	return &GrantVoteMsg{
		ViewNumber: msg.ViewNumber,
		From:       int32(msg.From),
	}
}

func GrantVoteFromPB(msg *GrantVoteMsg) (core.GrantVoteMsg, error) {
	if msg == nil {
		return core.GrantVoteMsg{}, nil
	}
	return core.GrantVoteMsg{
		ViewNumber: msg.ViewNumber,
		From:       int(msg.From),
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
	vote, err := GrantVoteFromPB(msg.GrantVoteMsg)
	if err != nil {
		return core.GrantVoteMsgSig{}, err
	}
	return core.GrantVoteMsgSig{
		GrantVoteMsg: vote,
		Signature:    append([]byte(nil), msg.Signature...),
	}, nil
}

func NewViewToPB(msg core.NewViewMsg) *NewViewMsg {
	out := &NewViewMsg{
		PreprepareLog: make([]*PreprepareMsgSig, 0, len(msg.PreprepareLog)),
		ViewChangeLog: make([]*ViewChangeMsgSig, 0, len(msg.ViewChangeLog)),
		NewViewNumber: msg.NewViewNumber,
		From:          int32(msg.From),
		Throughput:    msg.Throughput,
	}
	for _, p := range msg.PreprepareLog {
		out.PreprepareLog = append(out.PreprepareLog, PreprepareMsgSigToPB(p))
	}
	for _, vc := range msg.ViewChangeLog {
		if vc == nil {
			out.ViewChangeLog = append(out.ViewChangeLog, nil)
			continue
		}
		out.ViewChangeLog = append(out.ViewChangeLog, ViewChangeMsgSigToPB(*vc))
	}
	return out
}

func NewViewFromPB(msg *NewViewMsg) (core.NewViewMsg, error) {
	if msg == nil {
		return core.NewViewMsg{}, nil
	}
	out := core.NewViewMsg{
		PreprepareLog: make([]core.PreprepareMsgSig, 0, len(msg.PreprepareLog)),
		ViewChangeLog: make([]*core.ViewChangeMsgSig, 0, len(msg.ViewChangeLog)),
		NewViewNumber: msg.NewViewNumber,
		From:          int(msg.From),
		Throughput:    msg.Throughput,
	}
	for _, p := range msg.PreprepareLog {
		preprepare, err := PreprepareMsgSigFromPB(p)
		if err != nil {
			return core.NewViewMsg{}, err
		}
		out.PreprepareLog = append(out.PreprepareLog, preprepare)
	}
	for _, vc := range msg.ViewChangeLog {
		if vc == nil {
			out.ViewChangeLog = append(out.ViewChangeLog, nil)
			continue
		}
		viewChangeSig, err := ViewChangeMsgSigFromPB(vc)
		if err != nil {
			return core.NewViewMsg{}, err
		}
		v := viewChangeSig
		out.ViewChangeLog = append(out.ViewChangeLog, &v)
	}
	return out, nil
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

func IntentToChangeViewToPB(msg core.IntentToChangeViewMsg) *IntentToChangeViewMsg {
	return &IntentToChangeViewMsg{
		ViewNumber: msg.ViewNumber,
		From:       int32(msg.From),
	}
}

func IntentToChangeViewFromPB(msg *IntentToChangeViewMsg) (core.IntentToChangeViewMsg, error) {
	if msg == nil {
		return core.IntentToChangeViewMsg{}, nil
	}
	return core.IntentToChangeViewMsg{
		ViewNumber: msg.ViewNumber,
		From:       int(msg.From),
	}, nil
}
