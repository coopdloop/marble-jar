import { useQuery } from "@tanstack/react-query";
import { Search, X } from "lucide-react";
import { marbleJarApi } from "@/lib/api";
import { EmptyJarState, JarCanvas } from "@/components/jar/JarCanvas";
import { MarbleTable } from "@/components/marbles/MarbleTable";
import { MarbleSheet } from "@/components/marbles/MarbleSheet";
import { Button, Card, Input, Select, Stat } from "@/components/ui/primitives";
import { emptyFilters, useUiStore } from "@/stores/useAppStore";
import { formatCost, formatTokens, rangeToFrom } from "@/lib/utils";

export function LiveQueuePage() {
  const { filters, setFilter, resetFilters, activeMarbleId, openMarble, wsStatus } = useUiStore();

  const query = {
    project: filters.project,
    model: filters.model,
    agent_id: filters.agentId,
    status: filters.status,
    q: filters.q,
    from: rangeToFrom(filters.range),
    limit: 100,
  };

  const { data, isLoading } = useQuery({
    queryKey: ["marbles", query],
    queryFn: () => marbleJarApi.listMarbles(query),
    // The socket pushes updates; poll only when it is not connected.
    refetchInterval: wsStatus === "connected" ? false : 10_000,
  });

  const { data: status } = useQuery({
    queryKey: ["jar-status"],
    queryFn: () => marbleJarApi.jarStatus(),
    refetchInterval: 30_000,
  });

  const { data: projects } = useQuery({
    queryKey: ["projects"],
    queryFn: () => marbleJarApi.listProjects(),
    staleTime: 60_000,
  });

  const marbles = data?.items ?? [];
  const hasFilters = JSON.stringify(filters) !== JSON.stringify(emptyFilters);

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="text-lg font-semibold tracking-tight">Live Queue</h1>
          <p className="text-xs text-muted-foreground">
            Every finished task, a marble in the jar.
          </p>
        </div>
        {status ? (
          <div className="flex flex-wrap gap-6">
            <Stat label="Today" value={status.marbles_today} hint={`${status.marbles_last_hour} in the last hour`} />
            <Stat label="Spend today" value={formatCost(status.cost_today_usd)} />
            <Stat label="Tokens today" value={formatTokens(status.tokens_today)} />
            <Stat label="Open objectives" value={status.open_objectives} hint={`${status.pending_dispatches} dispatches pending`} />
          </div>
        ) : null}
      </div>

      {/* Hero: the jar gets room to breathe. */}
      <Card className="relative h-[380px] overflow-hidden">
        {isLoading ? null : marbles.length === 0 ? (
          <EmptyJarState />
        ) : (
          <JarCanvas marbles={marbles} onMarbleClick={openMarble} className="h-full w-full" />
        )}
      </Card>

      {/* Filters */}
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative min-w-[200px] flex-1">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={filters.q}
            onChange={(e) => setFilter("q", e.target.value)}
            placeholder="Search summaries…"
            className="pl-8"
          />
        </div>

        <Select value={filters.project} onChange={(e) => setFilter("project", e.target.value)}>
          <option value="">All projects</option>
          {projects?.items.map((p) => (
            <option key={p.id} value={p.slug}>
              {p.name}
            </option>
          ))}
        </Select>

        <Select value={filters.model} onChange={(e) => setFilter("model", e.target.value)}>
          <option value="">All models</option>
          {status?.top_models.map((m) => (
            <option key={m} value={m}>
              {m}
            </option>
          ))}
        </Select>

        <Select value={filters.status} onChange={(e) => setFilter("status", e.target.value)}>
          <option value="">Any status</option>
          <option value="logged">Logged</option>
          <option value="dispatched">Dispatched</option>
          <option value="failed">Failed</option>
        </Select>

        <Select
          value={filters.range}
          onChange={(e) => setFilter("range", e.target.value as typeof filters.range)}
        >
          <option value="1h">Last hour</option>
          <option value="24h">Last 24h</option>
          <option value="7d">Last 7 days</option>
          <option value="30d">Last 30 days</option>
          <option value="all">All time</option>
        </Select>

        {hasFilters ? (
          <Button variant="ghost" size="sm" onClick={resetFilters}>
            <X className="h-3.5 w-3.5" />
            Clear
          </Button>
        ) : null}
      </div>

      <Card className="overflow-hidden">
        <MarbleTable marbles={marbles} isLoading={isLoading} onRowClick={openMarble} />
      </Card>

      <MarbleSheet marbleId={activeMarbleId} onClose={() => openMarble(null)} />
    </div>
  );
}
