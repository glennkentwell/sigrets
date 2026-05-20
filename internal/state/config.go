package state

import (
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

type StackConfig struct {
	Config map[string]yaml.Node `yaml:"config,omitempty"`
}

type ConfigSecret struct {
	Secure string `yaml:"secure"`
}

func ParseStackConfig(data []byte) (*StackConfig, error) {
	var cfg StackConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing stack config: %w", err)
	}
	return &cfg, nil
}

func ParseStackState(data []byte) (*StackState, error) {
	var s StackState
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parsing stack state: %w", err)
	}
	return &s, nil
}

func ExtractConfigSecrets(cfg *StackConfig) []Secret {
	var secrets []Secret
	for key, node := range cfg.Config {
		var cs ConfigSecret
		if err := node.Decode(&cs); err != nil || cs.Secure == "" {
			continue
		}
		secrets = append(secrets, Secret{
			Name:       key,
			Source:     "config",
			Ciphertext: cs.Secure,
		})
	}
	return secrets
}
