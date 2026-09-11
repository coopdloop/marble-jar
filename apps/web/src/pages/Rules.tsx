import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, Plus, Trash2, X, Zap } from "lucide-react";
import { marbleJarApi } from "@/lib/api";
import type { IntegrationType, RuleCondition, RuleField, RuleOperator } from "@/lib/types";
import { cn, formatCost } from "@/lib/utils";
import {
  Badge,
  Button,
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  Input,
  Label,
  Select,
  Skeleton,
} from "@/components/ui/primitives";

/** A flat chip is what the builder edits; it compiles to a nested condition. */
interface Chip {
  field: RuleField;
  op: RuleOperator;
  value: string;
}

const FIELDS: { value: RuleField; label: string; numeric?: boolean }[] = [
  { value: "project", label: "Project" },
  { value: "model", label: "Model" },
  { value: "agent", label: "Agent" },
  { value: "status", label: "Status" },
  { value: "summary", label: "Summary" },
  { value: "cost_usd", label: "Cost (USD)", numeric: true },
  { value: "tokens_total", label: "Total tokens", numeric: true },
  { value: "duration_ms", label: "Duration (ms)", numeric: true },
];

const OPS: { value: RuleOperator; label: string; numericOnly?: boolean }[] = [
  { value: "eq", label: "is" },
  { value: "neq", label: "is not" },
  { value: "contains", label: "contains" },
  { value: "gt", label: ">", numericOnly: true },
  { value: "gte", label: "≥", numericOnly: true },
  { value: "lt", label: "<", numericOnly: true },
  { value: "lte", label: "≤", numericOnly: true },
];

function chipsToCondition(chips: Chip[], mode: "all" | "any"): RuleCondition {
  if (chips.length === 0) return {};
  const leaves: RuleCondition[] = chips.map((c) => {
    const numeric = FIELDS.find((f) => f.value === c.field)?.numeric;
    return {
      field: c.field,
      op: c.op,
      value: numeric ? Number(c.value) : c.value,
    };
  });
  if (leaves.length === 1) return leaves[0]!;
  return mode === "all" ? { all: leaves } : { any: leaves };
}

function conditionToChips(cond: RuleCondition): { chips: Chip[]; mode: "all" | "any" } {
  const leaves = cond.all ?? cond.any ?? (cond.field ? [cond] : []);
  return {
    mode: cond.any ? "any" : "all",
    chips: leaves
      .filter((l) => l.field)
      .map((l) => ({
        field: l.field as RuleField,
        op: (l.op ?? "eq") as RuleOperator,
        value: String(l.value ?? ""),
      })),
  };
}

