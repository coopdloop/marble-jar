import type { Marble } from "./types";

/** Columns written to the CSV export, in order, as `header: marbleField`. */
const CSV_COLUMNS: [string, (m: Marble) => string | number | null | undefined][] = [
  ["id", (m) => m.id],
  ["occurred_at", (m) => m.occurred_at],
  ["summary", (m) => m.summary],
  ["status", (m) => m.status],
  ["project", (m) => m.project_name],
  ["agent", (m) => m.agent_name],
  ["model", (m) => m.model],
  ["source", (m) => m.source],
  ["tokens_in", (m) => m.tokens_in],
  ["tokens_out", (m) => m.tokens_out],
  ["cost_usd", (m) => m.cost_usd],
  ["duration_ms", (m) => m.duration_ms],
  ["objective_id", (m) => m.objective_id],
  ["trace_id", (m) => m.trace_id],
  ["phoenix_trace_url", (m) => m.phoenix_trace_url],
];

/** Quotes a cell only when it needs it, per RFC 4180. */
export function csvCell(value: string | number | null | undefined): string {
  if (value === null || value === undefined) return "";
  const s = String(value);
  return /[",\r\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
}

export function marblesToCsv(marbles: Marble[]): string {
  const header = CSV_COLUMNS.map(([name]) => csvCell(name)).join(",");
  const rows = marbles.map((m) => CSV_COLUMNS.map(([, get]) => csvCell(get(m))).join(","));
  return [header, ...rows].join("\r\n");
}

/** Timestamped filename, optionally tagged with the active filter label. */
export function csvFilename(label?: string): string {
  const now = new Date();
  const pad = (n: number) => String(n).padStart(2, "0");
  const stamp = `${now.getFullYear()}${pad(now.getMonth() + 1)}${pad(now.getDate())}-${pad(
    now.getHours(),
  )}${pad(now.getMinutes())}`;
  const slug = (label ?? "").toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "");
  return `marble-jar${slug ? `-${slug}` : ""}-${stamp}.csv`;
}

/** Triggers a browser download. The BOM keeps Excel from mangling UTF-8. */
export function downloadTextFile(filename: string, text: string, mime = "text/csv"): void {
  const blob = new Blob([mime === "text/csv" ? `\uFEFF${text}` : text], {
    type: `${mime};charset=utf-8`,
  });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
}
