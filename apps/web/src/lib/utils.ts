import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";
import { formatDistanceToNowStrict } from "date-fns";
import type { Marble } from "./types";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

export function formatCost(v: number | null | undefined): string {
  if (v === null || v === undefined) return "—";
  if (v === 0) return "$0";
  if (v < 0.01) return `$${v.toFixed(4)}`;
  return `$${v.toFixed(2)}`;
}

export function formatTokens(v: number | null | undefined): string {
  if (v === null || v === undefined) return "—";
  if (v >= 1_000_000) return `${(v / 1_000_000).toFixed(1)}M`;
  if (v >= 1_000) return `${(v / 1_000).toFixed(1)}k`;
  return String(v);
}

export function formatDuration(ms: number | null | undefined): string {
  if (ms === null || ms === undefined) return "—";
  if (ms >= 3_600_000) return `${(ms / 3_600_000).toFixed(1)}h`;
  if (ms >= 60_000) return `${(ms / 60_000).toFixed(1)}m`;
  if (ms >= 1_000) return `${(ms / 1_000).toFixed(1)}s`;
  return `${ms}ms`;
}

export function formatRelative(iso: string): string {
  try {
    return formatDistanceToNowStrict(new Date(iso), { addSuffix: true });
  } catch {
    return iso;
  }
}

export function totalTokens(m: Marble): number {
  return (m.tokens_in ?? 0) + (m.tokens_out ?? 0);
}

/**
 * Clipboard write with a fallback: the async API needs a secure context, so
 * plain-http dev tunnels and older browsers still get a working copy button.
 */
export async function copyToClipboard(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    // Fall through to the legacy path.
  }
  try {
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.setAttribute("readonly", "");
    ta.style.position = "fixed";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    ta.select();
    const ok = document.execCommand("copy");
    ta.remove();
    return ok;
  } catch {
    return false;
  }
}

/**
 * Marble colors are semantic: hue is derived from the model (or project) so the
 * same agent always gets the same color on the queue.
 */
const PALETTE = [
  "#38bdf8", // sky
  "#a78bfa", // violet
  "#34d399", // emerald
  "#fbbf24", // amber
  "#f472b6", // pink
  "#60a5fa", // blue
  "#fb923c", // orange
  "#4ade80", // green
  "#e879f9", // fuchsia
  "#2dd4bf", // teal
];

export function colorForKey(key: string | null | undefined): string {
  if (!key) return "#94a3b8";
  let hash = 0;
  for (let i = 0; i < key.length; i++) {
    hash = (hash * 31 + key.charCodeAt(i)) | 0;
  }
  return PALETTE[Math.abs(hash) % PALETTE.length]!;
}

export function marbleColor(m: Marble): string {
  return colorForKey(m.model ?? m.project_name ?? m.source);
}

/** Marble radius scales with relative cost, clamped to stay visually sane. */
export function marbleRadius(m: Marble, maxCost: number): number {
  const cost = m.cost_usd ?? 0;
  if (maxCost <= 0) return 11;
  const ratio = Math.min(cost / maxCost, 1);
  return 8 + Math.sqrt(ratio) * 12;
}

/** High-token-cost outliers get a subtle glow. */
export function isOutlier(m: Marble, medianCost: number): boolean {
  const cost = m.cost_usd ?? 0;
  return medianCost > 0 && cost > medianCost * 3;
}

export function median(values: number[]): number {
  if (values.length === 0) return 0;
  const sorted = [...values].sort((a, b) => a - b);
  const mid = Math.floor(sorted.length / 2);
  return sorted.length % 2 === 0
    ? ((sorted[mid - 1] ?? 0) + (sorted[mid] ?? 0)) / 2
    : (sorted[mid] ?? 0);
}

const RANGE_MS: Record<string, number> = {
  "1h": 3_600_000,
  "24h": 86_400_000,
  "7d": 7 * 86_400_000,
  "30d": 30 * 86_400_000,
};

/**
 * Converts a range preset into an absolute ISO timestamp.
 *
 * The result is quantized to the minute so repeated renders produce an
 * identical value — otherwise it would be a new React Query key every render
 * and the list would refetch forever.
 */
export function rangeToFrom(range: string): string | undefined {
  const span = RANGE_MS[range];
  if (!span) return undefined;
  const quantized = Math.floor((Date.now() - span) / 60_000) * 60_000;
  return new Date(quantized).toISOString();
}

export function statusTone(status: string): string {
  switch (status) {
    case "dispatched":
    case "succeeded":
      return "text-[hsl(var(--success))] bg-[hsl(var(--success))]/10 border-[hsl(var(--success))]/25";
    case "failed":
    case "dead_lettered":
      return "text-destructive bg-destructive/10 border-destructive/25";
    case "pending":
    case "running":
    case "retrying":
      return "text-[hsl(var(--warning))] bg-[hsl(var(--warning))]/10 border-[hsl(var(--warning))]/25";
    default:
      return "text-muted-foreground bg-muted/60 border-border";
  }
}
