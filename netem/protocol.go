package netem

const ProtocolVersion = 1

type ExecutionEventRequest struct {
	Version int    `json:"version"`
	Type    string `json:"type"`
	RuleID  string `json:"rule_id"`
	NodeID  int    `json:"node_id"`
	Seq     int64  `json:"seq"`
}

type EventResponse struct {
	Version   int    `json:"version"`
	RuleID    string `json:"rule_id,omitempty"`
	NodeID    int    `json:"node_id,omitempty"`
	Seq       int64  `json:"seq,omitempty"`
	Accepted  bool   `json:"accepted"`
	Applied   bool   `json:"applied"`
	Duplicate bool   `json:"duplicate,omitempty"`
	Error     string `json:"error,omitempty"`
}
