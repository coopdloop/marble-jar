import { API_BASE_URL } from "./api";
import type { Marble } from "./types";

/** Wraps a value in single quotes so the shell passes it through verbatim. */
function shellQuote(value: string): string {
  return `'${value.replace(/'/g, `'\\''`)}'`;
}

/**
 * The wire shape for one marble, with unset fields dropped so the snippet is
 * not full of nulls. Mirrors the aliases POST /v1/marbles accepts.
 */
export function marbleIngestPayload(m: Marble): Record<string, unknown> {
  const payload: Record<string, unknown> = { summary: m.summary };
  if (m.project_name) payload.project = m.project_name;
  if (m.agent_name) payload.agent = m.agent_name;
  if (m.model) payload.model = m.model;
  if (m.source) payload.source = m.source;
  if (m.status) payload.status = m.status;
  if (m.tokens_in !== null) payload.tokens_input = m.tokens_in;
  if (m.tokens_out !== null) payload.tokens_output = m.tokens_out;
  if (m.cost_usd !== null) payload.cost_usd = m.cost_usd;
  if (m.duration_ms !== null) payload.duration_ms = m.duration_ms;
  if (m.trace_id) payload.trace_id = m.trace_id;
  if (m.phoenix_trace_url) payload.phoenix_trace_url = m.phoenix_trace_url;
  if (m.objective_id) payload.objective_id = m.objective_id;
  if (m.metadata && Object.keys(m.metadata).length > 0) payload.metadata = m.metadata;
  return payload;
}

/**
 * A copy-pasteable curl that replays this marble through the ingestion API.
 *
 * The key stays a `$MARBLE_JAR_API_KEY` reference: a browser clipboard is easy
 * to leak, and the placeholder also keeps the snippet reusable outside the app.
 */
export function marbleIngestCurl(m: Marble, apiBase: string = API_BASE_URL): string {
  const body = JSON.stringify(marbleIngestPayload(m));
  return [
    `curl -X POST ${shellQuote(`${apiBase.replace(/\/+$/, "")}/v1/marbles`)}`,
    `  -H ${shellQuote("Authorization: Bearer $MARBLE_JAR_API_KEY")}`,
    `  -H ${shellQuote("Content-Type: application/json")}`,
    `  -d ${shellQuote(body)}`,
  ].join(" \\\n");
}
