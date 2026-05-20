package state

import (
	"encoding/json"
	"fmt"
)

type HistoryRecord struct {
	Config map[string]json.RawMessage `json:"config"`
}

func ParseStackState(data []byte) (*StackState, error) {
	var s StackState
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parsing stack state: %w", err)
	}
	return &s, nil
}

func ParseHistoryRecord(data []byte) (*HistoryRecord, error) {
	var h HistoryRecord
	if err := json.Unmarshal(data, &h); err != nil {
		return nil, fmt.Errorf("parsing history record: %w", err)
	}
	return &h, nil
}

func ExtractHistorySecrets(h *HistoryRecord) []Secret {
	var secrets []Secret
	for key, raw := range h.Config {
		var obj struct {
			Secure string `json:"secure"`
		}
		if err := json.Unmarshal(raw, &obj); err != nil || obj.Secure == "" {
			continue
		}
		secrets = append(secrets, Secret{
			Name:       key,
			Source:     "config",
			Ciphertext: obj.Secure,
		})
	}
	return secrets
}
