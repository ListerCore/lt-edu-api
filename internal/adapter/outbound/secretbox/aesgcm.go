// Package secretbox encrypts secrets at rest (TOTP seeds) with AES-256-GCM. The key lives in the environment, never in the database.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

var errShortCiphertext = errors.New("ciphertext too short")

type AESGCM struct {
	aead    cipher.AEAD
	version int
}

// New takes the base64 key from APP__SECRETS__KEY (32 bytes once decoded). An empty key returns a box that
// refuses every operation, so the API still starts; TOTP enrollment then answers that it is not configured.
func New(base64Key string, version int) (*AESGCM, error) {
	if base64Key == "" {
		return &AESGCM{version: version}, nil
	}
	key, err := base64.StdEncoding.DecodeString(base64Key)
	if err != nil {
		return nil, fmt.Errorf("APP__SECRETS__KEY is not valid base64: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("APP__SECRETS__KEY must decode to 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &AESGCM{aead: aead, version: version}, nil
}

func (b *AESGCM) Ready() bool     { return b.aead != nil }
func (b *AESGCM) KeyVersion() int { return b.version }

// Encrypt returns nonce || ciphertext
func (b *AESGCM) Encrypt(plaintext, associatedData []byte) ([]byte, error) {
	if b.aead == nil {
		return nil, errors.New("secrets key not configured")
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return b.aead.Seal(nonce, nonce, plaintext, associatedData), nil
}

func (b *AESGCM) Decrypt(ciphertext, associatedData []byte) ([]byte, error) {
	if b.aead == nil {
		return nil, errors.New("secrets key not configured")
	}
	size := b.aead.NonceSize()
	if len(ciphertext) < size {
		return nil, errShortCiphertext
	}
	return b.aead.Open(nil, ciphertext[:size], ciphertext[size:], associatedData)
}
