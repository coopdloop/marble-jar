"""Async HTTP client for marble_jar_core, used by both MCP servers."""

from __future__ import annotations

import logging
from typing import Any

import httpx

from .config import Settings

log = logging.getLogger(__name__)


class CoreAPIError(RuntimeError):
    """Raised when marble_jar_core returns a non-2xx response."""

    def __init__(self, status: int, message: str) -> None:
        super().__init__(f"core api {status}: {message}")
        self.status = status
        self.message = message


class CoreClient:
    """Thin wrapper around the core REST API with retries for transient faults."""

    def __init__(self, settings: Settings, client: httpx.AsyncClient | None = None) -> None:
        self._settings = settings
        self._client = client or httpx.AsyncClient(
            base_url=settings.core_base_url,
            timeout=httpx.Timeout(30.0, connect=10.0),
            headers={
                "Authorization": f"Bearer {settings.core_service_token}",
                "Content-Type": "application/json",
            },
            transport=httpx.AsyncHTTPTransport(retries=2),
        )

    async def aclose(self) -> None:
        await self._client.aclose()

    async def __aenter__(self) -> "CoreClient":
        return self

    async def __aexit__(self, *exc: object) -> None:
        await self.aclose()

    async def _request(self, method: str, path: str, **kwargs: Any) -> Any:
        resp = await self._client.request(method, path, **kwargs)
        if resp.status_code >= 400:
            detail = resp.text
            try:
                detail = resp.json().get("error", detail)
            except Exception:  # noqa: BLE001 - body may not be JSON
                pass
            raise CoreAPIError(resp.status_code, str(detail))
        if resp.status_code == 204 or not resp.content:
            return None
        return resp.json()

    # ---------- ingestion ----------

    async def log_marble(self, payload: dict[str, Any], idempotency_key: str | None = None) -> dict[str, Any]:
        headers = {"Idempotency-Key": idempotency_key} if idempotency_key else None
        return await self._request("POST", "/v1/marbles", json=payload, headers=headers)

    async def write_rollup(self, marble_id: str, payload: dict[str, Any]) -> dict[str, Any]:
        return await self._request("POST", f"/v1/marbles/{marble_id}/rollups", json=payload)

    # ---------- monitor queries ----------

    async def jar_status(self) -> dict[str, Any]:
        return await self._request("GET", "/v1/jar/status")

    async def list_marbles(self, **params: Any) -> dict[str, Any]:
        clean = {k: v for k, v in params.items() if v is not None}
        return await self._request("GET", "/v1/marbles", params=clean)

    async def get_objective(self, objective_id: str) -> dict[str, Any]:
        return await self._request("GET", f"/v1/objectives/{objective_id}")

    async def list_objectives(self, **params: Any) -> dict[str, Any]:
        clean = {k: v for k, v in params.items() if v is not None}
        return await self._request("GET", "/v1/objectives", params=clean)

    async def trends(self, kind: str = "cost", **params: Any) -> dict[str, Any]:
        clean = {k: v for k, v in params.items() if v is not None}
        return await self._request("GET", f"/v1/trends/{kind}", params=clean)

    async def health(self) -> dict[str, Any]:
        return await self._request("GET", "/health")
