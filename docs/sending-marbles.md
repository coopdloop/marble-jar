# Sending marbles

Four ways in, all ending at the same endpoint: `POST /v1/marbles` with an
`mj_` API key.

## TypeScript SDK

```bash
npm install @marble-jar/client
```

```ts
import { MarbleJar } from "@marble-jar/client";

const mj = new MarbleJar({
  baseUrl: "http://localhost:8080",
  apiKey: process.env.MARBLE_JAR_KEY, // mj_…
});

await mj.ingestion.logMarble({
  taskSummary: "Refactored the auth module",
  model: "claude-sonnet-4",
  project: "payments-api",
  costUsd: 0.42,
  tokensInput: 12000,
  tokensOutput: 3000,
  durationMs: 45000,
  idempotencyKey: runId, // retries are safe
});
```

## REST / curl

```bash
curl -X POST http://localhost:8080/v1/marbles \
  -H "Authorization: Bearer mj_your_key" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: run-42" \
  -d '{"summary":"…","model":"…","project":"…"}'
```

**Idempotency:** send `Idempotency-Key` (or `idempotency_key` in the body).
Replaying the same key returns the original marble instead of a duplicate and
fires no extra dispatches — so agents can retry freely.

## MCP bridge (Python agents)

```bash
cd services/mcp-bridge && pip install -e .
export CORE_SERVICE_TOKEN=mj_your_key
marble-jar-mcp --server http   # listens on :8090
```

Exposes `log_marble` and monitoring tools to any MCP-capable agent (Claude
Code, Cursor, LangGraph…). It forwards to the core API with the service token.

## Webhook ingestion

Create inbound endpoints under **Settings → Webhook endpoints**; external
systems (CI, cron, other agents) can then POST marble payloads to a dedicated
URL.

## What a marble can carry

| Field | Notes |
| --- | --- |
| `summary` | **Required.** One sentence of what got done. |
| `model` | **Required.** Drives the marble's color in the jar. |
| `project` | Groups marbles; feeds rule conditions. |
| `agent` / `agent_id` / `harness` | Who/what did the work. |
| `cost_usd` | Drives marble size; outliers get the glow. |
| `tokens_input` / `tokens_output` | Roll up into objectives and trends. |
| `duration_ms` | Wall-clock time of the task. |
| `trace_id` | Deep-links into Phoenix when `PHOENIX_BASE_URL` is set. |
| `objective_id` | Pre-bucket the marble. |
| `status` | `logged` (default), `dispatched`, `failed`. |
| `metadata` | Free-form JSON. |
| `occurred_at` | Backfill historical work. |
