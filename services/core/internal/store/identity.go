package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ---------- organizations & users ----------

func (s *Store) CreateOrganization(ctx context.Context, name, slug string) (*Organization, error) {
	var o Organization
	err := s.pool.QueryRow(ctx, `
		INSERT INTO organizations (name, slug) VALUES ($1,$2)
		RETURNING id, name, slug, settings, created_at, updated_at`,
		name, slug,
	).Scan(&o.ID, &o.Name, &o.Slug, &o.Settings, &o.CreatedAt, &o.UpdatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &o, nil
}

func (s *Store) GetOrganization(ctx context.Context, id string) (*Organization, error) {
	var o Organization
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, slug, settings, created_at, updated_at
		FROM organizations WHERE id = $1`, id,
	).Scan(&o.ID, &o.Name, &o.Slug, &o.Settings, &o.CreatedAt, &o.UpdatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &o, nil
}

const userColumns = `id, organization_id, email, display_name, avatar_url, role,
	is_active, auth_provider, provider_subject, created_at, updated_at`

// NewUser describes an account provisioned by an external identity provider.
type NewUser struct {
	OrganizationID  string
	Email           string
	DisplayName     string
	AvatarURL       string
	Role            string
	AuthProvider    string
	ProviderSubject string
}

func (s *Store) CreateUser(ctx context.Context, in NewUser) (*User, error) {
	var u User
	err := s.pool.QueryRow(ctx, `
		INSERT INTO users (organization_id, email, display_name, avatar_url, role,
		                   auth_provider, provider_subject)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		RETURNING `+userColumns,
		in.OrganizationID, strings.ToLower(in.Email), nullable(in.DisplayName),
		nullable(in.AvatarURL), in.Role, in.AuthProvider, nullable(in.ProviderSubject),
	).Scan(&u.ID, &u.OrganizationID, &u.Email, &u.DisplayName, &u.AvatarURL, &u.Role,
		&u.IsActive, &u.AuthProvider, &u.ProviderSubject, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &u, nil
}

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	err := row.Scan(&u.ID, &u.OrganizationID, &u.Email, &u.DisplayName, &u.AvatarURL,
		&u.Role, &u.IsActive, &u.AuthProvider, &u.ProviderSubject, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &u, nil
}

// GetUserByProviderSubject finds the account already bound to a provider identity.
func (s *Store) GetUserByProviderSubject(ctx context.Context, provider, subject string) (*User, error) {
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userColumns+
		` FROM users WHERE auth_provider = $1 AND provider_subject = $2`, provider, subject))
}

// RefreshProviderProfile updates the profile fields a provider claims for an
// account it is already bound to. The email is deliberately left alone: it is a
// lookup key elsewhere, and identity here is the provider subject.
func (s *Store) RefreshProviderProfile(ctx context.Context, userID, provider, subject, displayName, avatarURL string) (*User, error) {
	return scanUser(s.pool.QueryRow(ctx, `
		UPDATE users SET display_name = COALESCE($4, display_name),
		                 avatar_url = COALESCE($5, avatar_url), updated_at = NOW()
		WHERE id = $1 AND auth_provider = $2 AND provider_subject = $3
		RETURNING `+userColumns,
		userID, provider, subject, nullable(displayName), nullable(avatarURL)))
}

// ClaimUserForProvider binds a provider identity to an account that has none
// yet — how an account predating Google sign-in keeps its role and history.
// It never moves an account that is already bound to another subject, so two
// provider identities claiming one email cannot ping-pong the owner out.
func (s *Store) ClaimUserForProvider(ctx context.Context, userID, provider, subject, displayName, avatarURL string) (*User, error) {
	return scanUser(s.pool.QueryRow(ctx, `
		UPDATE users SET auth_provider = $2, provider_subject = $3,
		                 display_name = COALESCE($4, display_name),
		                 avatar_url = COALESCE($5, avatar_url), updated_at = NOW()
		WHERE id = $1 AND provider_subject IS NULL
		RETURNING `+userColumns,
		userID, provider, subject, nullable(displayName), nullable(avatarURL)))
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	return scanUser(s.pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE email = $1`, strings.ToLower(email)))
}

