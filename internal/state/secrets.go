package state

import (
	"encoding/json"
	"fmt"
)

func IsSecret(raw json.RawMessage) bool {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return false
	}
	sigRaw, ok := obj[SecretSig]
	if !ok {
		return false
	}
	var sigVal string
	if err := json.Unmarshal(sigRaw, &sigVal); err != nil {
		return false
	}
	return sigVal == SecretSigValue
}

func ParseRawSecret(raw json.RawMessage) (RawSecret, error) {
	var s RawSecret
	if err := json.Unmarshal(raw, &s); err != nil {
		return RawSecret{}, fmt.Errorf("parsing secret: %w", err)
	}
	return s, nil
}

func ExtractOutputSecrets(state *ProjectState) []Secret {
	var secrets []Secret
	if state.Checkpoint.Latest == nil {
		return secrets
	}
	for _, r := range state.Checkpoint.Latest.Resources {
		if r.Type != "pulumi:pulumi:Stack" {
			continue
		}
		for key, raw := range r.Outputs {
			if !IsSecret(raw) {
				continue
			}
			rs, err := ParseRawSecret(raw)
			if err != nil {
				continue
			}
			secrets = append(secrets, Secret{
				Name:       key,
				Source:     "output",
				Ciphertext: rs.Ciphertext,
				Value:      rs.Plaintext,
			})
		}
	}
	return secrets
}
