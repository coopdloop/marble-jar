import { describe, expect, it } from "vitest";
import { marbleIngestCurl, marbleIngestPayload } from "@/lib/curl";
import type { Marble } from "@/lib/types";

function marble(overrides: Partial<Marble> = {}): Marble {
  return {
    id: "m1",
    organization_id: "org1",
    project_id: "p1",
    objective_id: null,
    agent_id: null,
    created_by_user_id: null,
    summary: "Refactored the payment module",
    model: "claude-sonnet-4",
    tokens_in: 1200,
    tokens_out: 300,
    cost_usd: 0.0421,
    duration_ms: 1500,
    trace_id: null,
    phoenix_trace_url: null,
    status: "dispatched",
    metadata: {},
    source: "sdk",
    occurred_at: "2026-09-15T10:00:00.000Z",
    created_at: "2026-09-15T10:00:00.000Z",
    updated_at: "2026-09-15T10:00:00.000Z",
    project_name: "payments-api",
    agent_name: "claude-code",
    ...overrides,
  };
}

describe("marbleIngestPayload", () => {
  it("uses the field names POST /v1/marbles accepts", () => {
    expect(marbleIngestPayload(marble())).toMatchObject({
      summary: "Refactored the payment module",
      project: "payments-api",
      agent: "claude-code",
      model: "claude-sonnet-4",
      tokens_input: 1200,
      tokens_output: 300,
      cost_usd: 0.0421,
      duration_ms: 1500,
    });
  });

  it("drops metrics that were never reported instead of sending nulls", () => {
    const payload = marbleIngestPayload(
      marble({
        project_name: null,
        agent_name: null,
        model: null,
        tokens_in: null,
        tokens_out: null,
        cost_usd: null,
        duration_ms: null,
      }),
    );
    expect(Object.keys(payload).sort()).toEqual(["source", "status", "summary"]);
  });

  it("keeps metadata only when it actually has keys", () => {
    expect(marbleIngestPayload(marble())).not.toHaveProperty("metadata");
    expect(marbleIngestPayload(marble({ metadata: { pr: 42 } }))).toHaveProperty("metadata", {
      pr: 42,
    });
  });
});

describe("marbleIngestCurl", () => {
  const lines = marbleIngestCurl(marble(), "http://localhost:8080/").split("\n");
  const backslash = String.fromCharCode(92);

  it("posts to the ingest endpoint with the trailing slash trimmed", () => {
    expect(lines[0]!.startsWith("curl -X POST 'http://localhost:8080/v1/marbles'")).toBe(true);
  });

  it("keeps the credential a shell variable, never a literal token", () => {
    expect(lines[1]!.trim().startsWith("-H 'Authorization: Bearer $MARBLE_JAR_API_KEY'")).toBe(true);
  });

  it("continues every line except the last", () => {
    for (const [i, line] of lines.entries()) {
      expect(line.endsWith(backslash)).toBe(i < lines.length - 1);
    }
    expect(lines.at(-1)!.startsWith("  -d '")).toBe(true);
  });

  it("shell-quotes a JSON body containing apostrophes", () => {
    const curl = marbleIngestCurl(marble({ summary: "Fix Bob's card" }), "http://api.test");
    const quote = String.fromCharCode(39);
    // A single quote inside single quotes closes, escapes, reopens: '\''
    expect(curl).toContain(`Fix Bob${quote}${backslash}${quote}${quote}s card`);
  });

  it("sends the whole payload as one JSON argument", () => {
    const curl = marbleIngestCurl(marble({ metadata: { pr: 7 } }), "http://api.test");
    const body = curl.slice(curl.indexOf("-d '") + 4, curl.lastIndexOf("'"));
    expect(JSON.parse(body)).toMatchObject({ summary: "Refactored the payment module", metadata: { pr: 7 } });
  });

  it("falls back to the configured API base when none is passed", () => {
    expect(marbleIngestCurl(marble())).toContain("/v1/marbles");
  });
});
