package attacks

import (
	"encoding/json"
	"fmt"
	"os"
)

// LoadScenario loads an attack scenario from a JSON file
func LoadScenario(path string) (*Scenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read attacks config: %w", err)
	}
	var s Scenario
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse attacks config: %w", err)
	}
	return &s, nil
}