func (s *Store) GetUser(ctx context.Context, id string) (*User, error) {
	return scanUser(s.pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE id = $1`, id))
}

// ---------- api keys ----------

func (s *Store) CreateAPIKey(ctx context.Context, orgID string, userID *string, name, prefix, hashed string, scopes []string) (*APIKey, error) {
	var k APIKey
	err := s.pool.QueryRow(ctx, `
		INSERT INTO api_keys (organization_id, created_by_user_id, name, key_prefix, hashed_key, scopes)
		VALUES ($1,$2,$3,$4,$5,$6)
		RETURNING id, organization_id, created_by_user_id, name, key_prefix, hashed_key,
		          scopes, last_used_at, revoked_at, created_at, updated_at`,
		orgID, userID, name, prefix, hashed, scopes,
	).Scan(&k.ID, &k.OrganizationID, &k.CreatedByUserID, &k.Name, &k.KeyPrefix, &k.HashedKey,
		&k.Scopes, &k.LastUsedAt, &k.RevokedAt, &k.CreatedAt, &k.UpdatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &k, nil
}

// APIKeysByPrefix returns live candidates for a presented key prefix; the
// caller compares the full hash in constant time.
func (s *Store) APIKeysByPrefix(ctx context.Context, prefix string) ([]APIKey, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, organization_id, created_by_user_id, name, key_prefix, hashed_key,
		       scopes, last_used_at, revoked_at, created_at, updated_at
		FROM api_keys WHERE key_prefix = $1 AND revoked_at IS NULL`, prefix)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	out := []APIKey{}
	for rows.Next() {
		var k APIKey
		if err := rows.Scan(&k.ID, &k.OrganizationID, &k.CreatedByUserID, &k.Name,
			&k.KeyPrefix, &k.HashedKey, &k.Scopes, &k.LastUsedAt, &k.RevokedAt,
			&k.CreatedAt, &k.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Store) ListAPIKeys(ctx context.Context, orgID string) ([]APIKey, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, organization_id, created_by_user_id, name, key_prefix, hashed_key,
		       scopes, last_used_at, revoked_at, created_at, updated_at
		FROM api_keys WHERE organization_id = $1 ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	out := []APIKey{}
	for rows.Next() {
		var k APIKey
		if err := rows.Scan(&k.ID, &k.OrganizationID, &k.CreatedByUserID, &k.Name,
			&k.KeyPrefix, &k.HashedKey, &k.Scopes, &k.LastUsedAt, &k.RevokedAt,
			&k.CreatedAt, &k.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Store) RevokeAPIKey(ctx context.Context, orgID, id string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE api_keys SET revoked_at = NOW(), updated_at = NOW()
		WHERE organization_id = $1 AND id = $2 AND revoked_at IS NULL`, orgID, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) TouchAPIKey(ctx context.Context, id string) {
	_, _ = s.pool.Exec(ctx, `UPDATE api_keys SET last_used_at = NOW() WHERE id = $1`, id)
}

// ---------- projects & agents ----------

// UpsertProjectBySlug resolves (and lazily creates) a project from the free-form
// "project" field agents send at ingest time.
func (s *Store) UpsertProjectBySlug(ctx context.Context, orgID, slug string) (*Project, error) {
	slug = slugify(slug)
	if slug == "" {
		return nil, ErrNotFound
	}
	var p Project
	err := s.pool.QueryRow(ctx, `
		INSERT INTO projects (organization_id, name, slug) VALUES ($1,$2,$2)
		ON CONFLICT (organization_id, slug) DO UPDATE SET updated_at = NOW()
		RETURNING id, organization_id, name, slug, description, created_at, updated_at`,
		orgID, slug,
	).Scan(&p.ID, &p.OrganizationID, &p.Name, &p.Slug, &p.Description, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &p, nil
}

func (s *Store) ListProjects(ctx context.Context, orgID string) ([]Project, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, organization_id, name, slug, description, created_at, updated_at
		FROM projects WHERE organization_id = $1 ORDER BY name ASC`, orgID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	out := []Project{}
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.OrganizationID, &p.Name, &p.Slug, &p.Description,
			&p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// UpsertAgentByName resolves (and lazily creates) an agent identity by name.
func (s *Store) UpsertAgentByName(ctx context.Context, orgID, name, harness, model string) (*Agent, error) {
	if name == "" {
		return nil, ErrNotFound
	}
	var a Agent
	err := s.pool.QueryRow(ctx, `
		SELECT id, organization_id, name, harness, default_model, metadata, created_at, updated_at
		FROM agents WHERE organization_id = $1 AND name = $2`, orgID, name,
	).Scan(&a.ID, &a.OrganizationID, &a.Name, &a.Harness, &a.DefaultModel, &a.Metadata,
		&a.CreatedAt, &a.UpdatedAt)
	if err == nil {
		return &a, nil
	}
	if mapErr(err) != ErrNotFound {
		return nil, mapErr(err)
	}

	err = s.pool.QueryRow(ctx, `
		INSERT INTO agents (organization_id, name, harness, default_model)
		VALUES ($1,$2,$3,$4)
		RETURNING id, organization_id, name, harness, default_model, metadata, created_at, updated_at`,
		orgID, name, nullable(harness), nullable(model),
	).Scan(&a.ID, &a.OrganizationID, &a.Name, &a.Harness, &a.DefaultModel, &a.Metadata,
		&a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &a, nil
}

func (s *Store) ListAgents(ctx context.Context, orgID string) ([]Agent, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, organization_id, name, harness, default_model, metadata, created_at, updated_at
		FROM agents WHERE organization_id = $1 ORDER BY name ASC`, orgID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	out := []Agent{}
	for rows.Next() {
		var a Agent
		if err := rows.Scan(&a.ID, &a.OrganizationID, &a.Name, &a.Harness, &a.DefaultModel,
			&a.Metadata, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ---------- oauth connections (OBO) ----------
//
// Access and refresh tokens are sealed with the store's token box before they
// touch the database and opened again on read, so callers always handle
// plaintext while the rows hold ciphertext. The columns keep their
// *_token_encrypted names, which is what they now actually contain.

func (s *Store) UpsertOAuthConnection(ctx context.Context, orgID, userID, provider, accountID, accessToken string, refreshToken *string, scopes []string, expiresAt *time.Time) (*OAuthConnection, error) {
	var sealedRefresh *string
	if refreshToken != nil {
		sealed := s.tokens.Seal(*refreshToken)
		sealedRefresh = &sealed
	}

	var c OAuthConnection
	err := s.pool.QueryRow(ctx, `
		INSERT INTO oauth_connections (
			organization_id, user_id, provider, provider_account_id,
			access_token_encrypted, refresh_token_encrypted, scopes, expires_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING id, organization_id, user_id, provider, provider_account_id,
		          access_token_encrypted, refresh_token_encrypted, scopes, expires_at,
		          created_at, updated_at`,
		orgID, userID, provider, nullable(accountID), s.tokens.Seal(accessToken), sealedRefresh, scopes, expiresAt,
	).Scan(&c.ID, &c.OrganizationID, &c.UserID, &c.Provider, &c.ProviderAccountID,
		&c.AccessToken, &c.RefreshToken, &c.Scopes, &c.ExpiresAt,
		&c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	if err := s.openOAuthConnection(ctx, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// UpdateOAuthConnectionTokens replaces the tokens on the connection that reads
// back as current — the newest row for (org, user, provider), matching
// GetOAuthConnection's own tie-break.
func (s *Store) UpdateOAuthConnectionTokens(ctx context.Context, orgID, userID, provider, accessToken string, refreshToken *string, expiresAt *time.Time) error {
	args := []any{
		orgID, userID, provider,
		s.tokens.Seal(accessToken),
		expiresAt,
	}
	// A provider that does not rotate the refresh token sends back "", and the
	// stored one stays in play.
	setClause := "refresh_token_encrypted = refresh_token_encrypted"
	if refreshToken != nil && *refreshToken != "" {
		setClause = "refresh_token_encrypted = $6"
		args = append(args, s.tokens.Seal(*refreshToken))
	}

	tag, err := s.pool.Exec(ctx, fmt.Sprintf(`
		UPDATE oauth_connections
		   SET access_token_encrypted = $4,
		       %s,
		           expires_at = $5,
		       updated_at = NOW()
		 WHERE id = (SELECT id FROM oauth_connections
		           WHERE organization_id = $1 AND user_id = $2 AND provider = $3
		           ORDER BY created_at DESC LIMIT 1)`, setClause),
		args...)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) GetOAuthConnection(ctx context.Context, orgID, userID, provider string) (*OAuthConnection, error) {
	var c OAuthConnection
	err := s.pool.QueryRow(ctx, oauthConnectionSelect+`
		WHERE organization_id = $1 AND user_id = $2 AND provider = $3
		ORDER BY created_at DESC LIMIT 1`,
		orgID, userID, provider,
	).Scan(&c.ID, &c.OrganizationID, &c.UserID, &c.Provider, &c.ProviderAccountID,
		&c.AccessToken, &c.RefreshToken, &c.Scopes, &c.ExpiresAt,
		&c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	if err := s.openOAuthConnection(ctx, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

const oauthConnectionSelect = `
		SELECT id, organization_id, user_id, provider, provider_account_id,
		       access_token_encrypted, refresh_token_encrypted, scopes, expires_at,
		       created_at, updated_at
		FROM oauth_connections`

// openOAuthConnection decrypts a row in place and transparently upgrades rows
// written before a token key existed, so an instance can adopt encryption
// without a migration sweep or a downtime window.
func (s *Store) openOAuthConnection(ctx context.Context, c *OAuthConnection) error {
	access, upgradeAccess, err := s.tokens.Open(c.AccessToken)
	if err != nil {
		return err
	}
	c.AccessToken = access

	upgradeRefresh := false
	if c.RefreshToken != nil {
		refresh, needsUpgrade, err := s.tokens.Open(*c.RefreshToken)
		if err != nil {
			return err
		}
		*c.RefreshToken = refresh
		upgradeRefresh = needsUpgrade
	}

	if (upgradeAccess || upgradeRefresh) && s.tokens != nil {
		sealedRefresh := c.RefreshToken
		if sealedRefresh != nil {
			sealed := s.tokens.Seal(*sealedRefresh)
			sealedRefresh = &sealed
		}
		if _, err := s.pool.Exec(ctx, `
			UPDATE oauth_connections
			   SET access_token_encrypted = $2, refresh_token_encrypted = $3, updated_at = NOW()
			 WHERE id = $1`,
			c.ID, s.tokens.Seal(c.AccessToken), sealedRefresh); err != nil {
			// The row stays plaintext and the next read retries the upgrade; a
			// bookkeeping failure must not break the caller's dispatch.
		}
	}
	return nil
}

func (s *Store) ListOAuthConnections(ctx context.Context, orgID, userID string) ([]OAuthConnection, error) {
	rows, err := s.pool.Query(ctx, oauthConnectionSelect+`
		WHERE organization_id = $1 AND user_id = $2`,
		orgID, userID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	out := []OAuthConnection{}
	for rows.Next() {
		var c OAuthConnection
		if err := rows.Scan(&c.ID, &c.OrganizationID, &c.UserID, &c.Provider,
			&c.ProviderAccountID, &c.AccessToken, &c.RefreshToken,
			&c.Scopes, &c.ExpiresAt, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		if err := s.openOAuthConnection(ctx, &c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) DeleteOAuthConnection(ctx context.Context, orgID, userID, provider string) error {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM oauth_connections
		WHERE organization_id = $1 AND user_id = $2 AND provider = $3`,
		orgID, userID, provider)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func nullable(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &s
}

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == '-' || r == '_' || r == ' ' || r == '/' || r == '.':
			if !lastDash && b.Len() > 0 {
				b.WriteRune('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// RawJSON is a small helper for building JSONB values from maps.
func RawJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}
