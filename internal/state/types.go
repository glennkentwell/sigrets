package state

import (
	"encoding/json"
	"fmt"
)

type StackState struct {
	Version    int        `json:"version"`
	Checkpoint Checkpoint `json:"checkpoint"`
}

type Checkpoint struct {
	Latest *Deployment `json:"latest"`
}

type Deployment struct {
	SecretsProviders *SecretsProvider `json:"secrets_providers"`
	Resources        []Resource       `json:"resources"`
}

// SecretsProvider describes the encryption backend used for this stack.
type SecretsProvider struct {
	Type  string          `json:"type"`  // "cloud", "passphrase", "service"
	State json.RawMessage `json:"state"` // provider-specific state
}

// CloudSecretsState is the state for type="cloud" (AWS KMS, Azure Key Vault, GCP KMS, etc.).
// The EncryptedKey is the KMS-encrypted 32-byte data key, stored as raw bytes in JSON.
type CloudSecretsState struct {
	URL          string `json:"url"`          // e.g. "awskms://alias/my-key?region=ap-southeast-2&awssdk=v2"
	EncryptedKey []byte `json:"encryptedkey"` // KMS-encrypted plaintext data key
}

// Resource is a single Pulumi resource in the deployment snapshot.
type Resource struct {
	URN     string                     `json:"urn"`
	Type    string                     `json:"type"`
	Inputs  map[string]json.RawMessage `json:"inputs,omitempty"`
	Outputs map[string]json.RawMessage `json:"outputs,omitempty"`
}

// SecretSig is the property signature that marks a value as a Pulumi secret.
const SecretSig = "4dabf18193072939515e22adb298388d"

// SecretSigValue is the expected value of the signature property.
const SecretSigValue = "1b47061264138c4ac30d75fd1eb44270"

// RawSecret is the JSON representation of an encrypted or plaintext secret in state.
type RawSecret struct {
	Sig        string `json:"4dabf18193072939515e22adb298388d"`
	Ciphertext string `json:"ciphertext,omitempty"`
	Plaintext  string `json:"plaintext,omitempty"`
}

// Secret is a resolved secret with its location and value.
type Secret struct {
	// Name is the property key this secret came from.
	Name string
	// Source is "output" or "config".
	Source string
	// Ciphertext is the raw "v1:<nonce>:<ct>" string from state (empty if already plaintext).
	Ciphertext string
	// Value is the decrypted plaintext (populated after decryption).
	Value string
}

func ExtractCloudState(s *StackState) (CloudSecretsState, error) {
	if s.Checkpoint.Latest == nil {
		return CloudSecretsState{}, fmt.Errorf("stack has no deployment (empty checkpoint)")
	}
	sp := s.Checkpoint.Latest.SecretsProviders
	if sp == nil {
		return CloudSecretsState{}, fmt.Errorf("stack has no secrets provider")
	}
	if sp.Type != "cloud" {
		return CloudSecretsState{}, fmt.Errorf("unsupported secrets provider %q (only \"cloud\"/KMS supported)", sp.Type)
	}
	var cs CloudSecretsState
	if err := json.Unmarshal(sp.State, &cs); err != nil {
		return CloudSecretsState{}, fmt.Errorf("parsing cloud secrets state: %w", err)
	}
	return cs, nil
}
