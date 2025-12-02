package attacks

import (
	"strings"
	"sync"
	"time"
)

// Global, read-mostly state so send paths can make quick decisions
var (
	globalMu       sync.RWMutex
	globalScenario *Scenario
	globalStart    time.Time
)

// registerScenario makes the scenario visible to send hooks and sets the start time anchor
func registerScenario(s *Scenario, start time.Time) {
	globalMu.Lock()
	defer globalMu.Unlock()
	globalScenario = s
	globalStart = start
}

// EvaluateSend decides whether to delay or drop a message based on the current scenario
func EvaluateSend(msgType string, fromNodeID int64) (time.Duration, bool) {
	globalMu.RLock()
	s := globalScenario
	start := globalStart
	globalMu.RUnlock()
	if s == nil {
		return 0, false
	}
	elapsedMs := time.Since(start).Milliseconds()

	var maxDelayMs int64
	var shouldDrop bool
	for _, step := range s.Steps {
		active := false
		switch step.When.Trigger {
		case TriggerOnStart:
			active = true
		case TriggerTimeSinceStart:
			if elapsedMs >= step.When.StartMs {
				active = true
			}
		default:
			continue
		}

		if step.When.DurationMs > 0 && elapsedMs > (step.When.StartMs+step.When.DurationMs) {
			active = false
		}
		if !active {
			continue
		}

		switch step.What {
		case ActionDelayMessages:
			if matchMessageFilter(msgType, step.MessageFilter) {
				d := getInt64Param(step.Params, "delay_ms")
				if d > 0 {
					maxDelayMs = d
				}
			}
		case ActionDropMessages:
			if matchMessageFilter(msgType, step.MessageFilter) {
				shouldDrop = true
			}
		case ActionMuteNode:
			if isNodeInSelector(fromNodeID, step.TargetSelector) {
				shouldDrop = true
			}
		default:
			continue
		}
	}

	return time.Duration(maxDelayMs) * time.Millisecond, shouldDrop
}

func isNodeInSelector(nodeID int64, sel *TargetSelector) bool {
	if sel == nil {
		return false
	}
	for _, id := range sel.IDs {
		if id == nodeID {
			return true
		}
	}
	return false
}

func matchMessageFilter(msgType string, f *MessageFilter) bool {
	if f == nil {
		return true
	}
	if len(f.Types) > 0 {
		found := false
		for _, t := range f.Types {
			if t == msgType {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	// sender check: "leader" matches PRE_PREPARE only, since only leaders send those
	switch strings.ToLower(f.Senders) {
	case "leader":
		return msgType == "MsgPreprepareMessage"
	case "any", "":
		return true
	default:
		return false
	}
}

func getInt64Param(m map[string]any, key string) int64 {
	if m == nil {
		return 0
	}
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch x := v.(type) {
	case float64:
		return int64(x)
	case int:
		return int64(x)
	case int64:
		return x
	case float32:
		return int64(x)
	default:
		return 0
	}
}
