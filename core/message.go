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
}

type PreprepareMessage struct {
	Timestamp      int64
	From           string
	To             string
	SequenceNumber int64
	ViewNumber     int64
	RequestMessage *RequestMessage
}
