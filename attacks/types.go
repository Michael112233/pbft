package attacks

// TriggerType represents when an attack step should fire
type TriggerType string

const (
	TriggerOnStart        TriggerType = "on_start"
	TriggerTimeSinceStart TriggerType = "time_since_start"
)

// ActionType represents the effect to apply
type ActionType string

const (
	ActionDelayMessages ActionType = "delay_messages" // e.g., delay PRE_PREPARE from leader
	ActionDropMessages  ActionType = "drop_messages"  // e.g., drop NEW_VIEW
	ActionMuteNode      ActionType = "mute_node"      // temporarily mute a node by id
)

// MessageFilter selects PBFT message types and basic endpoints
type MessageFilter struct {
	Types   []string `json:"types,omitempty"`   // e.g., ["MsgPreprepareMessage"], ["MsgNewViewMessage"]
	Senders string   `json:"senders,omitempty"` // "leader" | "any"
}

// WhenClause describes the minimal condition to activate a step
type WhenClause struct {
	Trigger    TriggerType `json:"trigger"`
	StartMs    int64       `json:"start_ms,omitempty"`    // used for time_since_start
	DurationMs int64       `json:"duration_ms,omitempty"` // optional bounding window
}

// TargetSelector chooses explicit node ids
type TargetSelector struct {
	IDs []int64 `json:"ids,omitempty"`
}

// Step represents a single attack operation.
type Step struct {
	ID             string          `json:"id"`
	When           WhenClause      `json:"when"`
	What           ActionType      `json:"what"`
	MessageFilter  *MessageFilter  `json:"message_filter,omitempty"`
	TargetSelector *TargetSelector `json:"target_selector,omitempty"`
	Params         map[string]any  `json:"params,omitempty"`
}

// Scenario represents the attack plan.
type Scenario struct {
	Name  string `json:"name"`
	Seed  int64  `json:"seed"` // seed = experiment id for deterministic choices later
	Steps []Step `json:"steps"`
}
