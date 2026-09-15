import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useSearchParams } from "react-router-dom";
import { CheckCircle2, RotateCcw, ShieldAlert, X } from "lucide-react";
import { marbleJarApi } from "@/lib/api";
import type { Dispatch } from "@/lib/types";
import { cn, formatRelative, statusTone } from "@/lib/utils";
import {
  Badge,
  Button,
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  Select,
  Skeleton,
  Stat,
} from "@/components/ui/primitives";

/**
 * View presets. `status` is a comma-separated list, and the server rejects any
 * value outside the dispatch state machine. "Needs attention" holds exactly the
 * statuses the API will let you replay: ReplayDispatch refuses running, retrying
 * and succeeded rows so replay can never double-execute in-flight work.
 */
const VIEWS = {
  attention: { label: "Needs attention", status: "failed,dead_lettered" },
  failed: { label: "Failed", status: "failed" },
  dead_lettered: { label: "Dead-lettered", status: "dead_lettered" },
  all: { label: "All", status: "" },
} as const;

type ViewKey = keyof typeof VIEWS;

/** The only statuses POST /v1/dispatches/:id/replay accepts. */
const REPLAYABLE = new Set<Dispatch["status"]>(["failed", "dead_lettered"]);

const PAGE_SIZE = 50;
const INTEGRATIONS = ["jira", "slack", "teams", "webhook"] as const;
/** Same utility set Tailwind's truncation helper compiles to. */
const ELLIPSIS = "block overflow-hidden text-ellipsis whitespace-nowrap";

/** Cursor pages for one filter set: which cursors were asked for, and their rows. */
type PageState = {
  key: string;
  cursors: string[];
  byCursor: Record<string, Dispatch[]>;
};

const emptyPage = (key: string): PageState => ({ key, cursors: [""], byCursor: {} });

