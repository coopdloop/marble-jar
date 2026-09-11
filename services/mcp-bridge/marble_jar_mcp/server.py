"""Marble Jar MCP servers.

Two logical servers are exposed:

* ``marble-jar``          — the ``log_marble`` tool agent harnesses call when
  they finish a unit of work.
* ``marble-jar-monitor``  — read-only tools for querying jar status, objective
  progress, budget health and cost trends.

Both are served over stdio (for Claude Code / Cursor style harnesses) and over
HTTP (for dashboards and remote agents).
"""

from __future__ import annotations

import json
import logging
from typing import Any

from mcp.server import Server
from mcp.types import TextContent, Tool

from .config import Settings, load_settings
from .core_client import CoreAPIError, CoreClient

log = logging.getLogger(__name__)


def _text(payload: Any) -> list[TextContent]:
    """MCP tool results are text; JSON keeps them machine-readable."""
    if isinstance(payload, str):
        return [TextContent(type="text", text=payload)]
    return [TextContent(type="text", text=json.dumps(payload, indent=2, default=str))]


LOG_MARBLE_TOOL = Tool(
    name="log_marble",
    description=(
        "Log a completed unit of agent work as a marble in the team's jar. "
        "Call this once you finish a discrete task so humans can see, review "
        "and dispatch the result. Include cost/token/duration when known, and "
        "a trace_id if the run is instrumented with OpenTelemetry/Phoenix."
    ),
    inputSchema={
        "type": "object",
        "properties": {
            "summary": {
                "type": "string",
                "description": "One-line description of what was accomplished.",
            },
            "model": {"type": "string", "description": "Model that performed the work."},
            "project": {
                "type": "string",
                "description": "Project/repo slug this work belongs to (auto-created if new).",
            },
            "agent": {"type": "string", "description": "Agent identity, e.g. 'claude-code'."},
            "harness": {"type": "string", "description": "Harness name, e.g. 'claude-code', 'cursor'."},
            "status": {
                "type": "string",
                "enum": ["completed", "failed", "partial"],
                "description": "Outcome of the unit of work.",
            },
            "tokens_in": {"type": "integer", "minimum": 0},
            "tokens_out": {"type": "integer", "minimum": 0},
            "cost_usd": {"type": "number", "minimum": 0},
            "duration_ms": {"type": "integer", "minimum": 0},
            "trace_id": {
                "type": "string",
                "description": "OTel/Phoenix trace id for full lineage linking.",
            },
            "objective_id": {
                "type": "string",
                "description": "Optional objective (bigger jar) to bucket this marble into.",
            },
            "metadata": {"type": "object", "description": "Arbitrary structured context."},
            "idempotency_key": {
                "type": "string",
                "description": "Makes retries safe; replaying a key returns the original marble.",
            },
        },
        "required": ["summary", "model"],
    },
)

MONITOR_TOOLS = [
    Tool(
        name="get_jar_status",
        description=(
            "Get the current state of the marble jar: total/today/last-hour marble "
            "counts, spend and token usage today, open objectives, pending dispatches "
            "and the most-used models."
        ),
        inputSchema={"type": "object", "properties": {}},
    ),
    Tool(
        name="get_objective_progress",
        description=(
            "Get progress and cost/time/token rollups for a specific objective, "
            "including percent-of-budget consumed and a generated summary preview."
        ),
        inputSchema={
            "type": "object",
            "properties": {
                "objective_id": {"type": "string", "description": "Objective UUID."},
            },
            "required": ["objective_id"],
        },
    ),
    Tool(
        name="check_budget_status",
        description=(
            "Answer 'am I over budget?' for one objective, or across all open "
            "objectives when no id is given. Returns per-objective budget usage and "
            "a clear over/under verdict."
        ),
        inputSchema={
            "type": "object",
            "properties": {
                "objective_id": {
                    "type": "string",
                    "description": "Optional objective UUID; omit to check every open objective.",
                },
            },
        },
    ),
    Tool(
        name="get_cost_trends",
        description="Get cost or token usage trends bucketed over time.",
        inputSchema={
            "type": "object",
            "properties": {
                "metric": {"type": "string", "enum": ["cost", "tokens"], "default": "cost"},
                "interval": {
                    "type": "string",
                    "enum": ["hour", "day", "week", "month"],
                    "default": "day",
                },
                "days": {"type": "integer", "minimum": 1, "maximum": 365, "default": 30},
                "objective_id": {"type": "string"},
            },
        },
    ),
    Tool(
        name="list_recent_marbles",
        description="List the most recent marbles, optionally filtered by project or model.",
        inputSchema={
            "type": "object",
            "properties": {
                "limit": {"type": "integer", "minimum": 1, "maximum": 100, "default": 20},
                "project": {"type": "string"},
                "model": {"type": "string"},
            },
        },
    ),
]


