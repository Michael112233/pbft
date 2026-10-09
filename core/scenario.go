package core

// Scenario is the fault injected into the run while scenario mode is on. The
// active scenario is a function of the generation (see node/scenario.go), so
// every node derives the same one without extra messages.
type Scenario int

const (
	ScenarioHealthy Scenario = iota
	ScenarioProposalDelay
	ScenarioNetworkDelay
	// ScenarioNetworkDelayFCrash is NetworkDelay plus the nodes_dead nodes crashed.
	ScenarioNetworkDelayFCrash
	// ScenarioThrottle is targeted proposal throttling, driven by the client's
	// throttle manager (client/throttlemanager.go). Nodes inject nothing; they only
	// allow their proposal gate to be on while in this scenario.
	ScenarioThrottle
)

// ScenarioForGeneration maps a generation to its scenario: generations 1..span get
// list[0], the next span get list[1], and so on, cycling. Nodes and the client both
// use it, so they agree on the scenario of any generation without messages.
func ScenarioForGeneration(gen uint64, list []Scenario, span uint64) Scenario {
	return list[((gen-1)/span)%uint64(len(list))]
}

func ScenarioToString(scenario Scenario) string {
	switch scenario {
	case ScenarioHealthy:
		return "Healthy"
	case ScenarioProposalDelay:
		return "ProposalDelay"
	case ScenarioNetworkDelay:
		return "NetworkDelay"
	case ScenarioNetworkDelayFCrash:
		return "NetworkDelayFCrash"
	case ScenarioThrottle:
		return "Throttle"
	default:
		return "UnknownScenario"
	}
}

func StringToScenario(scenarioStr string) (Scenario, bool) {
	switch scenarioStr {
	case "Healthy":
		return ScenarioHealthy, true
	case "ProposalDelay":
		return ScenarioProposalDelay, true
	case "NetworkDelay":
		return ScenarioNetworkDelay, true
	case "NetworkDelayFCrash":
		return ScenarioNetworkDelayFCrash, true
	case "Throttle":
		return ScenarioThrottle, true
	default:
		return ScenarioHealthy, false
	}
}
