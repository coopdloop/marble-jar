import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useParams } from "react-router-dom";
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
import { ArrowLeft, Rocket } from "lucide-react";
import { marbleJarApi } from "@/lib/api";
import type { IntegrationType, TrendPoint } from "@/lib/types";
import { cn, formatCost, formatDuration, formatTokens } from "@/lib/utils";
import {
  Badge,
  Button,
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  Input,
  Label,
  Progress,
  Select,
  Skeleton,
  Stat,
  Textarea,
} from "@/components/ui/primitives";
import { MarbleTable } from "@/components/marbles/MarbleTable";
import { MarbleSheet } from "@/components/marbles/MarbleSheet";
import { useUiStore } from "@/stores/useAppStore";

export function ObjectiveDetailPage() {
  const { objectiveId = "" } = useParams();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { activeMarbleId, openMarble } = useUiStore();
  const [shipOpen, setShipOpen] = useState(false);

  const { data, isLoading } = useQuery({
    queryKey: ["objective", objectiveId],
    queryFn: () => marbleJarApi.getObjective(objectiveId),
    enabled: Boolean(objectiveId),
  });

  const { data: marbles } = useQuery({
    queryKey: ["marbles", { objective_id: objectiveId }],
    queryFn: () => marbleJarApi.listObjectiveMarbles(objectiveId, { limit: 100 }),
    enabled: Boolean(objectiveId),
  });

  const removeMutation = useMutation({
    mutationFn: (marbleId: string) =>
      marbleJarApi.removeMarbleFromObjective(objectiveId, marbleId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["objective", objectiveId] });
      queryClient.invalidateQueries({ queryKey: ["marbles"] });
    },
  });

  if (isLoading || !data) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-8 w-64" />
        <Skeleton className="h-32 w-full" />
        <Skeleton className="h-64 w-full" />
      </div>
    );
  }

  const { objective, rollup, trend, summary_preview } = data;

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="flex items-start gap-3">
          <Button variant="ghost" size="icon" onClick={() => navigate("/objectives")}>
            <ArrowLeft className="h-4 w-4" />
          </Button>
          <div>
            <div className="flex items-center gap-2">
              <h1 className="text-lg font-semibold tracking-tight">{objective.title}</h1>
              <Badge
                tone={
                  objective.status === "shipped"
                    ? "border-[hsl(var(--success))]/25 bg-[hsl(var(--success))]/10 text-[hsl(var(--success))]"
                    : undefined
                }
              >
                {objective.status}
              </Badge>
            </div>
            {objective.description ? (
              <p className="mt-0.5 max-w-2xl text-xs text-muted-foreground">
                {objective.description}
              </p>
            ) : null}
          </div>
        </div>

        <Button onClick={() => setShipOpen(true)} disabled={rollup.marble_count === 0}>
          <Rocket className="h-4 w-4" />
          Ship It
        </Button>
      </div>

      <Card>
        <CardContent className="grid grid-cols-2 gap-6 p-5 sm:grid-cols-4">
          <Stat label="Marbles" value={rollup.marble_count} />
          <Stat label="Cost" value={formatCost(rollup.cost_usd)} />
          <Stat
            label="Tokens"
            value={formatTokens(rollup.total_tokens)}
            hint={`${formatTokens(rollup.tokens_in)} in / ${formatTokens(rollup.tokens_out)} out`}
          />
          <Stat label="Runtime" value={formatDuration(rollup.duration_ms)} />
        </CardContent>

        {rollup.budget_pct_cost !== undefined || rollup.budget_pct_tokens !== undefined ? (
          <CardContent className="space-y-3 border-t border-border/60 pt-5">
            {rollup.budget_pct_cost !== undefined ? (
              <BudgetBar
                label="Cost budget"
                pct={rollup.budget_pct_cost}
                detail={`${formatCost(rollup.cost_usd)} of ${formatCost(objective.budget_cost_usd)}`}
              />
            ) : null}
            {rollup.budget_pct_tokens !== undefined ? (
              <BudgetBar
                label="Token budget"
                pct={rollup.budget_pct_tokens}
                tone="bg-accent"
                detail={`${formatTokens(rollup.total_tokens)} of ${formatTokens(objective.budget_tokens)}`}
              />
            ) : null}
          </CardContent>
        ) : null}
      </Card>

      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Cost over time</CardTitle>
          </CardHeader>
          <CardContent className="h-52">
            <TrendChart points={trend} dataKey="cost_usd" kind="line" />
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Tokens over time</CardTitle>
          </CardHeader>
          <CardContent className="h-52">
            <TrendChart points={trend} dataKey="tokens" kind="bar" />
          </CardContent>
        </Card>
      </div>

      <Card className="overflow-hidden">
        <CardHeader className="flex-row items-center justify-between">
          <CardTitle>Marbles in this objective</CardTitle>
          <span className="text-[11px] text-muted-foreground">
            {marbles?.items.length ?? 0} shown
          </span>
        </CardHeader>
        <MarbleTable
          marbles={marbles?.items ?? []}
          onRowClick={openMarble}
        />
        {(marbles?.items.length ?? 0) > 0 ? (
          <CardContent className="border-t border-border/60 pt-4">
            <p className="text-[11px] text-muted-foreground">
              Click a marble for trace lineage, or remove one below.
            </p>
            <div className="mt-2 flex flex-wrap gap-1.5">
              {marbles?.items.slice(0, 12).map((m) => (
                <button
                  key={m.id}
                  onClick={() => removeMutation.mutate(m.id)}
                  className="rounded-full border border-border/60 px-2 py-0.5 text-[11px] text-muted-foreground transition-colors hover:border-destructive/40 hover:text-destructive"
                  title="Remove from objective"
                >
                  {m.summary.slice(0, 28)} ×
                </button>
              ))}
            </div>
          </CardContent>
        ) : null}
      </Card>

      <ShipPreviewModal
        open={shipOpen}
        onClose={() => setShipOpen(false)}
        objectiveId={objectiveId}
        defaultSummary={summary_preview.markdown}
      />
      <MarbleSheet marbleId={activeMarbleId} onClose={() => openMarble(null)} />
    </div>
  );
}

