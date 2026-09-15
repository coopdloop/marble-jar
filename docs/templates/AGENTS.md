# Marble Jar reporting contract

> Paste this file into your harness context — `AGENTS.md` at the repo root for
> most agents, `CLAUDE.md` for Claude Code, or the platform's system-prompt
> store — and every unit of agent work becomes a marble in the jar. Everything
> below is written against the real HTTP API, so it works from any harness that
> can run `curl`.

## Environment

| Variable | Example | Notes |
| --- | --- | --- |
| `MARBLE_JAR_URL` | `http://localhost:8080` | Core API base. No trailing slash needed. |
| `MARBLE_JAR_API_KEY` | `mj_…` | Agent key from the dashboard → Settings → API keys. |
| `MJ_PROJECT` | `payments-api` | Stable slug for this repository or service. |
| `MJ_AGENT` | `claude-code` | Name of the harness running you. |
| `MJ_OBJECTIVE_ID` | `01…` | Optional: the objective current work belongs to. |

The key needs `marbles:write` to log and `rollups:write` to attach usage after
the fact. Reporting never needs `dispatch:write`, `rules:write` or
`objectives:write` — those belong to humans and to the automations they own.

## The contract

Read this section even if you skip the rest.

1. **One marble per completed unit of work.** A task, a commit, a passing test
   suite, a review comment addressed, an incident mitigated. Not one per file
   touched, and not one per chat turn.
2. **Log before the turn ends.** A finished unit of work that nobody reported
   did not happen. Batch the calls at the end of a task if you must, never
   skip them.
3. **Real numbers, honestly marked down when estimated.** Send `tokens_in`,
   `tokens_out`, `cost_usd` and `duration_ms` whenever the harness exposes
   them. If you estimated, say so: `metadata.usage_estimated: true`.
4. **Every log carries an `Idempotency-Key`.** Use the commit SHA, the task id,
   or `<branch>-<slug>` . Replaying a key returns the original marble and fires
   no extra dispatches, so retrying is always safe.
5. **Never put secrets in the jar.** No API keys, tokens, connection strings,
   credentials, or personal data in `summary` or `metadata`. Summaries get
   exported to CSV and posted to chat.
6. **Agents report; humans dispatch.** Do not post to Slack, Jira or Teams
   unless the human explicitly asked for that outbound action. Auto-dispatch is
   a rule a human owns.
7. **Failures are marbles too.** A failed run gets `status: "failed"` and
   `metadata.error`. A jar missing failures is a lying dashboard.

## Field conventions

| Field | Convention |
| --- | --- |
| `summary` | Imperative mood, ≤ 120 chars, what changed and why. `Add retry backoff to the webhook dispatcher`, not `I did the thing`. |
| `project` | Free-form; a slug or name resolves and lazily creates the project. Keep it stable, it drives colours and filters. |
| `agent` | The harness name, e.g. `claude-code`, `codex`, `ci-runner`. |
| `model` | The exact model id you were running on. |
| `source` | Where the report came from: `sdk`, `mcp`, `git`, `ci`, `curl`, `webhook`. |
| `status` | `logged` (default; `completed` maps to it), `partial`, `failed`, `dispatched`. |
| `occurred_at` | RFC 3339. Set it when reporting late about work done earlier. |
| `metadata` | JSON object for anything useful: `branch`, `commit`, `pr`, `files_changed`, `tests_added`, `error`, `usage_estimated`. |

## Recipes

### Log a finished unit of work

```bash
curl -sS -X POST "$MARBLE_JAR_URL/v1/marbles" \
  -H "Authorization: Bearer $MARBLE_JAR_API_KEY" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: $(git rev-parse HEAD 2>/dev/null || date -u +%Y%m%dT%H%M%SZ)" \
  -d '{
    "summary": "Add retry backoff to the webhook dispatcher",
    "project": "'"$MJ_PROJECT"'",
    "agent": "'"$MJ_AGENT"'",
    "model": "claude-sonnet-4",
    "source": "sdk",
    "status": "logged",
    "tokens_input": 48210,
    "tokens_output": 6120,
    "cost_usd": 0.42,
    "duration_ms": 315000,
    "metadata": {"branch": "fix/webhook-retry", "tests_added": 3}
  }'
```

The response carries `marble_id`. Keep it if you want to attach usage later.

### Attach usage after the fact (rollup)

Use this when cost/tokens/trace only become known once a trace or billing job
catches up. It overwrites only the fields you send.

```bash
curl -sS -X POST "$MARBLE_JAR_URL/v1/marbles/$MARBLE_ID/rollups" \
  -H "Authorization: Bearer $MARBLE_JAR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"tokens_in": 51000, "tokens_out": 6400, "cost_usd": 0.47, "trace_id": "abc123…"}'
```

### Work inside an objective

Objectives are the project-management tier: they roll up cost, tokens, duration
and budget across their marbles.

```bash
# Find or create the objective for the current epic.
curl -sS "$MARBLE_JAR_URL/v1/objectives?status=open" \
  -H "Authorization: Bearer $MARBLE_JAR_API_KEY"

# Bucket a marble you already logged.
curl -sS -X POST "$MARBLE_JAR_URL/v1/objectives/$OBJECTIVE_ID/marbles/$MARBLE_ID" \
  -H "Authorization: Bearer $MARBLE_JAR_API_KEY"
```

Create objectives only when asked; linking to an existing one is always fine.

### Check the jar before you report

```bash
curl -sS "$MARBLE_JAR_URL/v1/jar/status" -H "Authorization: Bearer $MARBLE_JAR_API_KEY"
```

`daily` gives 14 zero-filled UTC buckets and `streak_days` the current
consecutive-day streak — useful for deciding whether today's work is already
logged before you log it again.

### Search instead of re-doing work

```bash
curl -sSG "$MARBLE_JAR_URL/v1/marbles" \
  -H "Authorization: Bearer $MARBLE_JAR_API_KEY" \
  --data-urlencode "q=webhook" --data-urlencode "unassigned=true" --data-urlencode "limit=20"
```

`q` matches summary, project, agent, model and source.

## Estimating usage when the harness hides it

- Tokens: `ceil(chars / 4)` over the transcript you can see; record it with
  `metadata.usage_estimated: true` and `metadata.estimate_method: "chars/4"`.
- Cost: if the model's rate is known, compute it; otherwise send only tokens and
  leave `cost_usd` out. A missing cost beats a invented one — cost drives the
  jar's marble size, and fake numbers make big fakes.
- Duration: wall-clock of the task in ms. Never round to zero.

## Definition of done

Before you end a turn that finished work, confirm:

- [ ] one `POST /v1/marbles` per completed unit, with a stable idempotency key
- [ ] summary imperative, ≤ 120 chars, secret-free
- [ ] tokens / cost / duration sent, or marked as estimated
- [ ] `project` and `agent` set so the marble is filterable
- [ ] linked to `MJ_OBJECTIVE_ID` when the work belongs to an epic
- [ ] failures logged with `status: "failed"` and `metadata.error`
- [ ] no outbound dispatch unless a human asked for one

## If the report fails

Log-and-continue: a Marble Jar outage must never block the real work. Retry once
with the same idempotency key, then carry on and mention the missed report in
your summary so a human can backfill it.
