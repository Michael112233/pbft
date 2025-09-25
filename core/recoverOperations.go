package core

type Operation struct {
	// about sequence number
	minPrepareSeqNumber int64
	maxPrepareSeqNumber int64

	// preprepare message
	preprepareMsg *PreprepareMessage
}