function BudgetBar({
  label,
  pct,
  detail,
  tone,
}: {
  label: string;
  pct: number;
  detail: string;
  tone?: string;
}) {
  return (
    <div className="space-y-1.5">
      <div className="flex justify-between text-[11px]">
        <span className="text-muted-foreground">{label}</span>
        <span className={cn("numeric", pct > 100 && "font-medium text-destructive")}>
          {detail} ({pct.toFixed(0)}%)
        </span>
      </div>
      <Progress value={pct} tone={pct > 100 ? "bg-destructive" : tone} />
    </div>
  );
}

function TrendChart({
  points,
  dataKey,
  kind,
}: {
  points: TrendPoint[];
  dataKey: "cost_usd" | "tokens";
  kind: "line" | "bar";
}) {
  const data = points.map((p) => ({
    bucket: p.bucket.slice(5, 10),
    cost_usd: p.cost_usd,
    tokens: p.tokens_in + p.tokens_out,
  }));

  if (data.length === 0) {
    return (
      <div className="flex h-full items-center justify-center text-xs text-muted-foreground">
        No data yet.
      </div>
    );
  }

  const axisProps = {
    stroke: "hsl(var(--muted-foreground))",
    fontSize: 11,
    tickLine: false,
    axisLine: false,
  };
  const tooltipStyle = {
    backgroundColor: "hsl(var(--card))",
    border: "1px solid hsl(var(--border))",
    borderRadius: 8,
    fontSize: 12,
  };

  return (
    <ResponsiveContainer width="100%" height="100%">
      {kind === "line" ? (
        <LineChart data={data}>
          <CartesianGrid strokeDasharray="3 3" stroke="hsl(var(--border))" opacity={0.4} />
          <XAxis dataKey="bucket" {...axisProps} />
          <YAxis {...axisProps} width={44} />
          <Tooltip contentStyle={tooltipStyle} />
          <Line
            type="monotone"
            dataKey={dataKey}
            stroke="hsl(var(--primary))"
            strokeWidth={2}
            dot={false}
          />
        </LineChart>
      ) : (
        <BarChart data={data}>
          <CartesianGrid strokeDasharray="3 3" stroke="hsl(var(--border))" opacity={0.4} />
          <XAxis dataKey="bucket" {...axisProps} />
          <YAxis {...axisProps} width={44} />
          <Tooltip contentStyle={tooltipStyle} />
          <Bar dataKey={dataKey} fill="hsl(var(--accent))" radius={[4, 4, 0, 0]} />
        </BarChart>
      )}
    </ResponsiveContainer>
  );
}

