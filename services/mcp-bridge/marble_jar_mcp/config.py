"""Environment-driven configuration for the MCP bridge."""

from __future__ import annotations

import os
from dataclasses import dataclass


@dataclass(frozen=True)
class Settings:
    core_base_url: str
    core_service_token: str
    phoenix_base_url: str
    phoenix_api_key: str | None
    mcp_port: int
    log_level: str

    @property
    def has_phoenix(self) -> bool:
        return bool(self.phoenix_base_url)

    def phoenix_trace_url(self, trace_id: str) -> str | None:
        """Deep link to a trace in the Phoenix UI."""
        if not self.phoenix_base_url or not trace_id:
            return None
        return f"{self.phoenix_base_url}/v1/traces/{trace_id}"


def load_settings() -> Settings:
    core = os.getenv("CORE_API_BASE_URL", "http://localhost:8080").rstrip("/")
    token = os.getenv("CORE_SERVICE_TOKEN", "").strip()
    if not token:
        raise RuntimeError(
            "CORE_SERVICE_TOKEN is required: create an API key in Marble Jar "
            "(Settings -> Tokens) and export it before starting the MCP bridge."
        )

    return Settings(
        core_base_url=core,
        core_service_token=token,
        phoenix_base_url=os.getenv("PHOENIX_BASE_URL", "").rstrip("/"),
        phoenix_api_key=os.getenv("PHOENIX_API_KEY") or None,
        mcp_port=int(os.getenv("MCP_SERVER_PORT", "8090")),
        log_level=os.getenv("LOG_LEVEL", "info"),
    )
