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

func PreprepareToPB(msg core.PreprepareMsg) *PreprepareMsg {
	return &PreprepareMsg{
		View:            msg.View,
		SeqNum:          msg.SeqNum,
		ClientMsg:       ClientMsgSigToPB(msg.ClientMsg),
		To:              msg.To,
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
		View:            msg.View,
		SeqNum:          msg.SeqNum,
		ClientMsg:       clientMsg,
		To:              msg.To,
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
		To:     msg.To,
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
		To:     msg.To,
	}, nil
}

func CommitToPB(msg core.CommitMsg) *CommitMsg {
	return &CommitMsg{
		View:   msg.View,
		SeqNum: msg.SeqNum,
		Digest: digestToPB(msg.Digest),
		From:   int32(msg.From),
		To:     msg.To,
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
		To:     msg.To,
	}, nil
}

func ReplyToPB(msg core.ReplyMessage) *ReplyMessage {
	return &ReplyMessage{
		To:        msg.To,
		From:      msg.From,
		ClientMsg: ClientMsgToPB(msg.ClientMsg),
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
