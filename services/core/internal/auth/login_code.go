package auth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
	"time"
)

// LoginCodeTTL bounds how long a browser may spend carrying the one-time code
// from the Google callback back to the dashboard.
const LoginCodeTTL = 60 * time.Second

// maxPendingCodes caps the store so a flood of callbacks cannot grow memory
// without bound.
const maxPendingCodes = 4096

var ErrTooManyLoginCodes = errors.New("too many pending login codes")

type loginCode struct {
	userID    string
	expiresAt time.Time
}

// LoginCodeStore hands out the single-use codes that move a finished Google
// redirect into an API-issued session. Tokens stay server-side; only the code
// crosses the browser.
//
// State is per-process: with more than one core replica, back this with Redis.
type LoginCodeStore struct {
	mu    sync.Mutex
	codes map[string]loginCode
}

func NewLoginCodeStore() *LoginCodeStore {
	return &LoginCodeStore{codes: map[string]loginCode{}}
}

func (s *LoginCodeStore) Issue(userID string) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	code := base64.RawURLEncoding.EncodeToString(buf)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()
	if len(s.codes) >= maxPendingCodes {
		return "", ErrTooManyLoginCodes
	}
	s.codes[code] = loginCode{userID: userID, expiresAt: time.Now().Add(LoginCodeTTL)}
	return code, nil
}

// Consume redeems a code. A code is valid for exactly one redemption.
func (s *LoginCodeStore) Consume(code string) (string, bool) {
	if code == "" {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.codes[code]
	if !ok {
		return "", false
	}
	delete(s.codes, code)
	if time.Now().After(entry.expiresAt) {
		return "", false
	}
	return entry.userID, true
}

func (s *LoginCodeStore) sweepLocked() {
	now := time.Now()
	for code, entry := range s.codes {
		if now.After(entry.expiresAt) {
			delete(s.codes, code)
		}
	}
}
