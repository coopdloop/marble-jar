// Package secrets seals third-party provider tokens before they are written to
// the database, so a dump of oauth_connections does not hand out live Slack,
// Atlassian or Entra credentials.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

// sealedPrefix marks a stored value as our ciphertext envelope. Anything without
// it predates encryption and is treated as plaintext waiting to be upgraded.
const sealedPrefix = "mjv1."

const keyBytes = 32

type Box struct {
	gcm cipher.AEAD
}

var ErrNotSealed = errors.New("value is not a sealed token")

// New returns a Box from a base64-encoded 32-byte key. An empty string yields a
// nil Box, which passes tokens through untouched so existing deployments keep
// working until they opt in.
func New(encoded string) (*Box, error) {
	trimmed := strings.TrimSpace(encoded)
	if trimmed == "" {
		return nil, nil
	}
	key, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil {
		return nil, fmt.Errorf("decode token key: %w", err)
	}
	return NewKey(key)
}

func NewKey(key []byte) (*Box, error) {
	if len(key) != keyBytes {
		return nil, fmt.Errorf("token key must be %d bytes, got %d", keyBytes, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{gcm: gcm}, nil
}

// Seal encrypts a token. A nil Box returns the input unchanged.
func (b *Box) Seal(plain string) string {
	if b == nil || plain == "" {
		return plain
	}
	nonce := make([]byte, b.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		// Randomness failing is not a reason to store a token in the clear.
		panic("secrets: crypto/rand unavailable: " + err.Error())
	}
	ct := b.gcm.Seal(nil, nonce, []byte(plain), nil)
	return sealedPrefix + base64.RawStdEncoding.EncodeToString(nonce) + "." +
		base64.RawStdEncoding.EncodeToString(ct)
}

// Open returns the plaintext behind a stored value, plus whether the value still
// needs upgrading because it predates encryption. A nil Box is a no-op.
func (b *Box) Open(stored string) (plain string, needsUpgrade bool, err error) {
	if b == nil || stored == "" {
		return stored, false, nil
	}
	if !strings.HasPrefix(stored, sealedPrefix) {
		return stored, true, nil
	}
	nonce, ct, err := split(sealedPrefix, stored)
	if err != nil {
		return "", false, err
	}
	plainBytes, err := b.gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		// Authentication failed: wrong key or tampered row. Never fall back to
		// returning the ciphertext as if it were a token.
		return "", false, fmt.Errorf("open sealed token: %w", err)
	}
	return string(plainBytes), false, nil
}

// Sealed reports whether a stored value carries the envelope.
func (b *Box) Sealed(stored string) bool {
	return b != nil && strings.HasPrefix(stored, sealedPrefix)
}

func split(prefix, stored string) (nonce, ct []byte, err error) {
	parts := strings.SplitN(strings.TrimPrefix(stored, prefix), ".", 2)
	if len(parts) != 2 {
		return nil, nil, ErrNotSealed
	}
	nonce, err = base64.RawStdEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, nil, fmt.Errorf("decode nonce: %w", err)
	}
	ct, err = base64.RawStdEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, nil, fmt.Errorf("decode ciphertext: %w", err)
	}
	return nonce, ct, nil
}

// GenerateKey returns a base64 32-byte key, for operators standing up an
// instance that seals tokens at rest.
func GenerateKey() (string, error) {
	key := make([]byte, keyBytes)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(key), nil
}
