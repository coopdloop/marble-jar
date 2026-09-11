# AGENTS.md

Guidance for coding agents working in this repository, authored so that agents
can commit and push correctly with the `super-commit` CLI.

## Committing with super-commit

[`super-commit`](https://pypi.org/project/super-commit-cli/) is a CLI that
creates **conventional commits** and pushes them. It has two subcommands:
`commit` (stage + commit) and `push` (push the current branch, optionally open
a GitHub PR).

**Agents must run it non-interactively.** Without the right flags it opens an
interactive picker that will hang an agent session. Always pass `--yes` and an
explicit type/subject, and either `--all` or `--files`.

### Staging

- `--all` / `-a` — stage every modified and untracked file. Use this only when
  `git status` shows exactly the change set you intend to commit.
- `--files` / `-f "path1,path2"` — stage specific paths. **Prefer this** when
  the working tree contains unrelated changes (it usually does).
- Never stage caches or build artifacts (`*.tsbuildinfo`, binaries,
  `dist/`, etc.) even when using `-a` checks; if they're already tracked,
  fine—otherwise leave them out.

### Message format (conventional commits)

Use one of: `feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`,
`chore`, `ci`, `revert`. Optional `--scope`. Optional `--breaking` flag.
Optional `--body` and `--footer` (e.g. `Closes #42`).

Common scopes here: `core`, `web`, `mcp-bridge`, `sdk`, `docs`, `deploy`.

```bash
super-commit commit \
  -f "README.md,AGENTS.md" \
  --type docs \
  --scope docs \
  --subject "whatever changed" \
  --body "a sentence or two on the why" \
  --yes
```

`--subject` must be a complete summary sentence in imperative mood, starting
lowercase (e.g. "add OBO token exchange fallback"), no trailing period. The
linter rejects uppercase starts.

### Pushing

`super-commit commit --push -y` pushes after committing, or use the separate
command:

```bash
super-commit push --yes                 # push current branch to origin
super-commit push --create-pr --base main --yes   # also open a GitHub PR
```

Opening a PR (`--create-pr` on either subcommand) requires `GITHUB_TOKEN` in
the environment; check it exists before offering a PR. Default remote is
`origin`, base defaults to `main`.

### Bad habits to avoid

- **Never** run `super-commit commit` without `-y` — it blocks on prompts.
- **Never** run `git commit -a` or `git add -A` instead; always go through
  `super-commit` so the history stays conventional.
- Use `--dry-run` (`-n`) when unsure; it prints what would be staged/committed.
- If `git status` is empty, don't commit — just push with `super-commit push -y`.

---

## Repository map

- `services/core` — Go modular monolith (API + dispatch worker), tests via
  `go test ./...` (skips without Postgres unless `MARBLEJAR_TEST_DATABASE_URL`).
- `services/mcp-bridge` — Python MCP server.
- `apps/web` — React/TS frontend, `npm run typecheck`.
- `packages/sdk-ts` — TS client, `npm run typecheck`.
- `docs/` — documentation, rendered in-app at `/docs`.
- `deploy/` — deployment manifests.

Run the typecheck/test that matches your change before committing.
