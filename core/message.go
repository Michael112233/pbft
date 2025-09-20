package core

// MessageType represents the type of PBFT message
type MessageType int

type RequestMsg struct {
	Timestamp int64
	From      string
	To        string
	Txs       []*Transaction
}
