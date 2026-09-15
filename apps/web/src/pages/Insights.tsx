import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Bar,
  BarChart,
  CartesianGrid,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { Sparkles, X } from "lucide-react";
import { marbleJarApi } from "@/lib/api";
import { cn, formatCost, formatDuration, formatTokens } from "@/lib/utils";
import {
  Button,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  Select,
  Skeleton,
  Stat,
} from "@/components/ui/primitives";

const GRANULARITIES = [
  { value: "hour", label: "Hourly" },
  { value: "day", label: "Daily" },
  { value: "week", label: "Weekly" },
];

/**
 * Hourly is capped at a week on purpose: the server will happily answer a
 * 365-day hourly request with ~8760 buckets, which is slow to fetch and unread-
 * able to plot. Coarser buckets may span the whole year.
 */
const WINDOWS = {
  hour: ["1", "3", "7"],
  coarse: ["7", "30", "90", "180", "365"],
};

const DEFAULTS = {
  granularity: "day",
  days: "30",
  projectId: "",
  objectiveId: "",
  model: "",
};

const AXIS = {
  stroke: "hsl(var(--muted-foreground))",
  fontSize: 11,
  tickLine: false,
  axisLine: false,
};

const TOOLTIP_STYLE = {
  backgroundColor: "hsl(var(--card))",
  border: "1px solid hsl(var(--border))",
  borderRadius: 8,
  fontSize: 12,
};

