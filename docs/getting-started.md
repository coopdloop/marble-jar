# Getting started

A ten-minute lap around every feature, done by hand. The in-app **guided tour**
(this page, first tab) tracks these same steps live and can do some of them
for you.

## 1. Bring the stack up

```bash
docker compose up -d postgres redis redpanda phoenix

cd services/core && go run ./cmd/server     # runs migrations on boot
cd apps/web && npm install && npm run dev   # http://localhost:5173
```

`DEV_MODE=true` relaxes the secret-strength checks for local play; without it
set `JWT_SIGNING_KEY` (32+ bytes) and `HMAC_DISPATCH_SECRET`.

## 2. Sign in with Google

Put `GOOGLE_OAUTH_CLIENT_ID` and `GOOGLE_OAUTH_CLIENT_SECRET` in `.env` (see
[Configuration](./configuration.md) for the credentials to create), restart the
stack, then open <http://localhost:5173/login> and click **Sign in with
Google**. There are no passwords: the first account to sign in is provisioned
with its own workspace and owns it.

Accounts from the old email/password flow are `auth_provider = 'local'` with no
provider subject: signing in with a matching verified Google email claims such
an account (role and history intact), or clear them once with
`make reset-users`.

To bring a teammate into your workspace, use **Settings → Invite a teammate** and
send them the link — a join link is the only way to land in someone else's jar.

## 3. Create an API key

**Settings → API keys → New key.** Copy the `mj_…` token — it is shown once.
This is how agents (and curl, and the MCP bridge) authenticate.

## 4. Log your first marble

```bash
curl -X POST http://localhost:8080/v1/marbles \
  -H "Authorization: Bearer mj_your_key" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: demo-1" \
  -d '{"summary":"Refactored the auth module","model":"claude-sonnet-4","project":"payments-api","cost_usd":0.42,"tokens_input":12000,"tokens_output":3000,"duration_ms":45000}'
```

Switch to the **Queue** page and watch it drop into the jar in real time.
Click any marble to open its detail sheet — cost, tokens, trace link, and its
dispatch history.

## 5. Create an objective

**Objectives → New objective.** Give it a title ("Q4 Auth Hardening") and
optionally a cost or token budget. Back on the list, drag a marble from the
*Unassigned marbles* table onto the objective card to bucket it. Open the
objective to see rollups — marble count, spend, tokens, duration — and budget
bars.

## 6. Dispatch an update, by hand

Open a marble's detail sheet and choose **Dispatch** → Slack / Jira / Teams /
webhook. The dispatch worker executes it with retries and backoff; the result
lands in the marble's history and the audit trail. Webhooks work with zero
setup — HMAC-signed with `HMAC_DISPATCH_SECRET`. Jira/Slack/Teams need the
OAuth setup in [Integrations & OBO](./integrations.md).

## 7. Automate it with a rule

**Rules → New rule.** Build conditions with the chip editor
(`project eq payments-api`, `cost_usd gt 0.5` …), watch the live preview show
which recent marbles would have matched, pick a target (Slack channel, Jira
project key, Teams, or webhook URL) and save. Every new marble that matches is
dispatched automatically — still on-behalf-of a person.

## 8. Ship an objective

When an objective is done, open it and hit **Ship It**: Marble Jar composes a
rollup summary (marbles, cost, tokens, duration) and dispatches it to your
chosen channel — the standup update writes itself.

## 9. Connect OBO integrations

**Integrations** shows Jira, Slack and Teams. Once
[the OAuth secrets are configured](./integrations.md), **Connect** starts the
on-behalf-of flow so dispatches act as you. The audit trail at the bottom of
the page records who did what, when.

## 10. Watch it live

Leave the Queue page open on a second screen. The WebSocket feed pushes new
marbles the moment agents report them; the header pill shows `Live` while the
socket is connected and falls back to polling otherwise.

## 11. Read the numbers back

**Insights** charts spend, tokens and marble volume over a rolling window. Cut
it by interval (hour/day/week), window length, project, objective or model —
useful for "what did the agents cost us this week, and where did it go?"

## 12. Triage what did not land

**Dispatches** is the stuck-work queue: anything that failed or was
dead-lettered, with the provider's error message next to it. (Rows that are
`retrying` are still being worked, and the server refuses to replay them — as
well as anything already succeeded.) Hit **Replay** to re-queue one after you
fix the cause (bad channel, expired OBO token, endpoint down). Succeeded rows
stay visible so the external ref is one click away.

## 13. Prove who did what

**Audit log** is the full on-behalf-of trail — provider, action, the agent
identity that triggered it and the human it acted as. Filter by provider or
action, or paste a marble / dispatch ID for an exact lookup, and expand a row to
see the payload that went out. Credential-shaped fields (webhook URLs, tokens,
secrets) are redacted before they ever reach the log.
