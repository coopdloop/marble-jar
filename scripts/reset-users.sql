-- Reset identity rows after moving from password accounts to Google sign-in.
--
-- Password-only accounts can no longer authenticate, so they are removed along
-- with the workspaces they created. Deleting an organization cascades to its
-- marbles, objectives, rules, dispatches, audit log and API keys. Run with:
--   make reset-users
--
-- Afterward sign in with Google once, then recreate the API key the dispatch
-- worker reports back with (CORE_SERVICE_TOKEN).

BEGIN;

CREATE TEMP TABLE _identity_reset ON COMMIT DROP AS
    SELECT id FROM organizations
    WHERE id IN (SELECT organization_id FROM users);

DELETE FROM users;
DELETE FROM organizations WHERE id IN (SELECT id FROM _identity_reset);

SELECT count(*) AS workspaces_removed FROM _identity_reset;

COMMIT;