async def call_log_marble(core: CoreClient, settings: Settings, args: dict[str, Any]) -> dict[str, Any]:
    """Normalize MCP tool args and forward them to core's ingestion API."""
    summary = (args.get("summary") or "").strip()
    model = (args.get("model") or "").strip()
    if not summary:
        raise ValueError("summary is required")
    if not model:
        raise ValueError("model is required")

    payload: dict[str, Any] = {
        "summary": summary,
        "model": model,
        "source": "mcp",
        "project": args.get("project"),
        "agent": args.get("agent"),
        "harness": args.get("harness"),
        "status": args.get("status"),
        "cost_usd": args.get("cost_usd"),
        "duration_ms": args.get("duration_ms"),
        "objective_id": args.get("objective_id"),
        "metadata": args.get("metadata") or {},
    }

    tokens_in, tokens_out = args.get("tokens_in"), args.get("tokens_out")
    if tokens_in is not None or tokens_out is not None:
        payload["tokens"] = {"in": tokens_in, "out": tokens_out}

    # Attach the Phoenix deep link so the marble carries full trace lineage.
    trace_id = args.get("trace_id")
    if trace_id:
        payload["trace_id"] = trace_id
        if url := settings.phoenix_trace_url(trace_id):
            payload["phoenix_trace_url"] = url

    payload = {k: v for k, v in payload.items() if v is not None}
    return await core.log_marble(payload, idempotency_key=args.get("idempotency_key"))


async def call_budget_status(core: CoreClient, args: dict[str, Any]) -> dict[str, Any]:
    """Evaluate budget health for one or all open objectives."""
    objective_id = args.get("objective_id")

    if objective_id:
        detail = await core.get_objective(objective_id)
        objectives = [{**detail["objective"], "rollup": detail.get("rollup")}]
    else:
        listing = await core.list_objectives(status="open", limit=100)
        objectives = listing.get("items", [])

    results = []
    for obj in objectives:
        rollup = obj.get("rollup") or {}
        cost_pct = rollup.get("budget_pct_cost")
        token_pct = rollup.get("budget_pct_tokens")
        over = any(p is not None and p > 100 for p in (cost_pct, token_pct))
        warning = any(p is not None and 80 <= p <= 100 for p in (cost_pct, token_pct))

        results.append(
            {
                "objective_id": obj["id"],
                "title": obj["title"],
                "status": obj["status"],
                "marble_count": rollup.get("marble_count", 0),
                "cost_usd": rollup.get("cost_usd", 0),
                "total_tokens": rollup.get("total_tokens", 0),
                "budget_cost_usd": obj.get("budget_cost_usd"),
                "budget_tokens": obj.get("budget_tokens"),
                "cost_budget_pct": cost_pct,
                "token_budget_pct": token_pct,
                "verdict": "over_budget" if over else ("approaching_budget" if warning else "within_budget"),
            }
        )

    over_count = sum(1 for r in results if r["verdict"] == "over_budget")
    return {
        "checked": len(results),
        "over_budget_count": over_count,
        "any_over_budget": over_count > 0,
        "objectives": results,
    }


def build_log_marble_server(core: CoreClient, settings: Settings) -> Server:
    """The primary MCP server agents use to report completed work."""
    server: Server = Server("marble-jar")

    @server.list_tools()
    async def list_tools() -> list[Tool]:
        return [LOG_MARBLE_TOOL]

    @server.call_tool()
    async def call_tool(name: str, arguments: dict[str, Any]) -> list[TextContent]:
        if name != "log_marble":
            return _text({"error": f"unknown tool: {name}"})
        try:
            res = await call_log_marble(core, settings, arguments or {})
        except (CoreAPIError, ValueError) as err:
            log.warning("log_marble failed: %s", err)
            return _text({"error": str(err), "logged": False})

        marble = res.get("marble", {})
        return _text(
            {
                "logged": True,
                "marble_id": res.get("marble_id"),
                "created": res.get("created", True),
                "summary": marble.get("summary"),
                "trace_url": marble.get("phoenix_trace_url"),
                "message": "Marble added to the jar.",
            }
        )

    return server


def build_monitor_server(core: CoreClient) -> Server:
    """The read-only MCP server for querying jar and objective state."""
    server: Server = Server("marble-jar-monitor")

    @server.list_tools()
    async def list_tools() -> list[Tool]:
        return MONITOR_TOOLS

    @server.call_tool()
    async def call_tool(name: str, arguments: dict[str, Any]) -> list[TextContent]:
        args = arguments or {}
        try:
            if name == "get_jar_status":
                return _text(await core.jar_status())

            if name == "get_objective_progress":
                objective_id = args.get("objective_id")
                if not objective_id:
                    return _text({"error": "objective_id is required"})
                return _text(await core.get_objective(objective_id))

            if name == "check_budget_status":
                return _text(await call_budget_status(core, args))

            if name == "get_cost_trends":
                metric = args.get("metric", "cost")
                return _text(
                    await core.trends(
                        "tokens" if metric == "tokens" else "cost",
                        interval=args.get("interval", "day"),
                        days=args.get("days", 30),
                        objective_id=args.get("objective_id"),
                    )
                )

            if name == "list_recent_marbles":
                return _text(
                    await core.list_marbles(
                        limit=args.get("limit", 20),
                        project=args.get("project"),
                        model=args.get("model"),
                    )
                )

            return _text({"error": f"unknown tool: {name}"})
        except CoreAPIError as err:
            log.warning("monitor tool %s failed: %s", name, err)
            return _text({"error": str(err)})

    return server


__all__ = [
    "build_log_marble_server",
    "build_monitor_server",
    "call_log_marble",
    "call_budget_status",
    "LOG_MARBLE_TOOL",
    "MONITOR_TOOLS",
    "load_settings",
]
