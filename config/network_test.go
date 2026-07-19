package config

import "testing"

func TestGenerateLocalNetworkLearningAgentAddresses(t *testing.T) {
	GenerateLocalNetwork(4)

	if got := LearningAgentAddr[1]; got != "127.0.0.1:29001" {
		t.Fatalf("LearningAgentAddr[1] = %q, want %q", got, "127.0.0.1:29001")
	}
	if got := LearningAgentAddr[4]; got != "127.0.0.1:29004" {
		t.Fatalf("LearningAgentAddr[4] = %q, want %q", got, "127.0.0.1:29004")
	}
}

func TestGenerateLoopbackIPNetworkLearningAgentAddresses(t *testing.T) {
	GenerateLoopbackIPNetwork(4)

	if got := LearningAgentAddr[1]; got != "127.0.0.2:29000" {
		t.Fatalf("LearningAgentAddr[1] = %q, want %q", got, "127.0.0.2:29000")
	}
	if got := LearningAgentAddr[4]; got != "127.0.0.5:29000" {
		t.Fatalf("LearningAgentAddr[4] = %q, want %q", got, "127.0.0.5:29000")
	}
}

func TestGenerateRemoteNetworkDisablesLearningAgents(t *testing.T) {
	GenerateRemoteNetwork(4)

	if LearningAgentAddr != nil {
		t.Fatalf("LearningAgentAddr = %#v, want nil in remote mode", LearningAgentAddr)
	}
}