export function InsightsPage() {
  const [granularity, setGranularity] = useState(DEFAULTS.granularity);
  const [days, setDays] = useState(DEFAULTS.days);
  const [projectId, setProjectId] = useState(DEFAULTS.projectId);
  const [objectiveId, setObjectiveId] = useState(DEFAULTS.objectiveId);
  const [model, setModel] = useState(DEFAULTS.model);

  // Clamped during render rather than in an effect, so switching to hourly while
  // a 30-day window is stored never emits the invalid combination.
  const windowOptions = granularity === "hour" ? WINDOWS.hour : WINDOWS.coarse;
  const daysValue = windowOptions.includes(days) ? days : windowOptions[1];

  // Unset filters stay undefined so the api layer drops them from the query.
  const params = {
    interval: granularity,
    days: daysValue,
    project_id: projectId || undefined,
    objective_id: objectiveId || undefined,
    model: model || undefined,
  };

  // GET /v1/trends/cost and GET /v1/trends/tokens are one and the same handler
  // (httpapi.Server.trends) returning the full TrendPoint rows, so a single
  // request feeds the stat row and all three charts.
  const { data, isLoading, isError, error } = useQuery({
    queryKey: ["insights", params],
    queryFn: () => marbleJarApi.costTrends(params),
  });

  const { data: projects } = useQuery({
    queryKey: ["projects"],
    queryFn: marbleJarApi.listProjects,
    staleTime: 60_000,
  });

  const { data: objectives } = useQuery({
    queryKey: ["objectives", { limit: 100 }],
    queryFn: () => marbleJarApi.listObjectives({ limit: 100 }),
    staleTime: 30_000,
  });

  const { data: jar } = useQuery({
    queryKey: ["jar-status"],
    queryFn: marbleJarApi.jarStatus,
    staleTime: 30_000,
  });

  const points = data?.points ?? [];

  const totals = points.reduce(
    (acc, p) => ({
      cost: acc.cost + p.cost_usd,
      tokens: acc.tokens + p.tokens_in + p.tokens_out,
      marbles: acc.marbles + p.marble_count,
      duration: acc.duration + p.duration_ms,
    }),
    { cost: 0, tokens: 0, marbles: 0, duration: 0 },
  );

  const costSeries = points.map((p) => ({
    label: tickLabel(p.bucket, granularity),
    value: p.cost_usd,
  }));
  const marbleSeries = points.map((p) => ({
    label: tickLabel(p.bucket, granularity),
    value: p.marble_count,
  }));
  const tokenSeries = points.map((p) => ({
    label: tickLabel(p.bucket, granularity),
    tokens_in: p.tokens_in,
    tokens_out: p.tokens_out,
  }));

  const dirty =
    granularity !== DEFAULTS.granularity ||
    daysValue !== DEFAULTS.days ||
    projectId !== DEFAULTS.projectId ||
    objectiveId !== DEFAULTS.objectiveId ||
    model !== DEFAULTS.model;

  const reset = () => {
    setGranularity(DEFAULTS.granularity);
    setDays(DEFAULTS.days);
    setProjectId(DEFAULTS.projectId);
    setObjectiveId(DEFAULTS.objectiveId);
    setModel(DEFAULTS.model);
  };

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="text-lg font-semibold tracking-tight">Insights</h1>
          <p className="text-xs text-muted-foreground">
            Where the marbles went — spend, tokens and throughput over time.
          </p>
        </div>
        {isLoading ? (
          <Skeleton className="h-10 w-full max-w-md" />
        ) : isError ? (
          <p className="text-xs text-destructive">{(error as Error).message}</p>
        ) : (
          <div className="flex flex-wrap gap-6">
            <Stat label="Spend" value={formatCost(totals.cost)} hint={`last ${daysValue} days`} />
            <Stat label="Tokens" value={formatTokens(totals.tokens)} />
            <Stat label="Marbles" value={totals.marbles} />
            <Stat
              label="Avg / marble"
              value={totals.marbles ? formatCost(totals.cost / totals.marbles) : "—"}
            />
            <Stat
              label="Avg runtime"
              value={totals.marbles ? formatDuration(totals.duration / totals.marbles) : "—"}
            />
          </div>
        )}
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <Select value={granularity} onChange={(e) => setGranularity(e.target.value)}>
          {GRANULARITIES.map((g) => (
            <option key={g.value} value={g.value}>
              {g.label}
            </option>
          ))}
        </Select>

        <Select value={daysValue} onChange={(e) => setDays(e.target.value)}>
          {windowOptions.map((w) => (
            <option key={w} value={w}>
              Last {w} day{w === "1" ? "" : "s"}
            </option>
          ))}
        </Select>

        <Select value={projectId} onChange={(e) => setProjectId(e.target.value)}>
          <option value="">All projects</option>
          {projects?.items.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </Select>

        <Select value={objectiveId} onChange={(e) => setObjectiveId(e.target.value)}>
          <option value="">All objectives</option>
          {objectives?.items.map((o) => (
            <option key={o.id} value={o.id}>
              {o.title}
            </option>
          ))}
        </Select>

        {/* top_models is the only model vocabulary the API exposes to the UI. */}
        <Select value={model} onChange={(e) => setModel(e.target.value)}>
          <option value="">All models</option>
          {jar?.top_models.map((m) => (
            <option key={m} value={m}>
              {m}
            </option>
          ))}
        </Select>

        {dirty ? (
          <Button variant="ghost" size="sm" onClick={reset}>
            <X className="h-3.5 w-3.5" />
            Clear
          </Button>
        ) : null}
      </div>

      <div className="grid gap-4 lg:grid-cols-2">
        <ChartCard
          className="lg:col-span-2"
          title="Cost per bucket"
          hint="Sum of marble cost in each interval."
          loading={isLoading}
          error={isError ? (error as Error).message : null}
          empty={costSeries.length === 0}
        >
          <BarChart data={costSeries}>
            <CartesianGrid strokeDasharray="3 3" stroke="hsl(var(--border))" opacity={0.4} />
            <XAxis dataKey="label" {...AXIS} />
            <YAxis {...AXIS} width={56} />
            <Tooltip
              contentStyle={TOOLTIP_STYLE}
              formatter={(value) => formatCost(Number(value))}
            />
            <Bar dataKey="value" name="cost" fill="hsl(var(--primary))" radius={[4, 4, 0, 0]} />
          </BarChart>
        </ChartCard>

        <ChartCard
          title="Tokens per bucket"
          hint="Prompt tokens against completion tokens."
          loading={isLoading}
          error={isError ? (error as Error).message : null}
          empty={tokenSeries.length === 0}
        >
          <LineChart data={tokenSeries}>
            <CartesianGrid strokeDasharray="3 3" stroke="hsl(var(--border))" opacity={0.4} />
            <XAxis dataKey="label" {...AXIS} />
            <YAxis {...AXIS} width={44} tickFormatter={(value) => formatTokens(Number(value))} />
            <Tooltip
              contentStyle={TOOLTIP_STYLE}
              formatter={(value) => formatTokens(Number(value))}
            />
            <Line
              type="monotone"
              dataKey="tokens_in"
              stroke="hsl(var(--primary))"
              strokeWidth={2}
              dot={false}
            />
            <Line
              type="monotone"
              dataKey="tokens_out"
              stroke="hsl(var(--accent))"
              strokeWidth={2}
              dot={false}
            />
          </LineChart>
        </ChartCard>

        <ChartCard
          title="Marbles per bucket"
          hint="How much finished work actually landed."
          loading={isLoading}
          error={isError ? (error as Error).message : null}
          empty={marbleSeries.length === 0}
        >
          <BarChart data={marbleSeries}>
            <CartesianGrid strokeDasharray="3 3" stroke="hsl(var(--border))" opacity={0.4} />
            <XAxis dataKey="label" {...AXIS} />
            <YAxis {...AXIS} width={44} allowDecimals={false} />
            <Tooltip contentStyle={TOOLTIP_STYLE} />
            <Bar dataKey="value" name="marbles" fill="hsl(var(--accent))" radius={[4, 4, 0, 0]} />
          </BarChart>
        </ChartCard>
      </div>

      <Card>
        <CardHeader className="flex-row flex-wrap items-center gap-x-3 gap-y-2">
          <CardTitle className="flex items-center gap-2">
            <Sparkles className="h-3.5 w-3.5" />
            Top models
          </CardTitle>
          <CardDescription>Busiest models of the last 7 days — click to filter.</CardDescription>
        </CardHeader>
        <CardContent className="flex flex-wrap items-center gap-2">
          {(jar?.top_models.length ?? 0) === 0 ? (
            <p className="text-xs text-muted-foreground">
              No model has reported a marble in the last week.
            </p>
          ) : (
            jar?.top_models.map((m) => (
              <button
                key={m}
                type="button"
                onClick={() => setModel(model === m ? "" : m)}
                className={cn(
                  "rounded-full border px-2.5 py-0.5 text-[11px] font-medium transition-colors",
                  model === m
                    ? "border-primary/30 bg-primary/10 text-primary"
                    : "border-border bg-muted/60 text-muted-foreground hover:bg-secondary hover:text-foreground",
                )}
              >
                {m}
              </button>
            ))
          )}
        </CardContent>
      </Card>
    </div>
  );
}

function ChartCard({
  title,
  hint,
  loading,
  error,
  empty,
  className,
  children,
}: {
  title: string;
  hint: string;
  loading: boolean;
  error: string | null;
  empty: boolean;
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <Card className={cn("flex flex-col", className)}>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        <CardDescription>{hint}</CardDescription>
      </CardHeader>
      <CardContent className="h-64">
        {loading ? (
          <Skeleton className="h-full w-full" />
        ) : error ? (
          <div className="flex h-full items-center justify-center text-xs text-destructive">
            {error}
          </div>
        ) : empty ? (
          <div className="flex h-full items-center justify-center text-xs text-muted-foreground">
            No marbles in this window yet.
          </div>
        ) : (
          <ResponsiveContainer width="100%" height="100%">
            {children as React.ReactElement}
          </ResponsiveContainer>
        )}
      </CardContent>
    </Card>
  );
}

function tickLabel(bucket: string, granularity: string): string {
  return granularity === "hour" ? bucket.slice(11, 16) : bucket.slice(5, 10);
}
