"""Entrypoint: run an MCP server over stdio, or the HTTP bridge.

    marble-jar-mcp --server log       # stdio MCP for agent harnesses
    marble-jar-mcp --server monitor   # stdio MCP for dashboards/agents
    marble-jar-mcp --server http      # FastAPI bridge (default)
"""

from __future__ import annotations

import argparse
import asyncio
import logging
import sys

from .config import load_settings
from .core_client import CoreClient
from .server import build_log_marble_server, build_monitor_server


def main() -> int:
    parser = argparse.ArgumentParser(prog="marble-jar-mcp")
    parser.add_argument(
        "--server",
        choices=["log", "monitor", "http"],
        default="http",
        help="Which server to run (default: http).",
    )
    parser.add_argument("--port", type=int, default=None, help="Override the HTTP port.")
    args = parser.parse_args()

    settings = load_settings()
    logging.basicConfig(
        level=getattr(logging, settings.log_level.upper(), logging.INFO),
        format="%(asctime)s %(levelname)s %(name)s %(message)s",
        # stdio MCP owns stdout, so logs must go to stderr.
        stream=sys.stderr,
    )

    if args.server == "http":
        import uvicorn

        from .http_app import create_app

        uvicorn.run(
            create_app(settings),
            host="0.0.0.0",
            port=args.port or settings.mcp_port,
            log_level=settings.log_level,
        )
        return 0

    asyncio.run(_run_stdio(args.server, settings))
    return 0


async def _run_stdio(which: str, settings) -> None:
    from mcp.server.stdio import stdio_server

    core = CoreClient(settings)
    server = (
        build_log_marble_server(core, settings)
        if which == "log"
        else build_monitor_server(core)
    )

    try:
        async with stdio_server() as (read_stream, write_stream):
            await server.run(read_stream, write_stream, server.create_initialization_options())
    finally:
        await core.aclose()


if __name__ == "__main__":
    raise SystemExit(main())
