"""FastAPI surface for the MCP bridge.

Exposes the MCP tools over plain HTTP (for dashboards and remote agents that
do not speak stdio MCP), plus health and the Phoenix reconciliation job used to
backfill marbles whose trace data arrived late.
"""

from __future__ import annotations

import asyncio
import logging
import uuid
from contextlib import asynccontextmanager
from datetime import datetime, timezone
from typing import Any, Literal

import httpx
from fastapi import APIRouter, Depends, FastAPI, Header, HTTPException
from pydantic import BaseModel, Field

from .config import Settings, load_settings
from .core_client import CoreAPIError, CoreClient
from .server import LOG_MARBLE_TOOL, MONITOR_TOOLS, call_budget_status, call_log_marble

log = logging.getLogger(__name__)

# In-memory reconciliation job registry. Jobs are short-lived and advisory;
# losing them on restart is acceptable since reconciliation is idempotent.
_jobs: dict[str, dict[str, Any]] = {}


class LogMarbleBody(BaseModel):
    summary: str = Field(min_length=1)
    model: str = Field(min_length=1)
    project: str | None = None
    agent: str | None = None
    harness: str | None = None
    status: Literal["completed", "failed", "partial"] | None = None
    tokens_in: int | None = Field(default=None, ge=0)
    tokens_out: int | None = Field(default=None, ge=0)
    cost_usd: float | None = Field(default=None, ge=0)
    duration_ms: int | None = Field(default=None, ge=0)
    trace_id: str | None = None
    objective_id: str | None = None
    metadata: dict[str, Any] | None = None
    idempotency_key: str | None = None


class ReconcileBody(BaseModel):
    """Backfill rollups for marbles whose Phoenix spans arrived after ingest."""

    marble_ids: list[str] | None = None
    since_hours: int = Field(default=24, ge=1, le=720)
    phoenix_project: str | None = None


