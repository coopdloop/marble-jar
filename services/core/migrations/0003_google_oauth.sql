-- Google sign-in replaces password accounts. Identity is now the
-- (auth_provider, provider_subject) pair Google vouches for, so the bcrypt
-- hash column is dropped rather than left nullable.
--
-- Rows that predate Google sign-in keep auth_provider = 'local' and a null
-- provider_subject until someone signs in with a matching verified email, at
-- which point ClaimUserForProvider links that one account to that one identity.

ALTER TABLE users DROP COLUMN IF EXISTS password_hash;

ALTER TABLE users ADD COLUMN IF NOT EXISTS auth_provider TEXT NOT NULL DEFAULT 'local';
ALTER TABLE users ADD COLUMN IF NOT EXISTS provider_subject TEXT;
ALTER TABLE users ALTER COLUMN auth_provider SET DEFAULT 'local';

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_provider_subject
    ON users (auth_provider, provider_subject)
    WHERE provider_subject IS NOT NULL;
