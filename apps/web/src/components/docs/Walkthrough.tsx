import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, Circle, Loader2, Sparkles } from "lucide-react";
import { Link } from "react-router-dom";
import { marbleJarApi } from "@/lib/api";
import { cn } from "@/lib/utils";
import { Button, Card, Progress } from "@/components/ui/primitives";

const DEMO_SUMMARIES = [
  "Refactored the auth middleware into composable guards",
  "Migrated the billing webhook to the new event schema",
  "Tightened N+1 queries on the dashboard endpoint",
  "Shipped dark-mode tokens for the settings page",
  "Backfilled idempotency keys on the ingestion path",
];

/**
 * First-run checklist. Completion is derived from live workspace data, so it
 * works across sessions, browsers and teammates — no localStorage flags that
 * can lie.
 */
export function Walkthrough() {
  const queryClient = useQueryClient();

  const { data: apiKeys } = useQuery({ queryKey: ["api-keys"], queryFn: marbleJarApi.listApiKeys });
  const { data: marbles } = useQuery({
    queryKey: ["marbles", "tour"],
    queryFn: () => marbleJarApi.listMarbles({ limit: 5 }),
  });
  const { data: objectives } = useQuery({
    queryKey: ["objectives", "tour"],
    queryFn: () => marbleJarApi.listObjectives({ limit: 25 }),
  });
  const { data: rules } = useQuery({ queryKey: ["rules"], queryFn: marbleJarApi.listRules });
  const { data: dispatches } = useQuery({
    queryKey: ["dispatches", "tour"],
    queryFn: () => marbleJarApi.listDispatches({ limit: 1 }),
  });
  const { data: integrations } = useQuery({
    queryKey: ["integrations"],
    queryFn: marbleJarApi.listIntegrations,
  });

  const invalidate = (...keys: string[]) =>
    keys.forEach((k) => queryClient.invalidateQueries({ queryKey: [k] }));

  const dropMarble = useMutation({
    mutationFn: () =>
      marbleJarApi.createMarble(
        {
          summary: DEMO_SUMMARIES[Math.floor(Math.random() * DEMO_SUMMARIES.length)],
          model: "guided-tour",
          project: "getting-started",
          cost_usd: Math.round(Math.random() * 50) / 100 + 0.05,
          tokens_input: 1000 + Math.floor(Math.random() * 12000),
          tokens_output: 400 + Math.floor(Math.random() * 3000),
          duration_ms: 5000 + Math.floor(Math.random() * 60000),
        },
        `tour-${crypto.randomUUID()}`,
      ),
    onSuccess: () => invalidate("marbles", "jar-status"),
  });

  const bucketMarble = useMutation({
    mutationFn: () => {
      const objective = objectives?.items[0];
      const marble = marbles?.items.find((m) => !m.objective_id) ?? marbles?.items[0];
      if (!objective || !marble) throw new Error("Need an objective and a marble first");
      return marbleJarApi.addMarbleToObjective(objective.id, marble.id);
    },
    onSuccess: () => invalidate("objectives", "marbles"),
  });

  const firstObjective = objectives?.items[0];
  const steps: StepDef[] = [
    {
      title: "Create an API key",
      body: "Agents authenticate with mj_ keys. Make one under Settings.",
      done: (apiKeys?.items.length ?? 0) > 0,
      action: { kind: "link", to: "/settings", label: "Open Settings" },
    },
    {
      title: "Drop your first marble",
      body: "A marble is one finished unit of agent work. Drop a demo one and watch it land on the queue, linked to the thread it belongs to.",
      done: (marbles?.items.length ?? 0) > 0,
      action: {
        kind: "run",
        label: "Drop a demo marble",
        onRun: () => dropMarble.mutate(),
        pending: dropMarble.isPending,
      },
    },
    {
      title: "Create an objective",
      body: "Objectives are bigger jars — cost, token and time rollups toward a goal.",
      done: (objectives?.items.length ?? 0) > 0,
      action: { kind: "link", to: "/objectives", label: "Open Objectives" },
    },
    {
      title: "Bucket a marble into it",
      body: firstObjective
        ? `Add your latest marble to “${firstObjective.title}” — or drag rows onto the card on the Objectives page.`
        : "Drag a marble onto an objective card to bucket it.",
      done: (objectives?.items ?? []).some((o) => (o.rollup?.marble_count ?? 0) > 0),
      action:
        firstObjective && (marbles?.items.length ?? 0) > 0
          ? {
              kind: "run",
              label: "Add latest marble",
              onRun: () => bucketMarble.mutate(),
              pending: bucketMarble.isPending,
            }
          : { kind: "link", to: "/objectives", label: "Open Objectives" },
    },
    {
      title: "Automate with a rule",
      body: "Condition chips → a Slack channel, Jira project, Teams or webhook. The live preview shows what would have matched.",
      done: (rules?.items.length ?? 0) > 0,
      action: { kind: "link", to: "/rules", label: "Open Rules" },
    },
    {
      title: "Fire a dispatch",
      body: "Click any marble on the queue, then Dispatch from its detail sheet — one click posts the update.",
      done: (dispatches?.items.length ?? 0) > 0,
      action: { kind: "link", to: "/", label: "Open the Queue" },
    },
    {
      title: "Connect an integration",
      body: "Link Jira, Slack or Teams so dispatches run on-behalf-of you, with a full audit trail.",
      done: (integrations?.items ?? []).some((i) => i.connected),
      action: { kind: "link", to: "/integrations", label: "Open Integrations" },
    },
  ];

  const doneCount = steps.filter((s) => s.done).length;
  const pct = Math.round((doneCount / steps.length) * 100);

  return (
    <Card className="p-5">
      <div className="flex items-center justify-between gap-4">
        <div>
          <h3 className="flex items-center gap-2 text-sm font-semibold tracking-tight">
            <Sparkles className="h-4 w-4 text-primary" />
            Guided tour
          </h3>
          <p className="mt-0.5 text-xs text-muted-foreground">
            Seven steps, every feature, ten minutes. Progress is read from your workspace live.
          </p>
        </div>
        <span className="numeric text-xs text-muted-foreground">
          {doneCount}/{steps.length}
        </span>
      </div>
      <Progress value={pct} className="mt-3" />

      <ol className="mt-4 space-y-1">
        {steps.map((step, i) => (
          <Step key={step.title} index={i} step={step} />
        ))}
      </ol>
    </Card>
  );
}

