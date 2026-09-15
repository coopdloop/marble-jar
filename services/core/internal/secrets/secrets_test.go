package secrets

import (
	"encoding/base64"
	"strings"
	"testing"
)

func testBox(t *testing.T) *Box {
	t.Helper()
	key := make([]byte, keyBytes)
	for i := range key {
		key[i] = byte(i * 7)
	}
	box, err := NewKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return box
}

func TestSealOpenRoundTrip(t *testing.T) {
	box := testBox(t)
	const token = "xoxe.1.AAAA-bbbb.slack-access-token"

	sealed := box.Seal(token)
	if sealed == token {
		t.Fatal("Seal returned the plaintext unchanged")
	}
	if strings.Contains(sealed, "AAAA-bbbb") {
		t.Fatalf("ciphertext leaks the token: %s", sealed)
	}

	plain, upgraded, err := box.Open(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if plain != token {
		t.Fatalf("Open = %q, want %q", plain, token)
	}
	if upgraded {
		t.Fatal("a sealed value must not need upgrading")
	}

	// A fresh nonce per call means sealing twice gives different ciphertext that
	// both open to the same secret.
	second := box.Seal(token)
	if second == sealed {
		t.Fatal("nonce reuse: identical ciphertext for the same plaintext")
	}
	if p2, _, err := box.Open(second); err != nil || p2 != token {
		t.Fatalf("second seal opened to %q, %v", p2, err)
	}
}

func TestOpenLegacyPlaintextNeedsUpgrade(t *testing.T) {
	box := testBox(t)

	plain, upgraded, err := box.Open("xoxp-legacy-token")
	if err != nil {
		t.Fatal(err)
	}
	if plain != "xoxp-legacy-token" {
		t.Fatalf("legacy value altered: %q", plain)
	}
	if !upgraded {
		t.Fatal("plaintext must be reported as needing upgrade")
	}
}

func TestWrongKeyFailsLoudly(t *testing.T) {
	box := testBox(t)
	sealed := box.Seal("secret-token")

	other, err := NewKey(make([]byte, keyBytes))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := other.Open(sealed); err == nil {
		t.Fatal("opening with the wrong key must fail, not return junk")
	}

	// Tampering with the ciphertext is the same class of failure.
	tampered := sealed[:len(sealed)-3] + "AAA"
	if _, _, err := box.Open(tampered); err == nil {
		t.Fatal("tampered ciphertext must not open")
	}
}

func TestNilBoxPassesThrough(t *testing.T) {
	var box *Box // no OAUTH_TOKEN_KEY configured

	if got := box.Seal("plain-token"); got != "plain-token" {
		t.Fatalf("nil box Seal = %q, want unchanged", got)
	}
	plain, upgraded, err := box.Open("plain-token")
	if err != nil || plain != "plain-token" || upgraded {
		t.Fatalf("nil box Open = %q, %v, %v", plain, upgraded, err)
	}
	if box.Sealed("mjv1.whatever") {
		t.Fatal("nil box has nothing sealed")
	}
}

func TestEmptyValuesPassThrough(t *testing.T) {
	box := testBox(t)
	if got := box.Seal(""); got != "" {
		t.Fatalf("Seal(\"\") = %q", got)
	}
	plain, upgraded, err := box.Open("")
	if err != nil || plain != "" || upgraded {
		t.Fatalf("Open(\"\") = %q, %v, %v", plain, upgraded, err)
	}
}

func TestNewFromConfigString(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := base64.StdEncoding.DecodeString(key); err != nil {
		t.Fatalf("GenerateKey is not base64: %v", err)
	}

	box, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	if box == nil {
		t.Fatal("expected a box for a real key")
	}
	if p, _, err := box.Open(box.Seal("tok")); err != nil || p != "tok" {
		t.Fatalf("round trip through config string failed: %q %v", p, err)
	}

	// Blank config means "not configured", not "error at boot".
	for _, blank := range []string{"", "   "} {
		nilBox, err := New(blank)
		if err != nil || nilBox != nil {
			t.Fatalf("New(%q) = %v, %v; want nil box, no error", blank, nilBox, err)
		}
	}

	if _, err := New("not-base64!!"); err == nil {
		t.Fatal("invalid base64 must be a config error")
	}
	if _, err := New(base64.StdEncoding.EncodeToString([]byte("short"))); err == nil {
		t.Fatal("a short key must be rejected")
	}
}
