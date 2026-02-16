package core

type Message struct {
	MsgType   string
	Data      []byte
	Signature []byte
	From      int
}

type RequestMessage struct {
	// Timestamp int64
	// From string
	// To   string
	Txs []ClientMsgSignature
	// Id        int64
}

type ClientMsg struct {
	Id         int64
	Timestamp  int64
	Txn        *Transaction
	ClientName string
}

type ClientMsgSignature struct {
	Data      ClientMsg
	Signature []byte
}

// type PreprepareMessage struct {
// 	Timestamp      int64
// 	From           string
// 	To             string
// 	SequenceNumber int64
// 	ViewNumber     int64
// 	Digest         string
// 	RequestMessage *RequestMessage
// }

// type PrepareMessage struct {
// 	Timestamp      int64
// 	From           string
// 	To             string
// 	SequenceNumber int64
// 	ViewNumber     int64
// 	Digest         string
// 	RequestMessage *RequestMessage
// }

// type CommitMessage struct {
// 	Timestamp      int64
// 	From           string
// 	To             string
// 	SequenceNumber int64
// 	ViewNumber     int64
// 	Digest         string
// 	RequestMessage *RequestMessage
// }

type ReplyMessage struct {
	// Timestamp      int64
	// From           string
	To   string
	From string
	// SequenceNumber int64
	// ViewNumber     int64
	// Digest         string
	// RequestMessage *RequestMessage
	ClientMsg ClientMsg
}

type CloseMessage struct {
	Timestamp int64
	From      string
	To        string
}

// type ViewChangeMessage struct {
// 	Timestamp           int64
// 	From                string
// 	To                  string
// 	CheckpointSeqNumber int64
// 	ViewNumber          int64
// 	CheckpointMsgNumber int32
// 	HavePreparedList    map[int64]bool
// 	PreprepareMessages  map[int64][]*PreprepareMessage
// 	Mempool             []*Transaction
// }

// type NewViewMessage struct {
// 	Timestamp           int64
// 	From                string
// 	To                  string
// 	OngoingTxs          []*Transaction
// 	ViewNumber          int64
// 	CheckpointSeqNumber int64
// }

// type CheckpointMessage struct {
// 	Timestamp      int64
// 	From           string
// 	To             string
// 	SequenceNumber int64
// 	ViewNumber     int64
// 	Digest         string
// }

// type MempoolMsg struct {
// 	Mempool    []*Transaction
// 	From       string
// 	To         string
// 	ViewNumber int64
// }

// type HeartbeatMessage struct {
// 	Timestamp  int64
// 	From       string
// 	To         string
// 	ViewNumber int64
// 	LeaderAddr string
// }

// type RequestVoteData struct {
// 	ViewNumber int64
// 	From       string
// 	To         string
// }

// type RequestVoteResponseData struct {
// 	ViewNumber  int64
// 	From        string
// 	To          string
// 	VoteGranted bool
// }

// type AppendEntriesData struct {
// 	ViewNumber    int64
// 	VoteNumber    int64
// 	CurrentLeader int64
// 	To            string
// }

type PreprepareMsg struct {
	View   int64
	SeqNum int64
	// DigestClientMsg [32]byte
	ClientMsg ClientMsgSignature
	To        string
}

type PrepareMsg struct {
	View   int64
	SeqNum int64
	Digest [32]byte
	From   int
	To     string
}

type CommitMsg struct {
	View   int64
	SeqNum int64
	Digest [32]byte
	From   int
	To     string
}