type StepAction =
  | { kind: "link"; to: string; label: string }
  | { kind: "run"; label: string; onRun: () => void; pending: boolean };

interface StepDef {
  title: string;
  body: string;
  done: boolean;
  action: StepAction;
}

function Step({ index, step }: { index: number; step: StepDef }) {
  return (
    <li
      className={cn(
        "flex items-start gap-3 rounded-xl border border-transparent px-3 py-2.5 transition-colors",
        step.done ? "opacity-70" : "border-border/60 bg-card/40",
      )}
    >
      <span className="mt-0.5 shrink-0">
        {step.done ? (
          <span className="flex h-5 w-5 items-center justify-center rounded-full bg-[hsl(var(--success))]/15">
            <Check className="h-3 w-3 text-[hsl(var(--success))]" />
          </span>
        ) : (
          <span className="flex h-5 w-5 items-center justify-center rounded-full border border-border text-[10px] text-muted-foreground">
            {index + 1}
          </span>
        )}
      </span>
      <div className="min-w-0 flex-1">
        <p
          className={cn(
            "text-sm font-medium",
            step.done && "line-through decoration-muted-foreground/40",
          )}
        >
          {step.title}
        </p>
        <p className="mt-0.5 text-xs text-muted-foreground">{step.body}</p>
        {!step.done && step.action.kind === "link" ? (
          <Button asChild variant="outline" size="sm" className="mt-2">
            <Link to={step.action.to}>{step.action.label}</Link>
          </Button>
        ) : null}
        {!step.done && step.action.kind === "run" ? (
          <Button size="sm" className="mt-2" onClick={step.action.onRun} disabled={step.action.pending}>
            {step.action.pending ? (
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
            ) : (
              <Circle className="h-3 w-3 fill-current" />
            )}
            {step.action.label}
          </Button>
        ) : null}
      </div>
    </li>
  );
}
