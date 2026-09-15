# Starter templates

Copy-paste files that wire a team's agents and pipelines into Marble Jar, so the
jar becomes the reporting layer instead of a log sink. Every file below lives in
[`templates/`](./templates) in the repository and is rendered with copy and
download buttons on the **Starter templates** tab of this page in the app.

Grab an API key from the dashboard (Settings → API keys). Keys carry scopes; the
reporting templates need `marbles:write` and, for usage rollups, `rollups:write`.
Nothing an agent installs needs `dispatch:write` or `rules:write` — outbound
traffic and automation stay with humans.

```bash
export MARBLE_JAR_URL=http://localhost:8080
export MARBLE_JAR_API_KEY=mj_…
```

## The structure these templates assume

| Tier | What it is | Who writes it |
| --- | --- | --- |
| **Project** | A repo or service. Drives filtering, colours and per-project cost. | Set once in `MJ_PROJECT`. |
| **Objective** | An epic or initiative: rolls up marbles into cost, tokens, duration and budget burn. | Humans, or an agent when asked. |
| **Marble** | One completed unit of work. | Agents, hooks, CI — constantly. |
| **Rule** | Condition → dispatch target. The "tell the team" step, automated. | Humans, reviewed quarterly. |
| **Dispatch** | One outbound action, with retries, dead letters and replay. | The worker, on a rule or a click. |
| **Audit + trends** | Who did what as a person, and how spend moved over time. | Written for you; read in reports. |

That chain is the project-management story: an agent's commit lands as a marble,
a rule routes the ones that matter, the dispatch carries a real person's
identity, and `weekly-report.sh` reads the whole trail back out.

## The library

| File | Put it here | What it buys you |
| --- | --- | --- |
| `AGENTS.md` | repo root, as `AGENTS.md` | The reporting contract: one marble per finished unit, idempotency keys, honest estimates, failures logged, secrets never, and a definition-of-done checklist. |
| `CLAUDE.md` | repo root, beside it | Same contract for Claude Code, plus the `Stop` hook that makes reporting automatic. |
| `marble-jar.sh` | `scripts/` | `mj_log`, `mj_rollup`, `mj_objective_add`, `mj_dispatch`, `mj_search`, `mj_streak`, `mj_export` — one-liners from any shell step. |
| `post-commit.sh` | `.git/hooks/post-commit` | A marble per commit, keyed on the SHA so amends don't double-count. |
| `marble-jar-ci.yml` | `.github/workflows/` | Every CI run reported, usage rolled up later, jar state in the job summary. |
| `dispatch-rules.json` | `POST /v1/rules` | Five starting rules, previewable before you enable them. |
| `weekly-report.sh` | `scripts/` | The team-facing markdown report, straight from the jar. |

## Install in order

```bash
# 1. The contract agents read — download it above, or copy it from the repo.
cp ../path/to/marble-jar/docs/templates/AGENTS.md ./AGENTS.md

# 2. Shell helpers for every other step.
mkdir -p scripts && cp marble-jar.sh scripts/

# 3. Report commits without thinking about it.
cp post-commit.sh .git/hooks/post-commit && chmod +x .git/hooks/post-commit

# 4. Report CI: drop marble-jar-ci.yml in .github/workflows/ and set the two secrets.

# 5. Route the marbles that matter. Preview first — the test endpoint answers
#    "how many of my last 50 marbles would this have fired on?"
jq -c '.rules[]' dispatch-rules.json | while read -r rule; do
  curl -sS -X POST "$MARBLE_JAR_URL/v1/rules" \
    -H "Authorization: Bearer $MARBLE_JAR_API_KEY" \
    -H "Content-Type: application/json" -d "$rule"
done

# 6. Read it back for the team.
./weekly-report.sh > report.md
```

Step 5 preview, one condition at a time:

```bash
curl -sS -X POST "$MARBLE_JAR_URL/v1/rules/test" \
  -H "Authorization: Bearer $MARBLE_JAR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"condition": {"field":"status","op":"eq","value":"failed"}, "sample": 50}'
```

## Making it yours

The templates deliberately cover only what Marble Jar can guarantee:

- **Set `MJ_PROJECT` per repository** — mixed-up projects make cost numbers
  meaningless and the jar's colours stop meaning anything.
- **Pin `MJ_OBJECTIVE_ID` where an epic exists**, or add a
  `Marble-Objective: <uuid>` trailer and let the hook link it.
- **Estimate, but say so.** The hook marks `usage_estimated: true` because diff
  size is a proxy; if your harness knows real token counts, send them and the
  estimate falls away.
- **One failure path per report.** `mj_log_failed` sets `status: "failed"`, which
  is what the Jira rule keys on — a failure logged as `logged` never gets routed.
- **Keep dispatching human.** Rules and the ship endpoint exist so a person
  decides how work gets announced.
