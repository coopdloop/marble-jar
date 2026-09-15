# Integrations & OBO

Marble Jar's rule: **every dispatch runs on-behalf-of a real person, never an
anonymous bot.** When a rule or a click posts to Slack, files a Jira issue or
messages a Teams channel, the action carries the identity of the user who owns
the connection — and the audit trail records it.

This page explains how the OBO flow works and exactly which secrets to set to
turn it on.

## How a connection actually works

The connect flow is **provider-native OAuth** — no broker involved:

1. **Connect** (`POST /v1/integrations/:provider/connect`) returns the
   provider's own authorize URL (Slack, Atlassian or Entra) with a signed,
   short-lived state token carrying your identity.
2. You consent at the provider, which redirects to
   `GET {PUBLIC_API_BASE_URL}/v1/integrations/:provider/callback`.
3. The callback verifies the state, exchanges the code for tokens, stores the
   connection (user token for Slack, so actions post **as you**) and redirects
   back to the web app.
4. Every dispatch then resolves credentials **stored-connection first** — the
   acting user's real provider token — and writes an audit entry.

### Where `OAUTH_ISSUER_URL` fits

`OAUTH_ISSUER_URL` is the optional **RFC 8693 token-exchange broker** used as
a *fallback* when the acting user has no stored connection. Choose carefully:

- **Auth0 / Keycloak** — support the token-exchange grant; usable as the
  fallback broker.
- **Ory Hydra** — ❗ **does not implement RFC 8693** (the v2 release notes
  briefly claimed it and were retracted). Hydra works fine as a plain OIDC
  issuer (authorization_code + client_credentials) but the exchange fallback
  will never succeed against it. With stored connections doing the real work,
  Hydra is optional.

With no issuer and no connection, token-requiring dispatches fail loudly;
webhook dispatches always work.

## Required secrets (core service)

```bash
# Connect flow geometry
PUBLIC_API_BASE_URL=http://localhost:8080   # provider redirect_uri base
WEB_APP_URL=http://localhost:5173           # post-connect landing page

# Per-provider OAuth apps (see below for how to create them)
SLACK_OAUTH_CLIENT_ID=
SLACK_OAUTH_CLIENT_SECRET=
ATLASSIAN_OAUTH_CLIENT_ID=
ATLASSIAN_OAUTH_CLIENT_SECRET=
ENTRA_ID_TENANT_ID=
ENTRA_ID_CLIENT_ID=
ENTRA_ID_CLIENT_SECRET=

# Optional RFC 8693 fallback broker (Auth0 / Keycloak — NOT Hydra)
OAUTH_ISSUER_URL=
OAUTH_CLIENT_ID=
OAUTH_CLIENT_SECRET=
```

Legacy aliases `HYDRA_ISSUER_URL`, `HYDRA_CLIENT_ID` and `HYDRA_CLIENT_SECRET`
are still honored for the `OAUTH_*` names.

## Setting up Slack, step by step

1. Create an app at <https://api.slack.com/apps> → **From scratch**.
2. **OAuth & Permissions → Redirect URLs**: add
   `http://localhost:8080/v1/integrations/slack/callback`
   (Slack allows `http://localhost` for development; use HTTPS in prod).
3. **User Token Scopes**: add `chat:write` (and `channels:read` if you want
   channel listing later). *User* scope, not bot — the dispatch must act as
   the connecting person.
4. Copy **Client ID** and **Client Secret** from **Basic Information** into
   `SLACK_OAUTH_CLIENT_ID` / `SLACK_OAUTH_CLIENT_SECRET`, restart the API.
5. In the UI: **Integrations → Slack → Connect**, consent, and you land back
   on the page with a green connected badge.

Dispatches to Slack then post via `chat.postMessage` as you. The target of a
rule or one-click dispatch is the channel ID (or `webhook_url` for
OBO-free incoming-webhook mode).

Requested scopes per provider:

| Provider | Scopes |
| --- | --- |
| Jira | `read:jira-work`, `write:jira-work`, `offline_access` |
| Slack | `chat:write`, `channels:read` |
| Teams | `ChannelMessage.Send`, `offline_access` |

## What happens at runtime

**Dispatch (worker → provider):** the worker resolves the acting user's
stored provider connection first. If none exists and `OAUTH_ISSUER_URL` is
configured, it falls back to an RFC 8693 token exchange:

