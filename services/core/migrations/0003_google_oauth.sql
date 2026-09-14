-- Google sign-in replaces local password accounts. Identity is now the
-- (auth_provider, provider_subject) pair Google vouches for, so the bcrypt
-- hash column is dropped rather than left nullable.

ALTER TABLE users DROP COLUMN IF EXISTS password_hash;

ALTER TABLE users ADD COLUMN IF NOT EXISTS auth_provider TEXT NOT NULL DEFAULT 'google';
ALTER TABLE users ADD COLUMN IF NOT EXISTS provider_subject TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_provider_subject
    ON users (auth_provider, provider_subject)
    WHERE provider_subject IS NOT NULL;