export function RulesPage() {
  const queryClient = useQueryClient();
  const [chips, setChips] = useState<Chip[]>([]);
  const [mode, setMode] = useState<"all" | "any">("all");
  const [name, setName] = useState("");
  const [target, setTarget] = useState<IntegrationType>("slack");
  const [targetValue, setTargetValue] = useState("");

  const { data, isLoading } = useQuery({
    queryKey: ["rules"],
    queryFn: () => marbleJarApi.listRules(),
  });

  const condition = chipsToCondition(chips, mode);

  const { data: preview } = useQuery({
    queryKey: ["rule-preview", condition],
    queryFn: () => marbleJarApi.testRule(condition, 20),
    enabled: chips.length > 0,
  });

  const createMutation = useMutation({
    mutationFn: () =>
      marbleJarApi.createRule({
        name,
        condition,
        target_type: target,
        target_config: ruleTargetConfig(target, targetValue),
        is_active: true,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["rules"] });
      setChips([]);
      setName("");
      setTargetValue("");
    },
  });

  const toggleMutation = useMutation({
    mutationFn: ({ id, active }: { id: string; active: boolean }) =>
      marbleJarApi.updateRule(id, { is_active: active }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["rules"] }),
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) => marbleJarApi.deleteRule(id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["rules"] }),
  });

  return (
    <div className="space-y-5">
      <div>
        <h1 className="text-lg font-semibold tracking-tight">Rules</h1>
        <p className="text-xs text-muted-foreground">
          Graduate from manual dispatch to automation — test before you trust.
        </p>
      </div>

      <div className="grid gap-4 lg:grid-cols-[1.2fr_1fr]">
        <Card>
          <CardHeader>
            <CardTitle>Rule builder</CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="space-y-1">
              <Label>Rule name</Label>
              <Input
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="Expensive payments work → #eng-updates"
              />
            </div>

            <div className="space-y-2">
              <div className="flex items-center gap-2">
                <Label>Match</Label>
                <Select
                  value={mode}
                  onChange={(e) => setMode(e.target.value as "all" | "any")}
                  className="h-7 text-xs"
                >
                  <option value="all">all conditions</option>
                  <option value="any">any condition</option>
                </Select>
              </div>

              <div className="flex flex-wrap gap-2">
                {chips.map((chip, i) => (
                  <ConditionChip
                    key={i}
                    chip={chip}
                    onChange={(c) => setChips(chips.map((x, j) => (j === i ? c : x)))}
                    onRemove={() => setChips(chips.filter((_, j) => j !== i))}
                  />
                ))}
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => setChips([...chips, { field: "project", op: "eq", value: "" }])}
                >
                  <Plus className="h-3.5 w-3.5" />
                  Condition
                </Button>
              </div>
            </div>

            <div className="space-y-1">
              <Label>Then dispatch to</Label>
              <div className="flex gap-2">
                <Select
                  value={target}
                  onChange={(e) => setTarget(e.target.value as IntegrationType)}
                  className="w-32"
                >
                  <option value="slack">Slack</option>
                  <option value="teams">Teams</option>
                  <option value="jira">Jira</option>
                  <option value="webhook">Webhook</option>
                </Select>
                <Input
                  value={targetValue}
                  onChange={(e) => setTargetValue(e.target.value)}
                  placeholder={
                    target === "jira" ? "PROJ" : target === "webhook" ? "https://…" : "#channel or webhook URL"
                  }
                  className="flex-1"
                />
              </div>
            </div>

            {createMutation.isError ? (
              <p className="text-[11px] text-destructive">
                {(createMutation.error as Error).message}
              </p>
            ) : null}

            <Button
              className="w-full"
              onClick={() => createMutation.mutate()}
              disabled={!name.trim() || chips.length === 0 || !targetValue.trim() || createMutation.isPending}
            >
              <Zap className="h-4 w-4" />
              {createMutation.isPending ? "Creating…" : "Create rule"}
            </Button>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Test against recent marbles</CardTitle>
            <p className="text-xs text-muted-foreground">
              {preview
                ? `${preview.matched_count} of the last ${preview.sample_size} marbles would match.`
                : "Add a condition to preview matches."}
            </p>
          </CardHeader>
          <CardContent className="max-h-80 space-y-1.5 overflow-y-auto">
            {preview?.results.map(({ marble, matched }) => (
              <div
                key={marble.id}
                className={cn(
                  "flex items-center gap-2 rounded-lg border px-2.5 py-1.5 text-[11px]",
                  matched
                    ? "border-[hsl(var(--success))]/30 bg-[hsl(var(--success))]/5"
                    : "border-border/50 opacity-50",
                )}
              >
                {matched ? (
                  <Check className="h-3 w-3 shrink-0 text-[hsl(var(--success))]" />
                ) : (
                  <X className="h-3 w-3 shrink-0 text-muted-foreground" />
                )}
                <span className="flex-1 truncate">{marble.summary}</span>
                <span className="numeric shrink-0 text-muted-foreground">
                  {formatCost(marble.cost_usd)}
                </span>
              </div>
            ))}
          </CardContent>
        </Card>
      </div>

      <Card className="overflow-hidden">
        <CardHeader>
          <CardTitle>Active rules</CardTitle>
        </CardHeader>
        {isLoading ? (
          <CardContent className="space-y-2">
            <Skeleton className="h-12 w-full" />
            <Skeleton className="h-12 w-full" />
          </CardContent>
        ) : (data?.items.length ?? 0) === 0 ? (
          <CardContent>
            <p className="text-xs text-muted-foreground">
              No rules yet. Everything is dispatched manually — which is a fine place to start.
            </p>
          </CardContent>
        ) : (
          <div className="divide-y divide-border/40">
            {data?.items.map((rule) => {
              const { chips: ruleChips, mode: ruleMode } = conditionToChips(rule.condition);
              return (
                <div key={rule.id} className="flex items-center gap-3 px-5 py-3">
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      <span className="truncate text-sm font-medium">{rule.name}</span>
                      <Badge>{rule.target_type}</Badge>
                    </div>
                    <div className="mt-1 flex flex-wrap gap-1">
                      {ruleChips.map((c, i) => (
                        <span
                          key={i}
                          className="rounded border border-border/60 bg-muted/40 px-1.5 py-0.5 text-[10px] text-muted-foreground"
                        >
                          {i > 0 ? `${ruleMode === "all" ? "AND " : "OR "}` : ""}
                          {c.field} {OPS.find((o) => o.value === c.op)?.label ?? c.op} {c.value}
                        </span>
                      ))}
                    </div>
                  </div>

                  <button
                    onClick={() =>
                      toggleMutation.mutate({ id: rule.id, active: !rule.is_active })
                    }
                    className={cn(
                      "relative h-5 w-9 shrink-0 rounded-full transition-colors",
                      rule.is_active ? "bg-primary" : "bg-muted",
                    )}
                    aria-label={rule.is_active ? "Disable rule" : "Enable rule"}
                  >
                    <span
                      className={cn(
                        "absolute top-0.5 h-4 w-4 rounded-full bg-white transition-transform",
                        rule.is_active ? "translate-x-4.5 left-0.5" : "left-0.5",
                      )}
                      style={{ transform: rule.is_active ? "translateX(16px)" : undefined }}
                    />
                  </button>

                  <Button
                    variant="ghost"
                    size="icon"
                    className="shrink-0 text-muted-foreground hover:text-destructive"
                    onClick={() => deleteMutation.mutate(rule.id)}
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </Button>
                </div>
              );
            })}
          </div>
        )}
      </Card>
    </div>
  );
}

