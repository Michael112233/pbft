package core

import (
	"math/big"
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
	ActualMsg         []ClientMsgSignature
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

type CheckpointMsg struct {
	SeqNum int64
	Digest [32]byte
	From   int
}

type CheckpointMsgSig struct {
	CheckpointMsg CheckpointMsg
	Signature     []byte
}

type PreparedCert struct {
	PreprepareMsg PreprepareMsgSig
	PrepareLog    map[int]PrepareMsgSig
}

type VCType int

const (
	VCTypeElection VCType = iota + 1
	VCTypeRoundRobin
	VCTypeWRR
)

type ElectionVCData struct {
	ReqVote   bool
	GrantVote bool
	GrantTo   int
}
type RoundRobinVCData struct {
	GrantVote bool
}
type WRRVCData struct {
	Throughput float64
}

type ViewChangeMsg struct {
	ViewNumber          int64
	CheckpointSeqNumber int64
	CheckpointDigest    [32]byte
	CheckpointProof     []CheckpointMsgSig
	CheckpointBalances  map[string]*big.Int
	From                int
	PreparedCerts       map[int64]*PreparedCert
	Type                VCType
	ElectionData        *ElectionVCData
	RoundRobinData      *RoundRobinVCData
	WRRData             *WRRVCData
}

type ViewChangeMsgSig struct {
	ViewChangeMsg ViewChangeMsg
	Signature     []byte
}

type NewViewMsg struct {
	PreprepareLog []PreprepareMsgSig
	ViewChangeLog []*ViewChangeMsgSig
	NewViewNumber int64
	Throughput    float64
	From          int
}

type NewViewMsgSig struct {
	NewViewMsg NewViewMsg
	Signature  []byte
}
