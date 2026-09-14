# API reference

All routes live under `/v1` and accept either a session JWT (web UI) or an
`mj_` API key (agents). Every response error is JSON: `{"error": "…"}`.

## Auth

| Method & path | Notes |
| --- | --- |
| `GET /v1/auth/config` | Which sign-in methods this server has configured. |
| `GET /v1/auth/google/start` | 302 to Google. Optional `?workspace=<slug-or-uuid>` joins that workspace as `member`. |
| `GET /v1/auth/google/callback` | Google's redirect target; answers with a 60-second single-use `login_code`. |
| `POST /v1/auth/exchange` | `{login_code}` → user, org, access + refresh tokens. |
| `POST /v1/token/refresh` | Rotates the pair. |
| `GET /v1/me` | Current principal. |
| `POST /v1/token/introspect` | Validate a token. |
| `GET/POST /v1/api-keys`, `DELETE /v1/api-keys/:id` | `mj_` keys with scopes. |

There is no password endpoint: humans arrive through Google, and the ID token's
signature, `iss`, `aud`, expiry and `email_verified` are all checked server-side
before a session is issued.

## Ingestion

| Method & path | Notes |
| --- | --- |
| `POST /v1/marbles` | Log a marble. `Idempotency-Key` makes retries safe. |
| `GET /v1/ws/queue` | WebSocket feed of live jar events. |

## Marbles

| Method & path | Notes |
| --- | --- |
| `GET /v1/marbles` | Filters: `project`, `model`, `agent_id`, `status`, `q`, `from`, `unassigned`, `objective_id`, pagination. |
| `GET /v1/marbles/:id` | Marble plus its dispatch history. |
| `PATCH /v1/marbles/:id` | Edit summary/status/objective/metadata. |
| `DELETE /v1/marbles/:id` | |
| `POST /v1/marbles/:id/rollups` | Write cost/token rollups. |
| `POST /v1/marbles/:id/dispatch` | One-click dispatch: `{integration_type, action?, target_config?}`. |

## Objectives

| Method & path | Notes |
| --- | --- |
| `GET/POST /v1/objectives` | List includes rollups (marble count, cost, tokens, duration, budget %). |
| `GET/PATCH/DELETE /v1/objectives/:id` | |
| `GET/POST/DELETE /v1/objectives/:id/marbles/:marbleId` | Bucket marbles. |
| `POST /v1/objectives/:id/ship` | Compose the rollup summary and dispatch it; optionally mark shipped. |

## Rules

| Method & path | Notes |
| --- | --- |
| `GET/POST /v1/rules`, `GET/PATCH/DELETE /v1/rules/:id` | Condition → target. |
| `POST /v1/rules/test` | Preview a condition against recent marbles before saving. |

### Rule conditions

```json
{"all": [
  {"field": "project",  "op": "eq", "value": "payments-api"},
  {"field": "cost_usd", "op": "gt", "value": 0.5}
]}
```

Combine with `all` / `any` / `not`. Fields: `project`, `model`, `agent`,
`cost_usd`, `tokens_total`, `duration_ms`. Operators: `eq`, `neq`, `gt`,
`gte`, `lt`, `lte`, `contains`, `in`, `exists`.

Targets: `jira` (target config = project key), `slack` / `teams` (channel),
`webhook` (URL, HMAC-signed).

## Dispatches & audit

| Method & path | Notes |
| --- | --- |
| `GET /v1/dispatches`, `GET /v1/dispatches/:id` | Status: pending → running → succeeded / failed / dead-lettered. |
| `POST /v1/dispatches/:id/result` | Worker callback (service token). |
| `POST /v1/dispatches/:id/replay` | Re-queue failed, dead-lettered or stale-pending dispatches. |
| `GET /v1/audit-log`, `GET /v1/audit-log/:id` | Who did what, when. |

## Integrations (OBO)

| Method & path | Notes |
| --- | --- |
| `GET /v1/integrations` | Per-provider connected state for the current user. |
| `GET /v1/integrations/:p/connection` | Scopes, expiry, provider account. |
| `POST /v1/integrations/:p/connect` | Returns authorize URL; `501` + hint when the OAuth issuer isn't configured. |
| `DELETE /v1/integrations/:p/connection` | |

## Webhook endpoints

| Method & path | Notes |
| --- | --- |
| `GET/POST /v1/webhook-endpoints`, `DELETE /v1/webhook-endpoints/:id` | Inbound URLs external systems can POST marbles to. |

## Analytics & ops

| Method & path | Notes |
| --- | --- |
| `GET /v1/jar/status` | Today/last-hour counts, spend, tokens, open objectives. |
| `GET /v1/trends/cost`, `GET /v1/trends/tokens` | Time series. |
| `GET /v1/projects`, `GET /v1/agents` | Filter vocabularies. |
| `GET /health` | Liveness + DB + WS connection count. |
| `GET /metrics` | Prometheus. |
