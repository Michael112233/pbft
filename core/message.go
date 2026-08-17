package core

import (
	"time"
)

type Message struct {
	MsgType   string
	Data      []byte
	Signature []byte
	From      int
}

type RequestMessage struct {
	MsgType string
	Txs     []ClientMsgSignature
}
type VCRunningStatus struct {
	Txs       []ClientMsgSignature
	VCRunning bool
}

type ClientMsg struct {
	Id         int64
	Timestamp  time.Time
	Txn        Transaction
	ClientName string
	Padding    string
}

type ClientMsgReply struct {
	Id         int64
	Timestamp  time.Time
	Txn        Transaction
	ClientName string
}

type ClientMsgSignature struct {
	Data      ClientMsg
	Signature []byte
}

type ReplyMessage struct {
	To   string
	From string

	ClientMsg ClientMsg
	Result    ExecutionResult
}

type CommitTps struct {
	To        string
	From      string
	ClientMsg ClientMsgReply
}

type LeaderIdUpdate struct {
	To          string
	From        string
	NewLeaderId int
	View        int64
}

type CloseMessage struct {
	Timestamp int64
	From      string
	To        string
}

type PreprepareMsg struct {
	View                       int64
	SeqNum                     int64
	DigestClientMsg            [32]byte
	ClientMsg                  []ClientMsgSignature
	DigestIndividualClientMsgs [][32]byte
}

type PreprepareMsgMini struct {
	View                       int64
	SeqNum                     int64
	DigestClientMsg            [32]byte
	DigestIndividualClientMsgs [][32]byte
}
type PreprepareMsgSig struct { // used in VC
	PreprepareMsgMini PreprepareMsgMini
	Signature         []byte
	ActualMsg         ClientMsgSignature
}

type PrepareMsg struct {
	View   int64
	SeqNum int64
	Digest [32]byte
	From   int
	// To     string
}
type PrepareMsgSig struct {
	PrepareMsg PrepareMsg
	Signature  []byte
}
type CommitMsg struct {
	View   int64
	SeqNum int64
	Digest [32]byte
	From   int
}
