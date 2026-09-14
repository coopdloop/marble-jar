package store_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/marble-jar/marble-jar/services/core/internal/store"
)

// Integration tests run against a real PostgreSQL (TimescaleDB optional).
// A throwaway database is created per test binary run and migrated with the
// real embedded migrations, so the SQL under test is exactly what production
// runs. Set MARBLEJAR_TEST_DATABASE_URL to point at a non-default instance;
// tests skip when no database is reachable.

var (
	testDB  *store.Store
	adminDB *pgxpool.Pool
	testDSN string
)

func TestMain(m *testing.M) {
	adminDSN := os.Getenv("MARBLEJAR_TEST_DATABASE_URL")
	if adminDSN == "" {
		adminDSN = "postgres://marblejar:marblejar@localhost:5432/postgres?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, adminDSN)
	if err != nil || pool.Ping(ctx) != nil {
		fmt.Fprintln(os.Stderr, "store tests skipped: no test database reachable")
		os.Exit(0)
	}
	adminDB = pool

	buf := make([]byte, 6)
	_, _ = rand.Read(buf)
	dbName := "mjtest_" + hex.EncodeToString(buf)

	if _, err := adminDB.Exec(ctx, "CREATE DATABASE "+dbName); err != nil {
		fmt.Fprintln(os.Stderr, "store tests skipped: cannot create database:", err)
		os.Exit(0)
	}

	testDSN = replaceDatabase(adminDSN, dbName)
	testDB, err = store.New(context.Background(), testDSN)
	if err != nil {
		fmt.Fprintln(os.Stderr, "store tests skipped: cannot connect to test db:", err)
		cleanup(ctx, dbName)
		os.Exit(0)
	}
	if err := testDB.Migrate(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "store tests failed: migrations:", err)
		cleanup(ctx, dbName)
		os.Exit(1)
	}

	code := m.Run()

	testDB.Close()
	cleanup(context.Background(), dbName)
	os.Exit(code)
}

func cleanup(ctx context.Context, dbName string) {
	// FORCE terminates the store's pooled connections first.
	_, _ = adminDB.Exec(ctx, "DROP DATABASE IF EXISTS "+dbName+" WITH (FORCE)")
	adminDB.Close()
}

func replaceDatabase(dsn, dbName string) string {
	// The DSNs used here are URL-form; swap the path segment.
	for _, sep := range []string{"/marblejar?", "/postgres?"} {
		if idx := indexOf(dsn, sep); idx >= 0 {
			return dsn[:idx] + "/" + dbName + dsn[idx+len(sep)-1:]
		}
	}
	// Fallback: append after host if no path present (unlikely for these DSNs).
	return dsn
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// newOrg gives each test an isolated tenant — every query is org-scoped, so a
// fresh organization is complete isolation without truncating tables.
func newOrg(t *testing.T) string {
	t.Helper()
	org, err := testDB.CreateOrganization(context.Background(),
		"Test Org", "test-"+randHex(4))
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	return org.ID
}

func newUser(t *testing.T, orgID string) *store.User {
	t.Helper()
	u, err := testDB.CreateUser(context.Background(), store.NewUser{
		OrganizationID:  orgID,
		Email:           fmt.Sprintf("u-%s@test.dev", randHex(4)),
		DisplayName:     "Tester",
		Role:            "owner",
		AuthProvider:    "google",
		ProviderSubject: randHex(12),
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func ptrInt(v int) *int         { return &v }
func ptrF64(v float64) *float64 { return &v }
func ptrStr(v string) *string   { return &v }
func ptrInt64(v int64) *int64   { return &v }

func baseMarble(orgID, summary string) store.CreateMarbleParams {
	return store.CreateMarbleParams{
		OrganizationID: orgID,
		Summary:        summary,
		Model:          ptrStr("test-model"),
		TokensIn:       ptrInt(100),
		TokensOut:      ptrInt(50),
		CostUSD:        ptrF64(0.01),
		DurationMS:     ptrInt(1000),
		Status:         "logged",
		Source:         "test",
	}
}

func mustCreateMarble(t *testing.T, orgID, summary string) *store.Marble {
	t.Helper()
	m, created, err := testDB.CreateMarble(context.Background(), baseMarble(orgID, summary))
	if err != nil {
		t.Fatalf("create marble %q: %v", summary, err)
	}
	if !created {
		t.Fatalf("expected marble %q to be created", summary)
	}
	return m
}