def create_app(settings: Settings | None = None) -> FastAPI:
    settings = settings or load_settings()

    @asynccontextmanager
    async def lifespan(app: FastAPI):
        app.state.settings = settings
        app.state.core = CoreClient(settings)
        app.state.phoenix = (
            httpx.AsyncClient(
                base_url=settings.phoenix_base_url,
                timeout=httpx.Timeout(30.0),
                headers=(
                    {"Authorization": f"Bearer {settings.phoenix_api_key}"}
                    if settings.phoenix_api_key
                    else {}
                ),
            )
            if settings.has_phoenix
            else None
        )
        log.info("mcp bridge ready (core=%s phoenix=%s)", settings.core_base_url,
                 settings.phoenix_base_url or "disabled")
        try:
            yield
        finally:
            await app.state.core.aclose()
            if app.state.phoenix is not None:
                await app.state.phoenix.aclose()

    app = FastAPI(
        title="Marble Jar MCP Bridge",
        version="0.1.0",
        description="MCP servers for logging and monitoring marbles, plus the Phoenix trace bridge.",
        lifespan=lifespan,
    )

    def get_core() -> CoreClient:
        return app.state.core

    def get_settings() -> Settings:
        return app.state.settings

    # ---------- health ----------

    @app.get("/health", tags=["ops"])
    async def health(core: CoreClient = Depends(get_core)) -> dict[str, Any]:
        core_ok = True
        try:
            await core.health()
        except Exception:  # noqa: BLE001 - health must never raise
            core_ok = False
        return {
            "status": "ok" if core_ok else "degraded",
            "core_api": "up" if core_ok else "down",
            "phoenix": "configured" if app.state.settings.has_phoenix else "disabled",
        }

    @app.get("/metrics", tags=["ops"])
    async def metrics() -> dict[str, Any]:
        return {
            "reconcile_jobs_total": len(_jobs),
            "reconcile_jobs_running": sum(1 for j in _jobs.values() if j["status"] == "running"),
        }

    # ---------- MCP over HTTP ----------

    mcp = APIRouter(prefix="/mcp", tags=["mcp"])

    def _tool_json(tool: Any) -> dict[str, Any]:
        # The MCP SDK renamed inputSchema -> input_schema across versions.
        schema = getattr(tool, "input_schema", None) or getattr(tool, "inputSchema", None)
        return {
            "name": tool.name,
            "description": tool.description,
            "inputSchema": schema,
        }

    @mcp.get("/tools")
    async def list_tools() -> dict[str, Any]:
        return {
            "servers": {
                "marble-jar": [_tool_json(LOG_MARBLE_TOOL)],
                "marble-jar-monitor": [_tool_json(t) for t in MONITOR_TOOLS],
            }
        }

    @mcp.post("/tools/log_marble")
    async def http_log_marble(
        body: LogMarbleBody,
        core: CoreClient = Depends(get_core),
        settings: Settings = Depends(get_settings),
    ) -> dict[str, Any]:
        try:
            res = await call_log_marble(core, settings, body.model_dump(exclude_none=True))
        except CoreAPIError as err:
            raise HTTPException(status_code=err.status, detail=err.message) from err
        except ValueError as err:
            raise HTTPException(status_code=400, detail=str(err)) from err
        return res

    @mcp.post("/tools/get_jar_status")
    async def http_jar_status(core: CoreClient = Depends(get_core)) -> dict[str, Any]:
        return await _guard(core.jar_status())

    @mcp.post("/tools/get_objective_progress")
    async def http_objective_progress(
        payload: dict[str, Any], core: CoreClient = Depends(get_core)
    ) -> dict[str, Any]:
        objective_id = payload.get("objective_id")
        if not objective_id:
            raise HTTPException(status_code=400, detail="objective_id is required")
        return await _guard(core.get_objective(objective_id))

    @mcp.post("/tools/check_budget_status")
    async def http_budget_status(
        payload: dict[str, Any] | None = None, core: CoreClient = Depends(get_core)
    ) -> dict[str, Any]:
        return await _guard(call_budget_status(core, payload or {}))

    @mcp.post("/tools/get_cost_trends")
    async def http_cost_trends(
        payload: dict[str, Any] | None = None, core: CoreClient = Depends(get_core)
    ) -> dict[str, Any]:
        p = payload or {}
        metric = "tokens" if p.get("metric") == "tokens" else "cost"
        return await _guard(
            core.trends(
                metric,
                interval=p.get("interval", "day"),
                days=p.get("days", 30),
                objective_id=p.get("objective_id"),
            )
        )

    @mcp.get("/config/discover")
    async def discover_config(
        settings: Settings = Depends(get_settings),
        x_workspace: str | None = Header(default=None, alias="X-Workspace"),
    ) -> dict[str, Any]:
        """Returns a ready-to-paste agents.md / MCP client configuration."""
        return {
            "core_api": settings.core_base_url,
            "mcp_http": f"http://localhost:{settings.mcp_port}/mcp",
            "phoenix": settings.phoenix_base_url or None,
            "workspace_hint": x_workspace,
            "mcp_client_config": {
                "mcpServers": {
                    "marble-jar": {
                        "command": "marble-jar-mcp",
                        "args": ["--server", "log"],
                        "env": {
                            "CORE_API_BASE_URL": settings.core_base_url,
                            "CORE_SERVICE_TOKEN": "${MARBLE_JAR_API_KEY}",
                        },
                    },
                    "marble-jar-monitor": {
                        "command": "marble-jar-mcp",
                        "args": ["--server", "monitor"],
                        "env": {
                            "CORE_API_BASE_URL": settings.core_base_url,
                            "CORE_SERVICE_TOKEN": "${MARBLE_JAR_API_KEY}",
                        },
                    },
                }
            },
            "agents_md_snippet": _agents_md_snippet(settings),
        }

    app.include_router(mcp)

    # ---------- Phoenix reconciliation ----------

    recon = APIRouter(prefix="/v1", tags=["phoenix"])

    @recon.post("/reconcile")
    async def start_reconcile(body: ReconcileBody) -> dict[str, Any]:
        if not app.state.settings.has_phoenix:
            raise HTTPException(status_code=501, detail="PHOENIX_BASE_URL is not configured")

        job_id = str(uuid.uuid4())
        _jobs[job_id] = {
            "job_id": job_id,
            "status": "running",
            "started_at": datetime.now(timezone.utc).isoformat(),
            "processed": 0,
            "updated": 0,
            "errors": [],
        }
        asyncio.create_task(_run_reconcile(job_id, body, app.state.core, app.state.phoenix))
        return _jobs[job_id]

    @recon.get("/reconcile/{job_id}")
    async def get_reconcile(job_id: str) -> dict[str, Any]:
        job = _jobs.get(job_id)
        if not job:
            raise HTTPException(status_code=404, detail="reconciliation job not found")
        return job

    app.include_router(recon)
    return app


