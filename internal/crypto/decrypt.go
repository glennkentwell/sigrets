package crypto

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gocloud.dev/secrets"
	_ "gocloud.dev/secrets/awskms"
)

type Decryptor struct {
	dataKey []byte
}

func NewDecryptor(ctx context.Context, kmsURL string, encryptedKey []byte, profile string) (*Decryptor, error) {
	if profile != "" {
		sep := "?"
		if strings.Contains(kmsURL, "?") {
			sep = "&"
		}
		kmsURL += sep + "profile=" + profile + "&awssdk=v2"
	}
	keeper, err := secrets.OpenKeeper(ctx, kmsURL)
	if err != nil {
		return nil, fmt.Errorf("opening kms keeper: %w", err)
	}
	defer keeper.Close()

	plaintext, err := keeper.Decrypt(ctx, encryptedKey)
	if err != nil {
		return nil, fmt.Errorf("decrypting data key via kms: %w", err)
	}
	if len(plaintext) != 32 {
		return nil, fmt.Errorf("unexpected data key length: got %d, want 32", len(plaintext))
	}
	return &Decryptor{dataKey: plaintext}, nil
}

// Decrypt decrypts a Pulumi secret ciphertext of the form "v1:<base64-nonce>:<base64-ciphertext>".
// The decrypted bytes are JSON-encoded, so they are unmarshalled before returning.
func Decrypt(key []byte, ciphertext string) (string, error) {
	parts := strings.Split(ciphertext, ":")
	if len(parts) != 3 || parts[0] != "v1" {
		return "", errors.New("invalid ciphertext format: expected v1:<nonce>:<ct>")
	}

	nonce, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("decoding nonce: %w", err)
	}

	ct, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil {
		return "", fmt.Errorf("decoding ciphertext: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("creating aes cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("creating gcm: %w", err)
	}

	if len(nonce) != gcm.NonceSize() {
		return "", fmt.Errorf("nonce size mismatch: got %d, want %d", len(nonce), gcm.NonceSize())
	}

	plaintext, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("aes-gcm decryption failed: %w", err)
	}

	var value any
	if err := json.Unmarshal(plaintext, &value); err != nil {
		return string(plaintext), nil
	}

	switch v := value.(type) {
	case string:
		return v, nil
	default:
		result, _ := json.Marshal(v)
		return string(result), nil
	}
}

func (d *Decryptor) Decrypt(ciphertext string) (string, error) {
	return Decrypt(d.dataKey, ciphertext)
}