/** Preview and edit the aggregate summary before it goes out. */
function ShipPreviewModal({
  open,
  onClose,
  objectiveId,
  defaultSummary,
}: {
  open: boolean;
  onClose: () => void;
  objectiveId: string;
  defaultSummary: string;
}) {
  const queryClient = useQueryClient();
  const [integration, setIntegration] = useState<IntegrationType>("slack");
  const [target, setTarget] = useState("");
  const [summary, setSummary] = useState(defaultSummary);
  const [markShipped, setMarkShipped] = useState(true);

  useEffect(() => {
    if (open) setSummary(defaultSummary);
  }, [open, defaultSummary]);

  const mutation = useMutation({
    mutationFn: () =>
      marbleJarApi.shipObjective(objectiveId, {
        integration_type: integration,
        target_config: shipTargetConfig(integration, target),
        summary_override: summary,
        mark_shipped: markShipped,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["objective", objectiveId] });
      queryClient.invalidateQueries({ queryKey: ["objectives"] });
      onClose();
    },
  });

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
      <div className="absolute inset-0 bg-black/50 backdrop-blur-sm" onClick={onClose} />
      <Card className="relative flex max-h-[85vh] w-full max-w-2xl flex-col animate-fade-in">
        <CardHeader>
          <CardTitle>Ship it</CardTitle>
          <p className="text-xs text-muted-foreground">
            Review the aggregate summary before it is posted on your behalf.
          </p>
        </CardHeader>
        <CardContent className="flex-1 space-y-3 overflow-y-auto">
          <div className="flex gap-2">
            <Select
              value={integration}
              onChange={(e) => setIntegration(e.target.value as IntegrationType)}
              className="w-40"
            >
              <option value="slack">Slack</option>
              <option value="teams">Teams</option>
              <option value="jira">Jira</option>
              <option value="webhook">Webhook</option>
            </Select>
            <Input
              value={target}
              onChange={(e) => setTarget(e.target.value)}
              placeholder={
                integration === "jira" ? "PROJ" : integration === "webhook" ? "https://…" : "#channel or webhook URL"
              }
              className="flex-1"
            />
          </div>

          <div className="space-y-1">
            <Label>Summary (editable)</Label>
            <Textarea
              value={summary}
              onChange={(e) => setSummary(e.target.value)}
              className="min-h-[220px] font-mono text-[11px]"
            />
          </div>

          <label className="flex items-center gap-2 text-xs text-muted-foreground">
            <input
              type="checkbox"
              checked={markShipped}
              onChange={(e) => setMarkShipped(e.target.checked)}
              className="rounded border-border"
            />
            Mark this objective as shipped
          </label>

          {mutation.isError ? (
            <p className="text-[11px] text-destructive">{(mutation.error as Error).message}</p>
          ) : null}
        </CardContent>
        <CardContent className="flex justify-end gap-2 border-t border-border/60 pt-4">
          <Button variant="ghost" size="sm" onClick={onClose}>
            Cancel
          </Button>
          <Button
            size="sm"
            onClick={() => mutation.mutate()}
            disabled={mutation.isPending || !target.trim()}
          >
            <Rocket className="h-3.5 w-3.5" />
            {mutation.isPending ? "Shipping…" : "Ship it"}
          </Button>
        </CardContent>
      </Card>
    </div>
  );
}

function shipTargetConfig(integration: IntegrationType, target: string): Record<string, unknown> {
  switch (integration) {
    case "jira":
      return { project_key: target };
    case "slack":
      return target.startsWith("http") ? { webhook_url: target } : { channel: target };
    case "teams":
      return target.startsWith("http") ? { webhook_url: target } : { channel_id: target };
    case "webhook":
      return { url: target };
  }
}