export function DispatchesPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const queryClient = useQueryClient();

  const requested = searchParams.get("view") as ViewKey | null;
  const activeView: ViewKey = requested && requested in VIEWS ? requested : "attention";
  const integration = searchParams.get("integration") ?? "";

  const setParam = (key: string, value: string) => {
    const next = new URLSearchParams(searchParams);
    if (value) next.set(key, value);
    else next.delete(key);
    setSearchParams(next, { replace: true });
  };

  const status = VIEWS[activeView].status;
  const filterKey = `${status}|${integration}`;

  // Pagination is additive — asked-for pages accumulate instead of replacing the
  // table — and it is keyed by filter, so a page can never be read against the
  // wrong filter: a mismatch is simply an empty first page during render.
  const [stored, setStored] = useState<PageState>(emptyPage(filterKey));
  const page = stored.key === filterKey ? stored : emptyPage(filterKey);
  const activeCursor = page.cursors[page.cursors.length - 1] ?? "";

  const { data, isFetching, isError, error } = useQuery({
    queryKey: ["dispatches", activeView, integration, activeCursor],
    queryFn: () =>
      marbleJarApi.listDispatches({
        status,
        integration_type: integration,
        limit: PAGE_SIZE,
        cursor: activeCursor || undefined,
      }),
  });

  useEffect(() => {
    if (!data) return;
    setStored((prev) =>
      prev.key === filterKey
        ? { ...prev, byCursor: { ...prev.byCursor, [activeCursor]: data.items } }
        : prev,
    );
  }, [data, filterKey, activeCursor]);

  const rows = page.cursors.flatMap((c) => page.byCursor[c] ?? []);
  const loading = isFetching && rows.length === 0;
  const nextCursor = data?.next_cursor ?? "";

  const counts = {
    failed: rows.filter((d) => d.status === "failed").length,
    dead_lettered: rows.filter((d) => d.status === "dead_lettered").length,
    in_flight: rows.filter((d) => d.status === "pending" || d.status === "retrying").length,
  };

  const [notice, setNotice] = useState<{ id: string; ok: boolean; text: string } | null>(null);
  useEffect(() => {
    if (!notice) return;
    const timer = window.setTimeout(() => setNotice(null), 6000);
    return () => window.clearTimeout(timer);
  }, [notice]);

  const replay = useMutation({
    mutationFn: (id: string) => marbleJarApi.replayDispatch(id),
    onSuccess: (d) => {
      // Replace the row in every page already on screen as well as invalidating:
      // a refetch only covers the mounted cursor, so older pages would keep
      // offering Replay on a row that is now pending and would then 409.
      setStored((prev) => {
        const byCursor: Record<string, Dispatch[]> = {};
        for (const [cursor, items] of Object.entries(prev.byCursor)) {
          byCursor[cursor] = items.map((row) => (row.id === d.id ? d : row));
        }
        return { ...prev, byCursor };
      });
      queryClient.invalidateQueries({ queryKey: ["dispatches"] });
      setNotice({ id: d.id, ok: true, text: "Requeued — the worker will pick it up." });
    },
    // The API answers 409 when a row is not replayable: still in flight, or done.
    onError: (err, id) => setNotice({ id, ok: false, text: (err as Error).message }),
  });

  const loadMore = () =>
    setStored((prev) => {
      const base = prev.key === filterKey ? prev : emptyPage(filterKey);
      if (base.cursors.includes(nextCursor)) return base;
      return { ...base, cursors: [...base.cursors, nextCursor] };
    });

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="text-lg font-semibold tracking-tight">Dispatches</h1>
          <p className="text-xs text-muted-foreground">
            Outbound actions, and everything stuck in front of them.
          </p>
        </div>
        <div className="flex flex-wrap gap-6">
          <Stat label="Failed" value={counts.failed} hint="in view" />
          <Stat label="Dead-lettered" value={counts.dead_lettered} hint="in view" />
          <Stat label="In flight" value={counts.in_flight} hint="in view" />
        </div>
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <div className="flex flex-wrap gap-1.5">
          {(Object.keys(VIEWS) as ViewKey[]).map((key) => (
            <Button
              key={key}
              size="sm"
              variant={activeView === key ? "default" : "outline"}
              onClick={() => setParam("view", key === "attention" ? "" : key)}
            >
              {VIEWS[key].label}
            </Button>
          ))}
        </div>

        <Select
          className="ml-auto"
          value={integration}
          onChange={(e) => setParam("integration", e.target.value)}
        >
          <option value="">All integrations</option>
          {INTEGRATIONS.map((provider) => (
            <option key={provider} value={provider}>
              {provider}
            </option>
          ))}
        </Select>

        {activeView !== "attention" || integration ? (
          <Button
            variant="ghost"
            size="sm"
            onClick={() => setSearchParams(new URLSearchParams(), { replace: true })}
          >
            <X className="h-3.5 w-3.5" />
            Clear
          </Button>
        ) : null}
      </div>

      {activeView === "attention" && !isError && !loading && rows.length === 0 ? (
        <Card className="border-[hsl(var(--success))]/25 bg-[hsl(var(--success))]/10">
          <CardContent className="flex items-center gap-2 p-4 text-xs text-[hsl(var(--success))]">
            <CheckCircle2 className="h-4 w-4" />
            Nothing is stuck — every dispatch in this window either landed or is still running.
          </CardContent>
        </Card>
      ) : null}

      {isError ? <p className="text-xs text-destructive">{(error as Error).message}</p> : null}

      <Card className="overflow-hidden">
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <ShieldAlert className="h-3.5 w-3.5" />
            {VIEWS[activeView].label}
          </CardTitle>
          <p className="text-xs text-muted-foreground">
            Replay re-queues a failed or dead-lettered dispatch with a fresh attempt budget. Rows
            that are running, retrying or already done are refused.
          </p>
        </CardHeader>

        {loading ? (
          <CardContent className="space-y-2">
            {Array.from({ length: 5 }).map((_, i) => (
              <Skeleton key={i} className="h-9" />
            ))}
          </CardContent>
        ) : rows.length === 0 ? (
          <CardContent>
            <p className="text-xs text-muted-foreground">No dispatches match this view yet.</p>
          </CardContent>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-left text-xs">
              <thead>
                <tr className="border-b border-border/60 text-[11px] uppercase tracking-wider text-muted-foreground">
                  <th className="px-5 py-2">Integration</th>
                  <th className="px-5 py-2">Action</th>
                  <th className="px-5 py-2">Status</th>
                  <th className="px-5 py-2">Marble</th>
                  <th className="px-5 py-2">Attempts</th>
                  <th className="px-5 py-2">Error</th>
                  <th className="px-5 py-2">Ref</th>
                  <th className="px-5 py-2">Updated</th>
                  <th className="px-5 py-2" />
                </tr>
              </thead>
              <tbody>
                {rows.map((row) => {
                  const replaying = replay.isPending && replay.variables === row.id;
                  const canReplay = REPLAYABLE.has(row.status);
                  return (
                    <tr key={row.id} className="border-b border-border/40 last:border-0">
                      <td className="px-5 py-2 font-medium capitalize">{row.integration_type}</td>
                      <td className="px-5 py-2 text-muted-foreground">{row.action}</td>
                      <td className="px-5 py-2">
                        <Badge tone={statusTone(row.status)}>{row.status}</Badge>
                      </td>
                      <td className="px-5 py-2">
                        {row.marble_id ? (
                          <Link
                            to={`/marbles/${row.marble_id}`}
                            className="font-mono text-[10px] text-primary hover:underline"
                          >
                            {row.marble_id.slice(0, 8)}
                          </Link>
                        ) : (
                          <span className="text-muted-foreground">—</span>
                        )}
                      </td>
                      <td className="numeric px-5 py-2 text-muted-foreground">
                        {row.attempt_count}
                      </td>
                      <td className="max-w-[240px] px-5 py-2">
                        {row.error_message ? (
                          <span className={cn(ELLIPSIS, "text-destructive")} title={row.error_message}>
                            {row.error_message}
                          </span>
                        ) : (
                          <span className="text-muted-foreground">—</span>
                        )}
                      </td>
                      <td className="max-w-[160px] px-5 py-2">
                        {row.external_ref ? (
                          <span className={cn(ELLIPSIS, "font-mono text-[10px]")} title={row.external_ref}>
                            {row.external_ref}
                          </span>
                        ) : (
                          <span className="text-muted-foreground">—</span>
                        )}
                      </td>
                      <td className="whitespace-nowrap px-5 py-2 text-muted-foreground">
                        {formatRelative(row.updated_at)}
                      </td>
                      <td className="px-5 py-2 text-right">
                        <Button
                          size="sm"
                          variant="outline"
                          disabled={!canReplay || replaying}
                          title={
                            canReplay
                              ? "Re-queue this dispatch"
                              : "Only failed or dead-lettered dispatches can be replayed"
                          }
                          onClick={() => replay.mutate(row.id)}
                        >
                          <RotateCcw className={cn("h-3.5 w-3.5", replaying && "animate-spin")} />
                          Replay
                        </Button>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}

        {notice ? (
          <CardContent>
            <p
              className={cn(
                "text-xs",
                notice.ok ? "text-[hsl(var(--success))]" : "text-destructive",
              )}
            >
              {notice.text}
            </p>
          </CardContent>
        ) : null}

        {nextCursor && !loading ? (
          <CardContent>
            <Button
              variant="ghost"
              size="sm"
              disabled={isFetching}
              onClick={loadMore}
            >
              Load more
            </Button>
          </CardContent>
        ) : null}
      </Card>
    </div>
  );
}
