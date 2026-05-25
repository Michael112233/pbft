package core

const (
	MsgRequestMessage         string = "MsgRequestMessage"
	MsgPreprepareMessage      string = "MsgPreprepareMessage"
	MsgPrepareMessage         string = "MsgPrepareMessage"
	MsgCommitMessage          string = "MsgCommitMessage"
	MsgReplyMessage           string = "MsgReplyMessage"
	MsgCommitTpsMessage       string = "MsgCommitTpsMessage"
	MsgLeaderIdUpdateMessage  string = "MsgLeaderIdUpdateMessage"
	MsgVCRunningStatusMessage string = "MsgVCRunningStatusMessage"
	MsgCloseMessage           string = "MsgCloseMessage"
	MsgViewChangeMessage      string = "MsgViewChangeMessage"
	MsgCheckpointMessage      string = "MsgCheckpointMessage"
	MsgRequestStateTransfer   string = "MsgRequestStateTransfer"
	MsgStateTransfer          string = "MsgStateTransfer"
	MsgNewViewMessage         string = "MsgNewViewMessage"
	MsgMempoolMessage         string = "MsgMempoolMessage"

	MsgRequestVote         string = "MsgRequestVote"
	MsgRequestVoteResponse string = "MsgRequestVoteResponse"
	MsgAppendEntries       string = "MsgAppendEntries"
	MsgHeartbeatMessage    string = "MsgHeartbeatMessage"
	MsgGrantVoteMessage    string = "MsgGrantVoteMessage"
)
