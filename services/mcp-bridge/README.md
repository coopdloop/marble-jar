# Marble Jar MCP Bridge

Hosts both Marble Jar MCP servers and the Phoenix/OpenInference trace bridge.

## Servers

| Server | Purpose |
| --- | --- |
| `marble-jar` | `log_marble` — agents report completed units of work |
| `marble-jar-monitor` | `get_jar_status`, `get_objective_progress`, `check_budget_status`, `get_cost_trends`, `list_recent_marbles` |

## Running

```bash
export CORE_API_BASE_URL=http://localhost:8080
export CORE_SERVICE_TOKEN=mj_...        # API key from Settings -> Tokens
export PHOENIX_BASE_URL=http://localhost:6006

marble-jar-mcp --server http       # FastAPI bridge on :8090
marble-jar-mcp --server log        # stdio MCP (log_marble)
marble-jar-mcp --server monitor    # stdio MCP (monitor tools)
```

## Claude Code / Cursor configuration

```json
{
  "mcpServers": {
    "marble-jar": {
      "command": "marble-jar-mcp",
      "args": ["--server", "log"],
      "env": {
        "CORE_API_BASE_URL": "http://localhost:8080",
        "CORE_SERVICE_TOKEN": "mj_your_key_here"
      }
    }
  }
}
```

`GET /mcp/config/discover` returns this snippet pre-filled, plus an
`agents.md` block you can paste straight into a repo.

## Phoenix reconciliation

Traces sometimes land after the marble does. `POST /v1/reconcile` backfills
cost/token rollups from Phoenix; poll `GET /v1/reconcile/{job_id}` for status.
