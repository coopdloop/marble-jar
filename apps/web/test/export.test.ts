import { describe, expect, it } from "vitest";
import { csvCell, csvFilename, marblesToCsv } from "@/lib/export";
import type { Marble } from "@/lib/types";

function marble(overrides: Partial<Marble> = {}): Marble {
  return {
    id: "m1",
    organization_id: "org1",
    project_id: null,
    objective_id: null,
    agent_id: null,
    created_by_user_id: null,
    summary: "plain summary",
    model: null,
    tokens_in: null,
    tokens_out: null,
    cost_usd: null,
    duration_ms: null,
    trace_id: null,
    phoenix_trace_url: null,
    status: "logged",
    metadata: {},
    source: "sdk",
    occurred_at: "2026-09-15T10:00:00.000Z",
    created_at: "2026-09-15T10:00:00.000Z",
    updated_at: "2026-09-15T10:00:00.000Z",
    ...overrides,
  };
}

/** Minimal RFC-4180 field splitter: enough to prove a row survives a parse. */
function parseRow(line: string): string[] {
  const cells: string[] = [];
  let cell = "";
  let quoted = false;
  for (let i = 0; i < line.length; i++) {
    const ch = line[i];
    if (quoted) {
      if (ch === '"' && line[i + 1] === '"') {
        cell += '"';
        i++;
      } else if (ch === '"') {
        quoted = false;
      } else {
        cell += ch;
      }
    } else if (ch === '"') {
      quoted = true;
    } else if (ch === ",") {
      cells.push(cell);
      cell = "";
    } else {
      cell += ch;
    }
  }
  cells.push(cell);
  return cells;
}

describe("csvCell", () => {
  it("leaves plain text unquoted", () => {
    expect(csvCell("payments-api")).toBe("payments-api");
    expect(csvCell(12.5)).toBe("12.5");
  });

  it("quotes cells containing a separator, quote or newline", () => {
    expect(csvCell("a,b")).toBe('"a,b"');
    expect(csvCell('say "hi"')).toBe('"say ""hi"""');
    expect(csvCell("two\nlines")).toBe('"two\nlines"');
  });

  it("renders missing values as an empty cell", () => {
    expect(csvCell(null)).toBe("");
    expect(csvCell(undefined)).toBe("");
  });
});

describe("marblesToCsv", () => {
  it("writes a header even when there are no marbles", () => {
    const [header, ...rest] = marblesToCsv([]).split("\r\n");
    expect(header).toBe(
      "id,occurred_at,summary,status,project,agent,model,source," +
        "tokens_in,tokens_out,cost_usd,duration_ms,objective_id,trace_id,phoenix_trace_url",
    );
    expect(rest).toEqual([]);
  });

  it("keeps one row per marble, joined with CRLF", () => {
    const lines = marblesToCsv([marble(), marble({ id: "m2" })]).split("\r\n");
    expect(lines).toHaveLength(3);
    expect(lines[1]?.startsWith("m1,")).toBe(true);
    expect(lines[2]?.startsWith("m2,")).toBe(true);
  });

  it("protects summaries from breaking the columns", () => {
    const [header, row] = marblesToCsv([
      marble({ summary: 'Refactored "billing", twice', cost_usd: 1.25, tokens_in: 10 }),
    ]).split("\r\n");
    const cells = parseRow(row!);
    expect(cells).toHaveLength(parseRow(header!).length);
    expect(cells[2]).toBe('Refactored "billing", twice');
    expect(cells[10]).toBe("1.25");
  });

  it("emits the same number of fields for every row", () => {
    const [header, row] = marblesToCsv([
      marble({ project_name: "Payments API", agent_name: "claude-code", model: "gpt-5" }),
    ]).split("\r\n");
    expect(parseRow(row!)).toHaveLength(parseRow(header!).length);
  });
});

describe("csvFilename", () => {
  it("stamps the export with a sortable timestamp", () => {
    expect(csvFilename()).toMatch(/^marble-jar-\d{8}-\d{4}\.csv$/);
  });

  it("slugifies the active filter into the name", () => {
    expect(csvFilename("Payments API")).toMatch(
      /^marble-jar-payments-api-\d{8}-\d{4}\.csv$/,
    );
  });
});
