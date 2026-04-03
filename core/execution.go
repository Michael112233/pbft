package core

type ExecutionResult struct {
	Success        bool
	Error          string
	ExecutedSeqNum int64
}
