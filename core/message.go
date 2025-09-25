package core

type Message struct {
	MsgType string
	Data    []byte
}

type RequestMessage struct {
	Timestamp int64
	From      string
	To        string
	Txs       []*Transaction
	Id        int64
}

type PreprepareMessage struct {
	Timestamp      int64
	From           string
	To             string
	SequenceNumber int64
	ViewNumber     int64
	Digest         string
	RequestMessage *RequestMessage
}

type PrepareMessage struct {
	Timestamp      int64
	From           string
	To             string
	SequenceNumber int64
	ViewNumber     int64
	Digest         string
	RequestMessage *RequestMessage
}

type CommitMessage struct {
	Timestamp      int64
	From           string
	To             string
	SequenceNumber int64
	ViewNumber     int64
	Digest         string
	RequestMessage *RequestMessage
}

type ReplyMessage struct {
	Timestamp      int64
	From           string
	To             string
	SequenceNumber int64
	ViewNumber     int64
	Digest         string
	RequestMessage *RequestMessage
}

type CloseMessage struct {
	Timestamp int64
	From      string
	To        string
}

type ViewChangeMessage struct {
	Timestamp           int64
	From                string
	To                  string
	CheckpointSeqNumber int64
	ViewNumber          int64
	CheckpointMsgNumber int32
	HavePreparedList    map[int64]bool
}

type CheckpointMessage struct {
	Timestamp      int64
	From           string
	To             string
	SequenceNumber int64
	Digest         string
}
