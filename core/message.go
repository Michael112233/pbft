package core

import (
	"math/big"
	"time"
)

type ViewID struct {
	Generation uint64
	Counter    uint64
}

func (v ViewID) Equal(other ViewID) bool {
	return v.Generation == other.Generation && v.Counter == other.Counter
}

func (v ViewID) NotEqual(other ViewID) bool {
	return !v.Equal(other)
}

func (v ViewID) GreaterThan(other ViewID) bool {
	if v.Generation != other.Generation {
		return v.Generation > other.Generation
	}
	return v.Counter > other.Counter
}

func (v ViewID) GreaterThanOrEqual(other ViewID) bool {
	return !v.LessThan(other)
}

func (v ViewID) LessThan(other ViewID) bool {
	if v.Generation != other.Generation {
		return v.Generation < other.Generation
	}
	return v.Counter < other.Counter
}

func (v ViewID) LessThanOrEqual(other ViewID) bool {
	return !v.GreaterThan(other)
}

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

type EventMsg struct {
	EventType string
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
	View        ViewID
}

type CloseMessage struct {
	Timestamp int64
	From      string
	To        string
}

type PreprepareMsg struct {
	// View                       int64
	SeqNum                     int64
	DigestClientMsg            [32]byte
	ClientMsg                  []ClientMsgSignature
	DigestIndividualClientMsgs [][32]byte
	View                       ViewID
}

type PreprepareMsgMini struct {
	// View                       int64
	SeqNum                     int64
	DigestClientMsg            [32]byte
	DigestIndividualClientMsgs [][32]byte
	View                       ViewID
}
type PreprepareMsgSig struct { // used in VC
	PreprepareMsgMini PreprepareMsgMini
	Signature         []byte
	ActualMsg         []ClientMsgSignature
}

type PrepareMsg struct {
	View   ViewID
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
	View   ViewID
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
	ViewNumber          ViewID
	CheckpointSeqNumber int64
	CheckpointDigest    [32]byte
	CheckpointProof     []CheckpointMsgSig
	CheckpointBalances  map[string]*big.Int
	From                int
	PreparedCerts       map[int64]*PreparedCert
	Action              Action
}

type ViewChangeMsgSig struct {
	ViewChangeMsg ViewChangeMsg
	Signature     []byte
}

type NewViewMsg struct {
	PreprepareLog []PreprepareMsgSig
	ViewChangeLog []*ViewChangeMsgSig
	NewViewNumber ViewID
	Throughput    float64
	From          int
}

type NewViewMsgSig struct {
	NewViewMsg NewViewMsg
	Signature  []byte
}

type RequestVoteMsg struct {
	From       int
	ViewNumber ViewID
	Seed       []byte
	DelaySteps uint64
	Y          []byte
	VDFProof   []byte
	VRFProof   []byte
}

type RequestVoteMsgSig struct {
	RequestVoteMsg RequestVoteMsg
	Signature      []byte
}

type GrantVoteMsg struct {
	From       int
	ViewNumber ViewID
}

type GrantVoteMsgSig struct {
	GrantVoteMsg GrantVoteMsg
	Signature    []byte
}
type EpochData struct {
	Throughput       float64
	ProposalInterval float64
	VCRate           float64
	InactiveNodes    uint8
}

type EpochDataMsg struct {
	EpochGeneration uint64
	From            int
}

type EpochDataMsgSig struct {
	EpochDataMsg EpochDataMsg
	Signature    []byte
}

type EpochAggregateMsgMini struct {
	EpochGeneration uint64
	From            int
	CurrentAction   Action
	EpochData       EpochData
}

type EpochAggregateMsg struct {
	EpochGeneration  uint64
	From             int
	EpochData        EpochData
	EpochDataMsgSigs []EpochDataMsgSig
	CurrentAction    Action
}
type TriggerMode int

const (
	PeriodicTrigger TriggerMode = iota
	PerfTrigger
	FixedTrigger
	NullTrigger
)

type Policy int

const (
	PolicyRoundRobin Policy = iota
	PolicyElection
)

type Action struct {
	TriggerMode TriggerMode
	Policy      Policy
}

var (
	FixedRoundRobin       Action = Action{TriggerMode: FixedTrigger, Policy: PolicyRoundRobin}
	PeriodicRoundRobin    Action = Action{TriggerMode: PeriodicTrigger, Policy: PolicyRoundRobin}
	PeriodicElection      Action = Action{TriggerMode: PeriodicTrigger, Policy: PolicyElection}
	PerformanceElection   Action = Action{TriggerMode: PerfTrigger, Policy: PolicyElection}
	PerformanceRoundRobin Action = Action{TriggerMode: PerfTrigger, Policy: PolicyRoundRobin}
)

func ActiontoString(action Action) string {
	switch action {
	case FixedRoundRobin:
		return "FixedRoundRobin"
	case PeriodicRoundRobin:
		return "PeriodicRoundRobin"
	case PeriodicElection:
		return "PeriodicElection"
	case PerformanceElection:
		return "PerformanceElection"
	case PerformanceRoundRobin:
		return "PerformanceRoundRobin"
	default:
		return "UnknownAction"
	}
}

func StringtoAction(actionStr string) Action {
	switch actionStr {
	case "FixedRoundRobin":
		return FixedRoundRobin
	case "PeriodicRoundRobin":
		return PeriodicRoundRobin
	case "PeriodicElection":
		return PeriodicElection
	case "PerformanceElection":
		return PerformanceElection
	case "PerformanceRoundRobin":
		return PerformanceRoundRobin
	default:
		return FixedRoundRobin // default action
	}
}

type LearningAgentDecision struct {
	NextProtocol Action
	Generation   uint64
}