function ConditionChip({
  chip,
  onChange,
  onRemove,
}: {
  chip: Chip;
  onChange: (c: Chip) => void;
  onRemove: () => void;
}) {
  const numeric = FIELDS.find((f) => f.value === chip.field)?.numeric ?? false;
  const ops = OPS.filter((o) => (numeric ? o.value !== "contains" : !o.numericOnly));

  return (
    <div className="flex items-center gap-1 rounded-lg border border-border bg-background/50 p-1">
      <Select
        value={chip.field}
        onChange={(e) => onChange({ ...chip, field: e.target.value as RuleField })}
        className="h-7 border-0 bg-transparent px-1 text-xs shadow-none"
      >
        {FIELDS.map((f) => (
          <option key={f.value} value={f.value}>
            {f.label}
          </option>
        ))}
      </Select>
      <Select
        value={chip.op}
        onChange={(e) => onChange({ ...chip, op: e.target.value as RuleOperator })}
        className="h-7 border-0 bg-transparent px-1 text-xs shadow-none"
      >
        {ops.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </Select>
      <Input
        value={chip.value}
        onChange={(e) => onChange({ ...chip, value: e.target.value })}
        type={numeric ? "number" : "text"}
        step="any"
        placeholder="value"
        className="h-7 w-28 border-0 bg-transparent px-1 text-xs shadow-none"
      />
      <button
        onClick={onRemove}
        className="rounded p-1 text-muted-foreground transition-colors hover:text-destructive"
        aria-label="Remove condition"
      >
        <X className="h-3 w-3" />
      </button>
    </div>
  );
}

function ruleTargetConfig(target: IntegrationType, value: string): Record<string, unknown> {
  switch (target) {
    case "jira":
      return { project_key: value };
    case "slack":
      return value.startsWith("http") ? { webhook_url: value } : { channel: value };
    case "teams":
      return value.startsWith("http") ? { webhook_url: value } : { channel_id: value };
    case "webhook":
      return { url: value };
  }
}
