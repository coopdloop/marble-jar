# Marble Jar

> Every finished task, a marble in the jar. Every jar, a story you can tell your team.

Marble Jar is the missing middle layer between agent observability and team
communication. Agents report each completed unit of work as a **marble** via
SDK, MCP, REST or webhook. Marbles flow into a live, animated queue where humans
review and dispatch updates — one click or by rule — to Jira, Slack, Teams or a
custom webhook, always **on-behalf-of** a real person so every action is
auditable.

Built from [`product.json`](./product.json).

---

## Quick start

```bash
# 1. Infrastructure (Postgres+Timescale, Redis, Redpanda, Phoenix)
docker compose up -d postgres redis redpanda phoenix

# 2. Core API — runs migrations on boot
cd services/core
DATABASE_URL='postgres://marblejar:marblejar@localhost:5432/marblejar?sslmode=disable' \
REDIS_URL='redis://localhost:6379/0' \
REDPANDA_BROKERS='localhost:9092' \
JWT_SIGNING_KEY='dev-signing-key-at-least-32-bytes-long!' \
HMAC_DISPATCH_SECRET='dev-dispatch-secret' \
PHOENIX_BASE_URL='http://localhost:6006' \
go run ./cmd/server

# 3. Frontend
cd apps/web && npm install && npm run dev     # http://localhost:5173
```

Register at <http://localhost:5173/login>, then create an API key under
**Settings → API keys** and log your first marble:

```bash
curl -X POST http://localhost:8080/v1/marbles \
  -H "Authorization: Bearer mj_your_key" \
  -H "Content-Type: application/json" \
  -d '{"summary":"Refactored the auth module","model":"claude-sonnet-4","project":"payments-api","cost_usd":0.42,"tokens":{"in":12000,"out":3000}}'
```

It drops into the jar in real time.

### Dispatch worker and MCP bridge

```bash
# Worker — executes Jira/Slack/Teams/webhook actions
cd services/core
REDPANDA_BROKERS=localhost:9092 CORE_API_BASE_URL=http://localhost:8080 \
CORE_SERVICE_TOKEN=mj_your_key ... go run ./cmd/dispatch-worker

# MCP bridge — log_marble + monitor tools
cd services/mcp-bridge && pip install -e .
CORE_SERVICE_TOKEN=mj_your_key marble-jar-mcp --server http
```

Or bring the whole stack up with `docker compose up -d`.

---

## Architecture

```
   agents (Claude Code, Cursor, LangGraph, …)
        │  SDK · MCP · REST · webhook
        ▼
 ┌───────────────────────────────────────────┐
 │ marble_jar_core (Go, modular monolith)    │
 │  ingest · ws · objectives · rules · auth  │
 └───────┬───────────────┬──────────────┬────┘
         │               │              │
   Postgres +        Redis pub/sub   Redpanda
   TimescaleDB       (live jar)     (durable log)
                          │              │
                     React web     dispatch worker
                     (matter.js)    │ Jira Slack
                                    │ Teams Webhook
                                    └─ OBO token exchange
```

| Component | Stack | Port |
| --- | --- | --- |
| `services/core` | Go, Gin, pgx, gorilla/websocket, kafka-go | 8080 |
| `services/core` (worker) | Go, per-integration goroutine pools | 8081 |
| `services/mcp-bridge` | Python, MCP SDK, FastAPI, httpx | 8090 |
| `apps/web` | React, TS, TanStack Query, Zustand, matter.js | 5173 |
| `packages/sdk-ts` | TypeScript client (`@marble-jar/client`) | — |

### Design decisions

- **Modular monolith over microservices.** The spec lists nine services; ingestion,
  core and auth share a database and transaction boundary, so they ship as one Go
  binary with clean internal packages. The dispatch worker is separate because it
  scales on a different axis (consumer lag, not request rate).
- **Four dispatch workers → one.** Per-integration *goroutine pools* give the blast-radius
  isolation the ADR wanted without four deployments.
- **Query owns server state; the socket only pushes.** WebSocket events patch the
  TanStack Query cache via `setQueryData`, keeping pagination, retries and
  optimistic updates consistent.
- **Graceful degradation.** Without `REDPANDA_BROKERS`/`REDIS_URL` the stack falls
  back to in-process transports, so a laptop needs only Postgres.
- **Idempotent ingestion.** `Idempotency-Key` makes agent retries safe; a replay
  returns the original marble and fires no duplicate dispatch.

---

## API

All routes are under `/v1` and accept either a session JWT or an `mj_` API key.

| Area | Endpoints |
| --- | --- |
| Ingestion | `POST /marbles`, `GET /ws/queue` |
| Marbles | `GET/PATCH/DELETE /marbles/:id`, `POST /marbles/:id/rollups`, `POST /marbles/:id/dispatch` |
| Objectives | CRUD, `/objectives/:id/marbles/:marbleId`, `POST /objectives/:id/ship` |
| Rules | CRUD + `POST /rules/test` (preview against recent marbles) |
| Dispatch | `GET /dispatches`, `POST /dispatches/:id/result` |
| Audit | `GET /audit-log` |
| Analytics | `GET /trends/cost`, `/trends/tokens`, `/jar/status` |
| Auth | `/register`, `/login`, `/token/refresh`, `/api-keys`, `/integrations/*` |
| Ops | `GET /health`, `GET /metrics` (Prometheus) |

### Rule conditions

Declarative JSON shared by the backend evaluator and the frontend chip builder:

```json
{"all": [
  {"field": "project",  "op": "eq", "value": "payments-api"},
  {"field": "cost_usd", "op": "gt", "value": 0.5}
]}
```

Supports `all`/`any`/`not`, fields like `project`, `model`, `agent`, `cost_usd`,
`tokens_total`, `duration_ms`, and operators `eq`, `neq`, `gt`, `gte`, `lt`,
`lte`, `contains`, `in`, `exists`.

---

## Testing

```bash
cd services/core && go test ./...      # rules engine + ingestion normalization
cd apps/web && npm run typecheck
cd packages/sdk-ts && npm run typecheck
```

Verified end-to-end against live infrastructure: registration → API key →
ingestion (incl. idempotent replay) → Redpanda → rule match → dispatch worker →
HMAC-signed webhook → result callback → audit trail, plus the browser UI
(physics jar, live WS push, rule preview, Ship It).

---

## Status

**Implemented**: ingestion + idempotency, live WebSocket jar, marbles/objectives/
rules/dispatch/audit APIs, rollups and budget tracking, Ship It summaries, rules
engine with preview, dispatch worker with retry/backoff/dead-lettering, Jira/
Slack/Teams/webhook executors, Prometheus metrics, TS SDK, both MCP servers,
Phoenix trace links and reconciliation, full React UI.

**Stubbed pending credentials**: the OAuth *authorization-code callback* for
Jira/Slack/Teams. Token exchange, storage and the OBO dispatch path are built;
`POST /v1/integrations/:provider/connect` returns `501` with a clear hint until
`OAUTH_ISSUER_URL` and per-provider client credentials are configured. Webhook
dispatch works today with no external setup.

**Not yet built**: Helm charts / ArgoCD manifests, the Python SDK package (the
MCP bridge covers Python agents), and dead-letter replay endpoints.
