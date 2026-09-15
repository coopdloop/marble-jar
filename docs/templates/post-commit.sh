#!/usr/bin/env bash
# post-commit — log one marble per git commit to Marble Jar.
#
#   cp post-commit.sh .git/hooks/post-commit && chmod +x .git/hooks/post-commit
#
# The commit SHA is the idempotency key, so amending and re-committing does not
# double-count, and a Marble Jar outage never blocks the commit itself.

set -uo pipefail

REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || exit 0
LIB="${MARBLE_JAR_LIB:-$REPO_ROOT/scripts/marble-jar.sh}"

# Fall back to plain curl when the helper library is not installed.
if [[ -f "$LIB" ]]; then
  # shellcheck disable=SC1090
  source "$LIB"
else
  echo "post-commit: $LIB not found, using inline reporter" >&2
fi

SHA=$(git rev-parse HEAD)
SHORT=$(git rev-parse --short HEAD)
SUBJECT=$(git log -1 --pretty=%s)
BRANCH=$(git rev-parse --abbrev-ref HEAD)
AUTHOR=$(git log -1 --pretty=%an)

# Diff size, used for the token estimate (see docs: chars/4, files as metadata).
NUMSTAT=$(git show --numstat --format= --no-color "$SHA" 2>/dev/null || true)
ADDED=$(echo "$NUMSTAT" | awk -F'\t' '{a+=$1} END{print a+0}')
DELETED=$(echo "$NUMSTAT" | awk -F'\t' '{d+=$2} END{print d+0}')
FILES=$(echo "$NUMSTAT" | grep -c '[^/]' || true)
DIFF_CHARS=$(git show --format= --no-color "$SHA" 2>/dev/null | wc -c | tr -d ' ')
EST_TOKENS=$(( (DIFF_CHARS + 3) / 4 ))

# Objective can be pinned in the environment or in a commit trailer.
OBJECTIVE=${MJ_OBJECTIVE_ID:-$(git log -1 --pretty=%B | sed -n 's/^Marble-Objective: //p' | tail -1)}
PROJECT=${MJ_PROJECT:-$(basename "$REPO_ROOT")}
AGENT=${MJ_AGENT:-git-hook}
MODEL=${MJ_MODEL:-}

BODY=$(jq -nc \
  --arg s "$SUBJECT" \
  --arg project "$PROJECT" \
  --arg agent "$AGENT" \
  --arg model "$MODEL" \
  --arg branch "$BRANCH" \
  --arg sha "$SHA" \
  --arg author "$AUTHOR" \
  --arg objective "$OBJECTIVE" \
  --argjson files "$FILES" \
  --argjson added "$ADDED" \
  --argjson deleted "$DELETED" \
  --argjson tokens "$EST_TOKENS" \
  '{summary: $s,
    project: $project,
    agent: $agent,
    status: "logged",
    source: "git",
    tokens_input: $tokens,
    metadata: {
      branch: $branch,
      commit: $sha,
      author: $author,
      files_changed: $files,
      lines_added: $added,
      lines_deleted: $deleted,
      usage_estimated: true,
      estimate_method: "diff chars/4"
    }
   }
   + (if $model != "" then {model: $model} else {} end)
   + (if $objective != "" then {objective_id: $objective} else {} end)')

report() {
  curl -sS -X POST "${MARBLE_JAR_URL:-http://localhost:8080}/v1/marbles" \
    -H "Authorization: Bearer $MARBLE_JAR_API_KEY" \
    -H "Content-Type: application/json" \
    -H "Idempotency-Key: commit-$SHA" \
    -d "$BODY"
}

if [[ -z "${MARBLE_JAR_API_KEY:-}" ]]; then
  echo "post-commit: MARBLE_JAR_API_KEY unset, skipping marble for $SHORT" >&2
  exit 0
fi

# Never fail a commit because reporting failed.
report >/dev/null 2>&1 || \
  echo "post-commit: could not reach Marble Jar, commit $SHORT not reported" >&2
