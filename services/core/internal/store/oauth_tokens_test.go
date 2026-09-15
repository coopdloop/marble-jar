package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/marble-jar/marble-jar/services/core/internal/secrets"
	"github.com/marble-jar/marble-jar/services/core/internal/store"
)

// A store built with a token box must never put a provider token on disk in
// plaintext, while every caller still sees the plaintext it can send to Slack.
func TestOAuthConnectionTokens_SealedAtRest(t *testing.T) {
	org := newOrg(t)
	user := newUser(t, org)
	ctx := context.Background()

	box, err := secrets.NewKey(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := store.New(ctx, testDSN, store.WithTokenBox(box))
	if err != nil {
		t.Fatal(err)
	}
	defer sealed.Close()

	const access, refresh = "xoxb-live-access-token", "xoxe-live-refresh-token"
	conn, err := sealed.UpsertOAuthConnection(ctx, org, user.ID, "slack", "U123",
		access, ptrStr(refresh), []string{"chat:write"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if conn.AccessToken != access || deref(conn.RefreshToken) != refresh {
		t.Fatalf("Upsert must hand back usable tokens, got %q / %q",
			conn.AccessToken, deref(conn.RefreshToken))
	}

	rawAccess, rawRefresh := rawConnectionTokens(t, conn.ID)
	if rawAccess == access || rawRefresh == refresh {
		t.Fatalf("plaintext token written to the database: %q / %q", rawAccess, rawRefresh)
	}
	if len(rawAccess) < 8 || rawAccess[:5] != "mjv1." {
		t.Fatalf("stored access token is not a sealed envelope: %q", rawAccess)
	}

	got, err := sealed.GetOAuthConnection(ctx, org, user.ID, "slack")
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != access || deref(got.RefreshToken) != refresh {
		t.Fatalf("GetOAuthConnection = %q / %q, want the plaintext tokens",
			got.AccessToken, deref(got.RefreshToken))
	}

	list, err := sealed.ListOAuthConnections(ctx, org, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].AccessToken != access {
		t.Fatalf("ListOAuthConnections = %+v", list)
	}
}

// Rows written before a key existed have to keep working, and reading them once
// should quietly upgrade them rather than needing a migration sweep.
func TestOAuthConnectionTokens_UpgradesLegacyPlaintext(t *testing.T) {
	org := newOrg(t)
	user := newUser(t, org)
	ctx := context.Background()

	box, err := secrets.NewKey(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := store.New(ctx, testDSN, store.WithTokenBox(box))
	if err != nil {
		t.Fatal(err)
	}
	defer sealed.Close()

	// An unsealed store is what the instance looked like before the key existed.
	plain, err := store.New(ctx, testDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()

	legacy, err := plain.UpsertOAuthConnection(ctx, org, user.ID, "jira", "acc-1",
		"atlassian-legacy-token", nil, []string{"read:jira-work"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if raw, _ := rawConnectionTokens(t, legacy.ID); raw != "atlassian-legacy-token" {
		t.Fatalf("precondition: expected a plaintext legacy row, got %q", raw)
	}

	got, err := sealed.GetOAuthConnection(ctx, org, user.ID, "jira")
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "atlassian-legacy-token" {
		t.Fatalf("legacy row read as %q", got.AccessToken)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		raw, _ := rawConnectionTokens(t, legacy.ID)
		if raw != "atlassian-legacy-token" {
			if raw[:5] != "mjv1." {
				t.Fatalf("upgrade wrote something that is not sealed: %q", raw)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("legacy plaintext row was never upgraded")
		}
		time.Sleep(50 * time.Millisecond)
	}

	again, err := sealed.GetOAuthConnection(ctx, org, user.ID, "jira")
	if err != nil {
		t.Fatal(err)
	}
	if again.AccessToken != "atlassian-legacy-token" {
		t.Fatalf("upgraded row no longer opens correctly: %q", again.AccessToken)
	}
}

// Refreshing a connection replaces the access token, keeps the stored refresh
// token when the provider does not rotate one, and stays sealed throughout.
func TestOAuthConnectionTokens_UpdateAfterRefresh(t *testing.T) {
	org := newOrg(t)
	user := newUser(t, org)
	ctx := context.Background()

	box, err := secrets.NewKey(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.New(ctx, testDSN, store.WithTokenBox(box))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	const storedRefresh = "graph-refresh-token"
	conn, err := st.UpsertOAuthConnection(ctx, org, user.ID, "teams", "user-9",
		"graph-old-token", ptrStr(storedRefresh), []string{"Channel.Send.AsUser"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	expires := time.Now().UTC().Add(time.Hour)
	if err := st.UpdateOAuthConnectionTokens(ctx, org, user.ID, "teams",
		"graph-new-token", nil, &expires); err != nil {
		t.Fatal(err)
	}

	got, err := st.GetOAuthConnection(ctx, org, user.ID, "teams")
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "graph-new-token" {
		t.Fatalf("access token after refresh = %q", got.AccessToken)
	}
	if deref(got.RefreshToken) != storedRefresh {
		t.Fatalf("unrotated refresh token was dropped: %q", deref(got.RefreshToken))
	}
	if got.ExpiresAt == nil || !got.ExpiresAt.After(time.Now()) {
		t.Fatalf("expiry after refresh = %v", got.ExpiresAt)
	}
	if got.ID != conn.ID {
		t.Fatal("refresh wrote a second connection row instead of updating the current one")
	}

	rawAccess, rawRefresh := rawConnectionTokens(t, conn.ID)
	if rawAccess == "graph-new-token" || rawRefresh == storedRefresh {
		t.Fatalf("refresh stored plaintext: %q / %q", rawAccess, rawRefresh)
	}

	// A rotated refresh token has to land too.
	rotated := "graph-refresh-token-2"
	if err := st.UpdateOAuthConnectionTokens(ctx, org, user.ID, "teams",
		"graph-third-token", ptrStr(rotated), nil); err != nil {
		t.Fatal(err)
	}
	got, err = st.GetOAuthConnection(ctx, org, user.ID, "teams")
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "graph-third-token" || deref(got.RefreshToken) != rotated {
		t.Fatalf("rotated refresh not stored: %q / %q", got.AccessToken, deref(got.RefreshToken))
	}

	if err := st.UpdateOAuthConnectionTokens(ctx, org, user.ID, "ghost",
		"x", nil, nil); err != store.ErrNotFound {
		t.Fatalf("update for a missing connection = %v, want ErrNotFound", err)
	}
}

// rawConnectionTokens reads the stored columns directly, bypassing the store's
// sealing, which is the only way to prove what is actually on disk.
func rawConnectionTokens(t *testing.T, id string) (access, refresh string) {
	t.Helper()
	err := testDB.Pool().QueryRow(context.Background(),
		`SELECT access_token_encrypted, COALESCE(refresh_token_encrypted, '')
		   FROM oauth_connections WHERE id = $1`, id,
	).Scan(&access, &refresh)
	if err != nil {
		t.Fatalf("read raw row: %v", err)
	}
	return access, refresh
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