async def _guard(awaitable: Any) -> Any:
    """Translate CoreAPIError into an HTTP error for FastAPI routes."""
    try:
        return await awaitable
    except CoreAPIError as err:
        raise HTTPException(status_code=err.status, detail=err.message) from err


async def _run_reconcile(
    job_id: str,
    body: ReconcileBody,
    core: CoreClient,
    phoenix: httpx.AsyncClient | None,
) -> None:
    """Backfill cost/token rollups from Phoenix for marbles missing metrics."""
    job = _jobs[job_id]
    try:
        if body.marble_ids:
            marbles = [{"id": mid, "trace_id": None} for mid in body.marble_ids]
        else:
            listing = await core.list_marbles(limit=200)
            marbles = [m for m in listing.get("items", []) if m.get("trace_id")]

        for marble in marbles:
            job["processed"] += 1
            trace_id = marble.get("trace_id")
            if not trace_id or phoenix is None:
                continue
            try:
                metrics = await _fetch_trace_metrics(phoenix, trace_id)
                if not metrics:
                    continue
                await core.write_rollup(
                    marble["id"],
                    {
                        "trace_id": trace_id,
                        "phoenix_project_name": body.phoenix_project,
                        "rollup_metrics": metrics,
                        **{
                            k: v
                            for k, v in {
                                "tokens_in": metrics.get("tokens_in"),
                                "tokens_out": metrics.get("tokens_out"),
                                "cost_usd": metrics.get("cost_usd"),
                                "duration_ms": metrics.get("duration_ms"),
                            }.items()
                            if v is not None
                        },
                    },
                )
                job["updated"] += 1
            except Exception as err:  # noqa: BLE001 - one bad trace must not kill the job
                job["errors"].append({"marble_id": marble.get("id"), "error": str(err)})

        job["status"] = "completed"
    except Exception as err:  # noqa: BLE001
        job["status"] = "failed"
        job["errors"].append({"error": str(err)})
    finally:
        job["finished_at"] = datetime.now(timezone.utc).isoformat()


async def _fetch_trace_metrics(phoenix: httpx.AsyncClient, trace_id: str) -> dict[str, Any] | None:
    """Query Phoenix for aggregate metrics on a trace.

    Phoenix's span query API differs between versions, so a failed lookup is
    treated as "no data yet" rather than an error.
    """
    try:
        resp = await phoenix.get(f"/v1/traces/{trace_id}")
        if resp.status_code != 200:
            return None
        data = resp.json()
    except Exception:  # noqa: BLE001
        return None

    spans = data.get("spans") or data.get("data") or []
    if not spans:
        return None

    tokens_in = sum(_num(s, "llm.token_count.prompt") for s in spans)
    tokens_out = sum(_num(s, "llm.token_count.completion") for s in spans)
    return {
        "tokens_in": int(tokens_in) or None,
        "tokens_out": int(tokens_out) or None,
        "span_count": len(spans),
    }


def _num(span: dict[str, Any], key: str) -> float:
    attrs = span.get("attributes") or {}
    try:
        return float(attrs.get(key) or 0)
    except (TypeError, ValueError):
        return 0.0


def _agents_md_snippet(settings: Settings) -> str:
    return f"""## Reporting completed work

This project uses **Marble Jar** to track agent work. When you finish a discrete
unit of work, call the `log_marble` MCP tool so the team can see it:

```
log_marble(
  summary="<one line describing what you accomplished>",
  model="<the model you used>",
  project="<repo or service slug>",
  cost_usd=<cost if known>,
  tokens_in=<prompt tokens>,
  tokens_out=<completion tokens>,
  duration_ms=<elapsed ms>,
)
```

Or via REST:

```bash
curl -X POST {settings.core_base_url}/v1/marbles \\
  -H "Authorization: Bearer $MARBLE_JAR_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{{"summary":"Fixed the flaky auth test","model":"claude-sonnet-4","project":"payments-api"}}'
```
"""
