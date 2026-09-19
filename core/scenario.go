package core

// Scenario is the fault injected into the run while scenario mode is on. The
// active scenario is a function of the generation (see node/scenario.go), so
// every node derives the same one without extra messages.
type Scenario int

const (
	ScenarioHealthy Scenario = iota
	ScenarioProposalDelay
	ScenarioNetworkDelay
)

func ScenarioToString(scenario Scenario) string {
	switch scenario {
	case ScenarioHealthy:
		return "Healthy"
	case ScenarioProposalDelay:
		return "ProposalDelay"
	case ScenarioNetworkDelay:
		return "NetworkDelay"
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
	default:
		return ScenarioHealthy, false
	}
}
