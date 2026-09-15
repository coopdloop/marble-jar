#!/usr/bin/env bash
# marble-jar.sh — Marble Jar reporter for shell-based harnesses.
#
#   source ./marble-jar.sh
#   mj_log "Fix the flaky payment test" '{"tokens_input":1200,"cost_usd":0.02}'
#
# Needs curl + jq. git is used for stable idempotency keys when available.
# Every function is safe to re-run: the same idempotency key returns the marble
# that already exists instead of creating a duplicate.

: "${MARBLE_JAR_URL:=http://localhost:8080}"
: "${MARBLE_JAR_API_KEY:?MARBLE_JAR_API_KEY must be set}"
: "${MJ_SOURCE:=shell}"
: "${MJ_MODEL:=}"
: "${MJ_OBJECTIVE_ID:=}"

# Project and agent default to the repository and the harness you are in.
: "${MJ_PROJECT:=$(basename "$(git rev-parse --show-toplevel 2>/dev/null || pwd)")}"
: "${MJ_AGENT:=${CI:-local}}"

_mj_hash() { # stable short digest of stdin-ish text
  if command -v git >/dev/null 2>&1; then
    printf '%s' "$1" | git hash-object --stdin
  else
    printf '%s' "$1" | cksum | cut -d' ' -f1
  fi
}

_mj_post() { # path body [idempotency-key]
  local path=$1 body=$2 idem=${3:-}
  local args=(-sS -X POST "$MARBLE_JAR_URL$path"
    -H "Authorization: Bearer $MARBLE_JAR_API_KEY"
    -H "Content-Type: application/json")
  [[ -n "$idem" ]] && args+=(-H "Idempotency-Key: $idem")
  curl "${args[@]}" -d "$body"
}

_mj_get() { # path [query-string]
  curl -sS "$MARBLE_JAR_URL$1${2:+?$2}" -H "Authorization: Bearer $MARBLE_JAR_API_KEY"
}

# mj_log SUMMARY [EXTRA_JSON] — one completed unit of work.
# EXTRA_JSON is merged into the request body, so it can carry tokens_input,
# tokens_output, cost_usd, duration_ms, status, occurred_at and metadata.
mj_log() {
  local summary=$1 extra=${2:-'{}'}
  local body
  body=$(jq -nc \
    --arg s "$summary" --arg p "$MJ_PROJECT" --arg a "$MJ_AGENT" \
    --arg m "$MJ_MODEL" --arg src "$MJ_SOURCE" --argjson x "$extra" \
    '{summary:$s, project:$p, agent:$a, source:$src}
     + (if $m != "" then {model:$m} else {} end)
     + $x') || { echo "mj_log: EXTRA_JSON is not valid JSON" >&2; return 2; }

  local res
  res=$(_mj_post /v1/marbles "$body" "${MJ_IDEM:-$(_mj_hash "$summary")}")
  echo "$res"

  if [[ -n "$MJ_OBJECTIVE_ID" ]]; then
    local id
    id=$(echo "$res" | jq -r '.marble_id // empty')
    [[ -n "$id" ]] && mj_objective_add "$id" "$MJ_OBJECTIVE_ID" >/dev/null
  fi
}

# mj_log_failed SUMMARY ERROR — failures belong in the jar too.
mj_log_failed() {
  mj_log "$1" "$(jq -nc --arg e "$2" '{status:"failed", metadata:{error:$e}}')"
}

# mj_rollup MARBLE_ID JSON — attach usage or a trace link after the fact.
mj_rollup() {
  _mj_post "/v1/marbles/$1/rollups" "${2:-'{}'}"
}

# mj_objective_add MARBLE_ID [OBJECTIVE_ID]
mj_objective_add() {
  local marble=$1 objective=${2:-$MJ_OBJECTIVE_ID}
  [[ -z "$objective" ]] && { echo "mj_objective_add: no objective id" >&2; return 2; }
  _mj_post "/v1/objectives/$objective/marbles/$marble" '{}'
}

# mj_dispatch MARBLE_ID INTEGRATION [TARGET_CONFIG_JSON] [ACTION]
# Humans own outbound traffic — call this only when one asked you to.
mj_dispatch() {
  local marble=$1 integration=$2
  local cfg=${3:-'{}'} action=${4:-}
  _mj_post "/v1/marbles/$marble/dispatch" \
    "$(jq -nc --arg t "$integration" --arg a "$action" --argjson c "$cfg" \
      '{integration_type:$t} + (if $a != "" then {action:$a} else {} end) + {target_config:$c}')"
}

# mj_search QUERY — summary, project, agent, model and source all match.
mj_search() {
  _mj_get /v1/marbles "q=$(printf %s "$1" | jq -sRr @uri)&limit=${2:-20}"
}

# mj_unassigned — logged marbles not yet bucketed into an objective.
mj_unassigned() { _mj_get /v1/marbles "unassigned=true&limit=${1:-50}"; }

# mj_status — totals, streak and the 14-day daily series.
mj_status() { _mj_get /v1/jar/status; }

# mj_streak — quick "are we still on a roll" one-liner for CI badges.
mj_streak() { mj_status | jq -r '"\(.streak_days)-day streak, \(.marbles_today) marbles today, \(.daily[-1].marbles) in \(.daily[-1].day)"'; }

# mj_export [QUERY] — CSV of everything a filter matches, cursor-walked.
mj_export() {
  local q=${1:-} out=${2:-marbles.csv} cursor=""
  : >"$out"
  local first=1
  while :; do
    local res
    res=$(_mj_get /v1/marbles "limit=200${q:+&$q}${cursor:+&cursor=$cursor}")
    echo "$res" | jq -r '.items[] | [.id, .occurred_at, .summary, .status, .project_name // "", .agent_name // "", .model // "", .source, (.tokens_in // 0), (.tokens_out // 0), (.cost_usd // 0), (.duration_ms // 0)] | @csv' >>"$out"
    if [[ $first == 1 ]]; then
      printf 'id,occurred_at,summary,status,project,agent,model,source,tokens_in,tokens_out,cost_usd,duration_ms\n' | cat - "$out" >"$out.new" && mv "$out.new" "$out"
      first=0
    fi
    cursor=$(echo "$res" | jq -r '.next_cursor // empty')
    [[ -z "$cursor" ]] && break
  done
  echo "$out"
}
