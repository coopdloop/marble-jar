package auth

import (
	"strings"
	"testing"
	"time"
)

func TestLoginCodeIsSingleUse(t *testing.T) {
	s := NewLoginCodeStore()
	code, err := s.Issue("user-1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if len(code) < 32 {
		t.Fatalf("code looks too short to be unguessable: %q", code)
	}

	userID, ok := s.Consume(code)
	if !ok || userID != "user-1" {
		t.Fatalf("consume: got (%q, %v)", userID, ok)
	}
	if _, ok := s.Consume(code); ok {
		t.Fatal("expected the code to be spent")
	}
}

func TestLoginCodeRejectsUnknownAndEmpty(t *testing.T) {
	s := NewLoginCodeStore()
	for _, code := range []string{"", "not-a-real-code", strings.Repeat("A", 43)} {
		if _, ok := s.Consume(code); ok {
			t.Fatalf("expected %q to be rejected", code)
		}
	}
}

func TestLoginCodeExpires(t *testing.T) {
	s := NewLoginCodeStore()
	code, err := s.Issue("user-2")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	s.mu.Lock()
	entry := s.codes[code]
	entry.expiresAt = time.Now().Add(-time.Second)
	s.codes[code] = entry
	s.mu.Unlock()

	if _, ok := s.Consume(code); ok {
		t.Fatal("expected an expired code to be rejected")
	}
}

func TestLoginCodesDifferPerIssue(t *testing.T) {
	s := NewLoginCodeStore()
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		code, err := s.Issue("user")
		if err != nil {
			t.Fatalf("issue: %v", err)
		}
		if seen[code] {
			t.Fatalf("duplicate login code issued: %s", code)
		}
		seen[code] = true
	}
}
