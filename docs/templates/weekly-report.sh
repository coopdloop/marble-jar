#!/usr/bin/env bash
# weekly-report.sh — a team-facing status report built from the jar.
#
#   ./weekly-report.sh > report.md            # write markdown
#   DAYS=14 ./weekly-report.sh                # two-week window
#   ./weekly-report.sh --ship OBJ_ID slack    # print, then dispatch it to a channel
#
# It reads only: jar/status, trends, objectives, dispatches, marbles. Needs
# curl + jq. Dispatching the finished report needs a key with dispatch:write,
# which is exactly the permission a human should be holding, not an agent.

set -euo pipefail

BASE=${MARBLE_JAR_URL:-http://localhost:8080}
KEY=${MARBLE_JAR_API_KEY:?MARBLE_JAR_API_KEY must be set}
DAYS=${DAYS:-7}

get() { curl -sS "$BASE$1" -H "Authorization: Bearer $KEY"; }

STATUS=$(get /v1/jar/status)
TRENDS=$(get "/v1/trends/cost?interval=day&days=$DAYS")
OBJECTIVES=$(get "/v1/objectives?status=open&limit=50")
UNASSIGNED=$(get "/v1/marbles?unassigned=true&limit=10")
STUCK=$(get "/v1/dispatches?status=failed,dead_lettered&limit=10")

REPORT=$(
  STATUS="$STATUS" TRENDS="$TRENDS" OBJECTIVES="$OBJECTIVES" \
  UNASSIGNED="$UNASSIGNED" STUCK="$STUCK" DAYS="$DAYS" \
  jq -rn '
    def n($x): ($x // 0);
    env.STATUS | fromjson | . as $s |
    env.TRENDS | fromjson | . as $t |
    env.OBJECTIVES | fromjson | . as $o |
    env.UNASSIGNED | fromjson | . as $u |
    env.STUCK | fromjson | . as $k |
    ($t.points // []) as $p |
    ($p | map(n(.marble_count)) | add // 0) as $periodMarbles |
    ($p | map(n(.cost_usd)) | add // 0) as $periodCost |
    ($p | map(n(.tokens_in) + n(.tokens_out)) | add // 0) as $periodTokens |
    ($p | map(n(.duration_ms)) | add // 0) as $periodMs |
    "# Marble Jar — last \(env.DAYS | tonumber) days",
    "",
    "- **\($periodMarbles)** marbles logged · **\($periodCost * 100000 | round / 100000)** USD · **\($periodTokens)** tokens · **\(($periodMs / 3600000 * 100 | round) / 100)** h of agent time",
    "- Today: **\($s.marbles_today)** marbles, streak **\($s.streak_days)** days",
    "- Total in jar: **\($s.marbles_total)**",
    "",
    "## Objectives in flight",
    (($o.items // []) | map(
        "- **\(.title)** — \(.rollup.marble_count // 0) marbles, " +
        "\(((.rollup.cost_usd // 0) * 100 | round) / 100) USD" +
        (if .budget_cost_usd then " of \(.budget_cost_usd) USD budget (\(((((.rollup.cost_usd // 0) / .budget_cost_usd) * 100) | round)))%" else "" end)
      ) | join("\n") | if . == "" then "_none open_" else . end),
    "",
    "## Needs a decision",
    (($u.items // []) | map("- unassigned: \(.summary) (\(.project_name // "no project"))") | join("\n")
      | if . == "" then "_nothing unassigned_" else . end),
    (($k.items // []) | map("- stuck dispatch: \(.integration_type)/\(.action) — \(.error_message // .status)") | join("\n")
      | if . == "" then "_no failed or dead-lettered dispatches_" else . end),
    "",
    "## Daily activity",
    (($s.daily // []) | map("- `\(.day)` \(("█" * ([.marbles, 20] | min)))  \(.marbles)") | join("\n"))
  '
)

echo "$REPORT"

if [[ ${1:-} == "--ship" ]]; then
  OBJECTIVE_ID=${2:?usage: --ship OBJECTIVE_ID [integration]}
  INTEGRATION=${3:-slack}
  curl -sS -X POST "$BASE/v1/objectives/$OBJECTIVE_ID/ship" \
    -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
    -d "$(jq -nc --arg t "$INTEGRATION" --arg s "$REPORT" \
          '{integration_type:$t, summary_override:$s, mark_shipped:false}')"
  echo "shipped to $INTEGRATION" >&2
fi
