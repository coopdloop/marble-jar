# CLAUDE.md — Marble Jar reporting

This repository reports agent work to [Marble Jar](./AGENTS.md). The full
contract lives in `AGENTS.md` next to this file; **read it, it also applies to
you**: @AGENTS.md

## Claude Code specifics

- This file is fine as the only project context if you keep `AGENTS.md` beside
  it — the `@AGENTS.md` import above pulls the contract in.
- For an org-wide default, paste the "The contract" and "Definition of done"
  sections into `~/.claude/CLAUDE.md`.
- Credentials come from the environment, never from this file:
  `MARBLE_JAR_URL` and `MARBLE_JAR_API_KEY`.

## Making reporting automatic

Hooks remove the "remember to log it" failure mode:

```json
{
  "hooks": {
    "Stop": [
      {
        "hooks": [
          { "type": "command", "command": "./scripts/marble-report.sh" }
        ]
      }
    ]
  }
}
```

`scripts/marble-report.sh` is the `marble-jar.sh` template sourced by a tiny
wrapper; run at `Stop` it reports whatever the session completed. Idempotency
keys make double-firing harmless, so wire it up and stop thinking about it.