```
POST {OAUTH_ISSUER_URL}/oauth2/token
grant_type=urn:ietf:params:oauth:grant-type:token-exchange
subject_token=<acting user id>
subject_token_type=urn:ietf:params:oauth:token-type:id_token
requested_token_type=urn:ietf:params:oauth:token-type:access_token
audience=<provider audience>
```

Audiences: Jira → `https://api.atlassian.com`, Slack →
`https://slack.com/api`, Teams → `https://graph.microsoft.com`. Tokens are
cached until 30 s before expiry, so a busy rule doesn't hammer the issuer.

> ⚠️ The exchange fallback expects an issuer that accepts the acting user's
> identity as a subject token. Marble Jar currently sends the **user ID** there,
> which a spec-compliant issuer rejects — so treat this path as unproven until
> it is exercised against a real Auth0/Keycloak tenant. Stored connections (the
> path above it) do not depend on any of this.

## Stored tokens: encryption and refresh

Everything a Connect flow obtains — provider access token, refresh token,
scopes, expiry — lands in `oauth_connections`, and that table is read by the
worker on every dispatch. Two things protect it:

**Encryption at rest.** Set `OAUTH_TOKEN_KEY` to a base64 32-byte key
(`openssl rand -base64 32`) and tokens are sealed with AES-256-GCM before they
reach Postgres, then opened on read. Both core and the dispatch worker must see
the **same** key, because the worker reads back what the API wrote.

- Rows written before a key existed keep working: reading one returns the
  plaintext and re-seals it in place, so adoption needs no migration and no
  downtime.
- Without the key, values are stored exactly as the provider sent them and both
  processes log a warning at startup. The columns are named
  `*_token_encrypted` either way.
- A wrong or rotated key fails closed — dispatches error out rather than
  handing an executor a ciphertext blob as a bearer token. Keep a backup before
  rotating.
- Tokens never appear in an API response: the model tags them `json:"-"`.

**Refresh before expiry.** Provider access tokens are short-lived (Microsoft
graph tokens last about an hour). When a stored connection is within a minute
of `expires_at`, the worker redeems the refresh token at the provider's own
token endpoint — Slack `oauth.v2.access`, Atlassian `/oauth/token` with Basic
client auth, Entra `/common/oauth2/v2.0/token` — stores the new pair and
continues the dispatch. That needs each provider's client credentials in the
**worker's** environment, not just core's.

When a provider says the grant is dead (`invalid_grant`, revoked app, consent
withdrawn) the dispatch fails permanently with a *reconnect required* message
rather than retrying a hundred times, and it appears on the Dispatches page.
Transport blips and 429/5xx stay retryable, and if a refresh fails for a token
that is not yet expired the worker still uses what it has.

Slack and Atlassian commonly hand out tokens with no expiry; those rows carry no
`expires_at`, are never refreshed, and keep working until they actually break.

## Local Hydra (optional)

```bash
docker compose up -d postgres hydra-migrate hydra
# health: curl http://localhost:4444/health/ready
curl -X POST http://localhost:4445/admin/clients \
  -H 'Content-Type: application/json' -d '{
    "client_id": "marble-jar", "client_secret": "dev-hydra-client-secret",
    "grant_types": ["client_credentials"],
    "token_endpoint_auth_method": "client_secret_post"}'
# smoke test:
curl -X POST http://localhost:4444/oauth2/token \
  -d grant_type=client_credentials -d client_id=marble-jar \
  -d client_secret=dev-hydra-client-secret
```

Hydra's `hydra` database is created by `deploy/postgres/00-create-hydra-db.sql`
on first boot of the postgres volume; on an existing volume create it once by
hand: `docker compose exec postgres psql -U marblejar -c 'CREATE DATABASE hydra;'`

**Failure semantics:** a 429/5xx from the issuer marks the dispatch retryable
(normal backoff); a 4xx marks it permanent (dead-letter, replayable from the
Dispatches page once fixed). The same split applies to a failed token refresh.
A dispatch with no acting user fails loudly instead of falling back to a shared
credential.

## No broker? Webhooks still work

If `OAUTH_ISSUER_URL` is unset, the OBO provider returns no token and
token-requiring executors fail loudly — but **webhook dispatches are fully
functional with zero OAuth setup**. Rules and one-click dispatches can POST an
HMAC-signed intent (signed with `HMAC_DISPATCH_SECRET`) to any URL today.

## Security notes

- Keep `OAUTH_CLIENT_SECRET` and the provider secrets server-side only; they
  must never reach the web app bundle.
- Every connect, disconnect and dispatch writes to the audit log
  (**Integrations → Audit trail**), including the acting user id.
- Token responses are held in memory only and never logged.
