import { describe, expect, it } from "vitest";
import { downloadMime, downloadName, templates, unmappedTemplates } from "@/lib/templates";

const EXPECTED = [
  "AGENTS.md",
  "CLAUDE.md",
  "marble-jar.sh",
  "post-commit.sh",
  "marble-jar-ci.yml",
  "dispatch-rules.json",
  "weekly-report.sh",
];

describe("template library contents", () => {
  it("inlines every template file from /docs/templates", () => {
    expect(templates.map((t) => t.file).sort()).toEqual([...EXPECTED].sort());
  });

  it("has metadata for every file on disk", () => {
    expect(unmappedTemplates).toEqual([]);
  });

  it("describes each template well enough to install it", () => {
    for (const t of templates) {
      expect(t.label, t.file).toBeTruthy();
      expect(t.install, t.file).toBeTruthy();
      expect(t.description.length, t.file).toBeGreaterThan(40);
      expect(t.scopes.length, t.file).toBeGreaterThan(0);
      expect(t.tags.length, t.file).toBeGreaterThan(0);
    }
  });

  it("names only scopes the API actually checks", () => {
    const known = new Set([
      "marbles:read",
      "marbles:write",
      "rollups:write",
      "objectives:read",
      "objectives:write",
      "rules:read",
      "rules:write",
      "dispatch:read",
      "dispatch:write",
      "audit:read",
      "jar:read",
    ]);
    for (const t of templates) {
      for (const scope of t.scopes) {
        expect(known.has(scope), `${t.file}: unknown scope ${scope}`).toBe(true);
      }
    }
  });

  it("ships no credential that looks real", () => {
    for (const t of templates) {
      expect(t.contents, t.file).not.toMatch(/mj_[A-Za-z0-9_-]{16,}/);
      expect(t.contents, t.file).not.toMatch(/Bearer [A-Za-z0-9_-]{32,}/);
    }
  });
});

describe("reporting contract (AGENTS.md)", () => {
  const agents = templates.find((t) => t.file === "AGENTS.md")!.contents;

  it("covers the fields the ingest endpoint actually accepts", () => {
    for (const field of [
      "tokens_input",
      "tokens_output",
      "cost_usd",
      "duration_ms",
      "occurred_at",
      "idempotency",
      "metadata",
    ]) {
      expect(agents.toLowerCase(), field).toContain(field.toLowerCase());
    }
  });

  it("states the status vocabulary the API validates", () => {
    const row = agents.split("\n").find((line) => line.includes("`status`"))!;
    for (const status of ["logged", "partial", "failed", "dispatched"]) {
      expect(row, `${status} listed`).toContain(`\`${status}\``);
    }
  });

  it("tells the agent to report failures and to never dispatch unasked", () => {
    expect(agents).toContain("Failures are marbles too");
    expect(agents).toContain("Agents report; humans dispatch");
  });

  it("keeps every documented call against the live API surface", () => {
    const paths = [...agents.matchAll(/POST "\$MARBLE_JAR_URL([^"]+)"/g)].map((m) => m[1]);
    expect(paths.length).toBeGreaterThanOrEqual(3);
    for (const path of paths) {
      expect(path.startsWith("/v1/"), path).toBe(true);
    }
  });
});

describe("downloadable files", () => {
  it("keeps filenames and mimes tied to the language", () => {
    const byFile = Object.fromEntries(templates.map((t) => [t.file, t]));
    expect(downloadName(byFile["post-commit.sh"]!)).toBe("post-commit.sh");
    expect(downloadMime(byFile["AGENTS.md"]!)).toBe("text/markdown");
    expect(downloadMime(byFile["marble-jar-ci.yml"]!)).toBe("text/yaml");
    expect(downloadMime(byFile["dispatch-rules.json"]!)).toBe("application/json");
  });

  it("ships dispatch presets that parse and carry the required rule fields", () => {
    const rules = JSON.parse(templates.find((t) => t.file === "dispatch-rules.json")!.contents);
    expect(Array.isArray(rules.rules)).toBe(true);
    expect(rules.rules.length).toBeGreaterThanOrEqual(3);
    for (const rule of rules.rules) {
      expect(rule.name).toBeTruthy();
      expect(rule.condition).toBeTruthy();
      expect(["jira", "slack", "teams", "webhook"]).toContain(rule.target_type);
      expect(rule.target_config).toBeTruthy();
    }
  });

  it("gives the shell helpers a function per documented action", () => {
    const lib = templates.find((t) => t.file === "marble-jar.sh")!.contents;
    for (const fn of [
      "mj_log",
      "mj_log_failed",
      "mj_rollup",
      "mj_objective_add",
      "mj_dispatch",
      "mj_search",
      "mj_status",
      "mj_streak",
      "mj_export",
    ]) {
      expect(lib, fn).toContain(`${fn}()`);
    }
    // Every helper must go through the same auth header, not improvise per call.
    expect(lib).toContain('Authorization: Bearer $MARBLE_JAR_API_KEY');
  });
});
