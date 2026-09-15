import { useDeferredValue, useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { ChevronRight, ScrollText, X } from "lucide-react";
import { marbleJarApi } from "@/lib/api";
import type { AuditEntry } from "@/lib/types";
import { cn, formatRelative } from "@/lib/utils";
import {
  Badge,
  Button,
  Card,
  CardHeader,
  CardTitle,
  Input,
  Label,
  Select,
  Skeleton,
} from "@/components/ui/primitives";

const PROVIDERS = ["jira", "slack", "teams", "webhook"];

// The server caps a page at 200 rows; 50 matches the rest of the dashboard.
const PAGE_SIZE = 50;

/** Cursor pages for one filter set, keyed by that filter. */
type PageState = {
  key: string;
  cursors: string[];
  byCursor: Record<string, AuditEntry[]>;
};

const emptyPage = (key: string): PageState => ({ key, cursors: [""], byCursor: {} });

/**
 * Audit details carry the target_config a dispatch used, which for Slack and
 * Teams can be an incoming-webhook URL and for custom webhooks a per-target HMAC
 * secret — both are live credentials. The dispatch service now redacts at write
 * time; this masks again on the way out, which is what covers rows written
 * before that change landed.
 */
const SENSITIVE = /token|secret|password|api[-_]?key|authorization|webhook/i;

function maskValue(value: unknown): string {
  if (typeof value === "string") {
    try {
      const url = new URL(value);
      // Host stays so the target is still identifiable while debugging; the
      // path is where the credential lives.
      if (url.protocol === "http:" || url.protocol === "https:") {
        return `${url.protocol}//${url.host}/[redacted]`;
      }
    } catch {
      /* not a URL — fall through to the blunt mask */
    }
  }
  return "[redacted]";
}

function redact(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(redact);
  if (value && typeof value === "object") {
    const out: Record<string, unknown> = {};
    for (const [key, inner] of Object.entries(value as Record<string, unknown>)) {
      out[key] = SENSITIVE.test(key) ? maskValue(inner) : redact(inner);
    }
    return out;
  }
  return value;
}

export function AuditLogPage() {
  const [provider, setProvider] = useState("");
  const [action, setAction] = useState("");
  const [marbleId, setMarbleId] = useState("");
  const [dispatchId, setDispatchId] = useState("");

  // Exact-match ids are pasted a character at a time, so the query reads the
  // deferred copies and does not fire dozens of lookups per UUID typed.
  const queriedMarbleId = useDeferredValue(marbleId);
  const queriedDispatchId = useDeferredValue(dispatchId);

  const [expandedId, setExpandedId] = useState<string | null>(null);

  const filterKey = `${provider}|${action}|${queriedMarbleId}|${queriedDispatchId}`;

  // Pages are keyed by the filter that produced them, so changing a filter can
  // never issue (new filter, old cursor) — a mismatch is an empty first page.
  const [stored, setStored] = useState<PageState>(emptyPage(filterKey));
  const page = stored.key === filterKey ? stored : emptyPage(filterKey);
  const cursor = page.cursors[page.cursors.length - 1] ?? "";

  const { data, isFetching, isError, error } = useQuery({
    // Prefixed "audit" so dispatch events on the WebSocket feed invalidate this
    // list too — useQueueFeed and pages/Integrations.tsx both use that prefix.
    queryKey: ["audit", "log", provider, action, queriedMarbleId, queriedDispatchId, cursor],
    queryFn: () =>
      marbleJarApi.listAudit({
        provider,
        action,
        marble_id: queriedMarbleId,
        dispatch_id: queriedDispatchId,
        limit: PAGE_SIZE,
        cursor,
      }),
  });

  useEffect(() => {
    if (!data) return;
    setStored((prev) =>
      prev.key === filterKey
        ? { ...prev, byCursor: { ...prev.byCursor, [cursor]: data.items } }
        : prev,
    );
  }, [data, filterKey, cursor]);

  const rows = useMemo(() => {
    // Cursor overlap is harmless when merged by id, and a refetch refreshes rows
    // already on screen.
    const byId = new Map<string, AuditEntry>();
    for (const c of page.cursors) {
      for (const entry of page.byCursor[c] ?? []) byId.set(entry.id, entry);
    }
    // ListAudit orders by created_at DESC, id DESC — tiebreak the same way or
    // same-timestamp rows reshuffle as pages arrive.
    return [...byId.values()].sort(
      (a, b) =>
        new Date(b.created_at).getTime() - new Date(a.created_at).getTime() ||
        (a.id < b.id ? 1 : a.id > b.id ? -1 : 0),
    );
  }, [page]);

  // A later page fetches without emptying the table, so the skeleton only stands
  // in when there is genuinely nothing on screen yet.
  const loading = isFetching && rows.length === 0;
  const nextCursor = data?.next_cursor ?? "";

  const loadMore = () =>
    setStored((prev) => {
      const base = prev.key === filterKey ? prev : emptyPage(filterKey);
      if (base.cursors.includes(nextCursor)) return base;
      return { ...base, cursors: [...base.cursors, nextCursor] };
    });

  const actions = useMemo(
    () => Array.from(new Set(rows.map((r) => r.action))).sort(),
    [rows],
  );

  // Sign-in events are audited too, so the provider vocabulary is wider than the
  // four integrations — offer whatever the loaded rows actually contain.
  const providerOptions = useMemo(() => {
    const extra = Array.from(new Set(rows.map((r) => r.provider)))
      .filter((p) => !PROVIDERS.includes(p))
      .sort();
    return [...PROVIDERS, ...extra];
  }, [rows]);

  const hasFilters = Boolean(provider || action || marbleId || dispatchId);

  return (
    <div className="space-y-5">
      <div>
        <h1 className="text-lg font-semibold tracking-tight">Audit log</h1>
        <p className="text-xs text-muted-foreground">
          Every dispatch is attributed to a real person — this page is the evidence.
        </p>
      </div>

      <div className="flex flex-wrap items-end gap-3">
        <div className="space-y-1">
          <Label htmlFor="audit-provider">Provider</Label>
          <Select id="audit-provider" value={provider} onChange={(e) => setProvider(e.target.value)}>
            <option value="">All providers</option>
            {providerOptions.map((p) => (
              <option key={p} value={p}>
                {p}
              </option>
            ))}
          </Select>
        </div>

        <div className="space-y-1">
          <Label htmlFor="audit-action">Action</Label>
          <Select id="audit-action" value={action} onChange={(e) => setAction(e.target.value)}>
            <option value="">All actions</option>
            {actions.map((a) => (
              <option key={a} value={a}>
                {a}
              </option>
            ))}
          </Select>
        </div>

        <div className="space-y-1">
          <Label htmlFor="audit-marble">Marble id</Label>
          <Input
            id="audit-marble"
            value={marbleId}
            onChange={(e) => setMarbleId(e.target.value.trim())}
            placeholder="exact id"
            className="w-44 font-mono text-xs"
          />
        </div>

        <div className="space-y-1">
          <Label htmlFor="audit-dispatch">Dispatch id</Label>
          <Input
            id="audit-dispatch"
            value={dispatchId}
            onChange={(e) => setDispatchId(e.target.value.trim())}
            placeholder="exact id"
            className="w-44 font-mono text-xs"
          />
        </div>

        {hasFilters ? (
          <Button
            variant="ghost"
            size="sm"
            onClick={() => {
              setProvider("");
              setAction("");
              setMarbleId("");
              setDispatchId("");
            }}
          >
            <X className="h-3.5 w-3.5" />
            Clear
          </Button>
        ) : null}

        <p className="ml-auto hidden max-w-xs text-right text-[11px] text-muted-foreground xl:block">
          Marble and dispatch ids match exactly — paste them from a marble or dispatch URL.
        </p>
      </div>

      {isError ? <p className="text-xs text-destructive">{(error as Error).message}</p> : null}

      <Card className="overflow-hidden">
        <CardHeader className="flex-row items-center justify-between">
          <CardTitle className="flex items-center gap-2">
            <ScrollText className="h-3.5 w-3.5" />
            Activity
          </CardTitle>
          <span className="text-[11px] text-muted-foreground">
            {rows.length} shown · click a row for the payload
          </span>
        </CardHeader>

        {loading ? (
          <div className="space-y-2 p-5 pt-0">
            {Array.from({ length: 6 }).map((_, i) => (
              <Skeleton key={i} className="h-9 w-full" />
            ))}
          </div>
        ) : rows.length === 0 ? (
          <div className="px-5 pb-5 text-xs text-muted-foreground">
            Nothing has been dispatched on behalf of anyone yet.
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-left text-xs">
              <thead>
                <tr className="border-b border-border/60 text-[11px] uppercase tracking-wider text-muted-foreground">
                  <th className="px-5 py-2">When</th>
                  <th className="px-5 py-2">Action</th>
                  <th className="px-5 py-2">Provider</th>
                  <th className="px-5 py-2">Agent</th>
                  <th className="px-5 py-2">Acting user</th>
                  <th className="px-5 py-2">Marble</th>
                  <th className="w-8 px-3 py-2" aria-hidden />
                </tr>
              </thead>
              <tbody>
                {rows.map((entry) => (
                  <AuditRow
                    key={entry.id}
                    entry={entry}
                    open={expandedId === entry.id}
                    onToggle={() => setExpandedId(expandedId === entry.id ? null : entry.id)}
                  />
                ))}
              </tbody>
            </table>
          </div>
        )}

        {nextCursor ? (
          <div className="flex items-center justify-between gap-3 border-t border-border/60 px-5 py-3">
            <p className="text-[11px] text-muted-foreground">
              {hasFilters ? "Filters are applied server-side on every page." : "Newest entries first."}
            </p>
            <Button variant="outline" size="sm" disabled={isFetching} onClick={loadMore}>
              {isFetching ? "Loading…" : "Load more"}
            </Button>
          </div>
        ) : null}
      </Card>
    </div>
  );
}

function AuditRow({
  entry,
  open,
  onToggle,
}: {
  entry: AuditEntry;
  open: boolean;
  onToggle: () => void;
}) {
  const detailId = `audit-details-${entry.id}`;

  return (
    <>
      <tr
        onClick={onToggle}
        className={cn(
          "cursor-pointer border-b border-border/40 transition-colors last:border-0 hover:bg-secondary/50",
          open && "bg-secondary/40",
        )}
      >
        <td className="whitespace-nowrap px-5 py-2">
          <div className="numeric">{formatAbsolute(entry.created_at)}</div>
          <div className="text-[11px] text-muted-foreground">
            {formatRelative(entry.created_at)}
          </div>
        </td>
        <td className="px-5 py-2 font-medium">{entry.action}</td>
        <td className="px-5 py-2">
          <Badge className="capitalize">{entry.provider}</Badge>
        </td>
        <td className="px-5 py-2 text-muted-foreground">{entry.agent_identity ?? "—"}</td>
        <td className="px-5 py-2 font-mono text-[10px] text-muted-foreground">
          {entry.acting_user_id?.slice(0, 8) ?? "—"}
        </td>
        <td className="px-5 py-2">
          {entry.marble_id ? (
            <Link
              to={`/marbles/${entry.marble_id}`}
              onClick={(e) => e.stopPropagation()}
              className="font-mono text-[10px] text-primary hover:underline"
            >
              {entry.marble_id.slice(0, 8)}
            </Link>
          ) : (
            <span className="text-muted-foreground">—</span>
          )}
        </td>
        <td className="px-3 py-2 text-right">
          <button
            type="button"
            aria-expanded={open}
            aria-controls={detailId}
            aria-label={open ? "Hide payload" : "Show payload"}
            onClick={(e) => {
              e.stopPropagation();
              onToggle();
            }}
            className="rounded-md p-1 text-muted-foreground transition-colors hover:bg-secondary hover:text-foreground"
          >
            <ChevronRight className={cn("h-3.5 w-3.5 transition-transform", open && "rotate-90")} />
          </button>
        </td>
      </tr>
      {open ? (
        <tr
          id={detailId}
          className="border-b border-border/40 last:border-0"
        >
          <td colSpan={7} className="px-5 pb-4">
            <div className="space-y-2">
              <DetailIds entry={entry} />
              <pre className="max-h-64 overflow-x-auto overflow-y-auto rounded-lg border border-border/60 bg-background/60 p-3 text-[11px] leading-relaxed">
                {JSON.stringify(redact(entry.details ?? {}), null, 2)}
              </pre>
            </div>
          </td>
        </tr>
      ) : null}
    </>
  );
}

function DetailIds({ entry }: { entry: AuditEntry }) {
  const ids: [string, string | null][] = [
    ["entry", entry.id],
    ["dispatch", entry.dispatch_id],
    ["marble", entry.marble_id],
    ["acting user", entry.acting_user_id],
  ];
  return (
    <div className="flex flex-wrap gap-x-5 gap-y-1 text-[11px] text-muted-foreground">
      {ids.map(([label, value]) => (
        <span key={label}>
          {label} <span className="font-mono text-[10px] text-foreground/80">{value ?? "—"}</span>
        </span>
      ))}
    </div>
  );
}

function formatAbsolute(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString();
}
