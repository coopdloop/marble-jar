# Configuration

Everything is environment-driven; `env.example` at the repo root is the
canonical template. `DEV_MODE=true` relaxes secret-strength checks and enables
in-process fallbacks so a laptop needs only Postgres.

## Core API + dispatch worker (`services/core`)

| Variable | Required | Default | Notes |
| --- | --- | --- | --- |
| `DATABASE_URL` | ✔ | — | Postgres DSN (alias `POSTGRES_DSN`). |
| `JWT_SIGNING_KEY` | ✔* | — | ≥ 32 bytes outside `DEV_MODE`. Signs session JWTs. |
| `HMAC_DISPATCH_SECRET` | ✔* | — | Signs outbound webhook intents. |
| `PORT` | | `8080` | API listen port. |
| `REDIS_URL` | | — | Live-queue pub/sub. In-process bus when unset. |
| `REDPANDA_BROKERS` | | — | Comma-separated. Durable dispatch log; in-process when unset. |
| `LOG_LEVEL` | | `info` | `debug` for noisy dev. |
| `CORS_ORIGINS` | | `http://localhost:5173` | Comma-separated allowed origins. |
| `ACCESS_TOKEN_TTL` | | `1h` | Go duration, e.g. `30m`. |
| `REFRESH_TOKEN_TTL` | | `720h` | |
| `PHOENIX_BASE_URL` | | — | Arize Phoenix; enables trace deep links. |
| `PHOENIX_API_KEY` | | — | |
| `DEV_MODE` | | `false` | Relaxes the ✔* requirements. Do not ship. |

## Sign in with Google

Human accounts use Google's OpenID Connect authorization code flow; Marble Jar
stores no passwords. Agents keep using `mj_` API keys, so the credentials below
are only needed to sign in to the dashboard.

| Variable | Required | Notes |
| --- | --- | --- |
| `GOOGLE_OAUTH_CLIENT_ID` | ✔ | OAuth client ID (type **Web application**) from Google Cloud → APIs & Services → Credentials. |
| `GOOGLE_OAUTH_CLIENT_SECRET` | ✔ | The matching client secret. |
| `GOOGLE_OAUTH_ALLOWED_DOMAINS` | | Comma-separated Workspace domains. Unset = any Google account with a verified email. |

The redirect URI must be `PUBLIC_API_BASE_URL + /v1/auth/google/callback`, e.g.
`http://localhost:8080/v1/auth/google/callback`. Without the credentials the
callback endpoints answer `501` and the sign-in page says so rather than
failing quietly.

Provisioning happens on first use. A verified Google account with no local row
opens its own workspace and owns it (`role = owner`). Joining an existing
workspace needs a link minted by an admin — **Settings → Invite a teammate**
(`POST /v1/auth/google/invite`), valid 7 days — because a visitor cannot be
allowed to name a workspace themselves. An account whose email already exists is
linked to that row instead, keeping its role and history; that link only happens
once per row, so two Google accounts claiming one email cannot trade ownership.

Unlike the OBO issuer, these credentials are useless without
`PUBLIC_API_BASE_URL` being reachable from the browser — a proxy base path is
honoured (`https://host/api` registers `https://host/api/v1/auth/google/callback`).

## OBO / OAuth (see [Integrations & OBO](./integrations.md))

| Variable | Notes |
| --- | --- |
| `PUBLIC_API_BASE_URL` | Where providers redirect after consent. Default `http://localhost:PORT`. |
| `WEB_APP_URL` | Where the OAuth callback lands the user. Default `http://localhost:5173`. |
| `SLACK_OAUTH_CLIENT_ID` / `SLACK_OAUTH_CLIENT_SECRET` | Slack app (user scope `chat:write`). |
| `ATLASSIAN_OAUTH_CLIENT_ID` / `ATLASSIAN_OAUTH_CLIENT_SECRET` | Jira OAuth app. |
| `ENTRA_ID_TENANT_ID` / `ENTRA_ID_CLIENT_ID` / `ENTRA_ID_CLIENT_SECRET` | Teams via Microsoft Entra ID. |
| `OAUTH_TOKEN_KEY` | base64 32-byte AES-256-GCM key sealing provider tokens at rest. **Same value in core and the worker.** Empty = plaintext storage plus a startup warning. Generate: `openssl rand -base64 32`. |
| `OAUTH_ISSUER_URL` | Optional RFC 8693 fallback broker (Auth0/Keycloak — **Hydra does not implement the exchange grant**). Alias `HYDRA_ISSUER_URL`. |
| `OAUTH_CLIENT_ID` / `OAUTH_CLIENT_SECRET` | Client at the broker. Aliases `HYDRA_CLIENT_ID` / `HYDRA_CLIENT_SECRET`. |
| `HYDRA_SECRETS_SYSTEM` | The Hydra container's own secret (docker compose). |

## Dispatch worker only

| Variable | Default | Notes |
| --- | --- | --- |
| `CORE_API_BASE_URL` | `http://localhost:8080` | Where the worker reports results. |
| `CORE_SERVICE_TOKEN` | — | An `mj_` API key with `dispatch:write` scope. |
| `DISPATCH_WORKER_CONCURRENCY` | `4` | Parallel executions. |
| `WORKER_PORT` | `8081` | Health endpoint. |

## MCP bridge (`services/mcp-bridge`)

| Variable | Default | Notes |
| --- | --- | --- |
| `CORE_API_BASE_URL` | `http://localhost:8080` | |
| `CORE_SERVICE_TOKEN` | — | **Required.** `mj_` key used for all agent writes. |
| `MCP_SERVER_PORT` | `8090` | |
| `PHOENIX_BASE_URL` / `PHOENIX_API_KEY` | — | Optional trace links. |

## Web (`apps/web`)

| Variable | Default | Notes |
| --- | --- | --- |
| `VITE_API_BASE_URL` | `http://localhost:8080` | Baked in at build time. |

## Graceful degradation

Without Redis/Redpanda the stack uses in-process transports — fine for a
laptop, not for multiple replicas. Without Phoenix, trace links simply don't
render. Without the OAuth issuer, Jira/Slack/Teams connect returns `501` with
a hint, and webhook dispatches keep working.
