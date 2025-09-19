package core

// MessageType represents the type of PBFT message
type MessageType int

const (
	PrePrepare MessageType = iota
	Prepare
	Commit
	Reply
	ViewChange
	NewView
)

// Message represents a PBFT protocol message
type Message struct {
	Type           MessageType
	ViewNumber     int64
	SequenceNumber int64
	Block          *Block

	From int64 // the node id of the sender
	To   int64 // the node id of the receiver
}
