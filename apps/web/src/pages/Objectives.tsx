import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import { motion } from "framer-motion";
import { Plus, Target } from "lucide-react";
import { marbleJarApi } from "@/lib/api";
import type { Objective } from "@/lib/types";
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
  Skeleton,
  Textarea,
} from "@/components/ui/primitives";
import { MarbleTable } from "@/components/marbles/MarbleTable";
import { useUiStore } from "@/stores/useAppStore";

export function ObjectivesPage() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [creating, setCreating] = useState(false);

  const { data, isLoading } = useQuery({
    queryKey: ["objectives"],
    queryFn: () => marbleJarApi.listObjectives({ limit: 100 }),
  });

  // The drag source: marbles not yet bucketed into any objective.
  const { data: unassigned } = useQuery({
    queryKey: ["marbles", { unassigned: true }],
    queryFn: () => marbleJarApi.listMarbles({ unassigned: true, limit: 25 }),
  });

  const addMutation = useMutation({
    mutationFn: ({ objectiveId, marbleId }: { objectiveId: string; marbleId: string }) =>
      marbleJarApi.addMarbleToObjective(objectiveId, marbleId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["objectives"] });
      queryClient.invalidateQueries({ queryKey: ["marbles"] });
    },
  });

  return (
    <div className="space-y-5">
      <div className="flex items-end justify-between gap-4">
        <div>
          <h1 className="text-lg font-semibold tracking-tight">Objectives</h1>
          <p className="text-xs text-muted-foreground">
            Bigger jars — group marbles for cost, time and token rollups.
          </p>
        </div>
        <Button size="sm" onClick={() => setCreating(true)}>
          <Plus className="h-3.5 w-3.5" />
          New objective
        </Button>
      </div>

      {isLoading ? (
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {Array.from({ length: 3 }).map((_, i) => (
            <Skeleton key={i} className="h-44" />
          ))}
        </div>
      ) : (data?.items.length ?? 0) === 0 ? (
        <Card className="flex h-44 flex-col items-center justify-center gap-2">
          <Target className="h-6 w-6 text-muted-foreground" />
          <p className="text-sm font-medium">No objectives yet</p>
          <p className="max-w-sm text-center text-xs text-muted-foreground">
            Create one, then drag marbles onto it to track cost and progress toward a goal.
          </p>
        </Card>
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {data?.items.map((objective) => (
            <ObjectiveCard
              key={objective.id}
              objective={objective}
              onClick={() => navigate(`/objectives/${objective.id}`)}
              onDropMarble={(marbleId) =>
                addMutation.mutate({ objectiveId: objective.id, marbleId })
              }
            />
          ))}
        </div>
      )}

      {(unassigned?.items.length ?? 0) > 0 ? (
        <Card className="overflow-hidden">
          <CardHeader>
            <CardTitle>Unassigned marbles</CardTitle>
            <p className="text-xs text-muted-foreground">
              Drag a row onto an objective card above to bucket it.
            </p>
          </CardHeader>
          <MarbleTable marbles={unassigned?.items ?? []} draggable />
        </Card>
      ) : null}

      <CreateObjectiveDialog open={creating} onClose={() => setCreating(false)} />
    </div>
  );
}

function ObjectiveCard({
  objective,
  onClick,
  onDropMarble,
}: {
  objective: Objective;
  onClick: () => void;
  onDropMarble: (marbleId: string) => void;
}) {
  const [isOver, setIsOver] = useState(false);
  const dragMarbleId = useUiStore((s) => s.dragMarbleId);
  const rollup = objective.rollup;

  return (
    <motion.div
      layout
      onDragOver={(e) => {
        e.preventDefault();
        setIsOver(true);
      }}
      onDragLeave={() => setIsOver(false)}
      onDrop={(e) => {
        e.preventDefault();
        setIsOver(false);
        const id = e.dataTransfer.getData("text/marble-id") || dragMarbleId;
        if (id) onDropMarble(id);
      }}
      onClick={onClick}
      className={cn(
        "glass glass-hover cursor-pointer rounded-2xl p-5 transition-all",
        isOver && "scale-[1.02] border-primary/50 ring-2 ring-primary/40",
      )}
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <h3 className="truncate text-sm font-semibold">{objective.title}</h3>
          {objective.description ? (
            <p className="mt-0.5 line-clamp-2 text-[11px] text-muted-foreground">
              {objective.description}
            </p>
          ) : null}
        </div>
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

      <div className="mt-4 flex items-baseline gap-4">
        <div>
          <div className="numeric text-2xl font-semibold">{rollup?.marble_count ?? 0}</div>
          <div className="text-[11px] text-muted-foreground">marbles</div>
        </div>
        <div className="ml-auto text-right text-[11px] text-muted-foreground">
          <div className="numeric">{formatCost(rollup?.cost_usd ?? 0)}</div>
          <div className="numeric">{formatTokens(rollup?.total_tokens ?? 0)} tokens</div>
          <div className="numeric">{formatDuration(rollup?.duration_ms ?? 0)}</div>
        </div>
      </div>

      {rollup?.budget_pct_cost !== undefined ? (
        <div className="mt-4 space-y-1.5">
          <div className="flex justify-between text-[11px]">
            <span className="text-muted-foreground">Cost budget</span>
            <span
              className={cn(
                "numeric",
                rollup.budget_pct_cost > 100 && "font-medium text-destructive",
              )}
            >
              {rollup.budget_pct_cost.toFixed(0)}%
            </span>
          </div>
          <Progress value={rollup.budget_pct_cost} />
        </div>
      ) : null}

      {rollup?.budget_pct_tokens !== undefined ? (
        <div className="mt-2 space-y-1.5">
          <div className="flex justify-between text-[11px]">
            <span className="text-muted-foreground">Token budget</span>
            <span
              className={cn(
                "numeric",
                rollup.budget_pct_tokens > 100 && "font-medium text-destructive",
              )}
            >
              {rollup.budget_pct_tokens.toFixed(0)}%
            </span>
          </div>
          <Progress value={rollup.budget_pct_tokens} tone="bg-accent" />
        </div>
      ) : null}
    </motion.div>
  );
}

function CreateObjectiveDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const queryClient = useQueryClient();
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [budgetCost, setBudgetCost] = useState("");
  const [budgetTokens, setBudgetTokens] = useState("");

  const mutation = useMutation({
    mutationFn: () =>
      marbleJarApi.createObjective({
        title,
        description: description || undefined,
        budget_cost_usd: budgetCost ? Number(budgetCost) : undefined,
        budget_tokens: budgetTokens ? Number(budgetTokens) : undefined,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["objectives"] });
      setTitle("");
      setDescription("");
      setBudgetCost("");
      setBudgetTokens("");
      onClose();
    },
  });

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
      <div className="absolute inset-0 bg-black/50 backdrop-blur-sm" onClick={onClose} />
      <Card className="relative w-full max-w-md animate-fade-in">
        <CardHeader>
          <CardTitle>New objective</CardTitle>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="space-y-1">
            <Label htmlFor="obj-title">Title</Label>
            <Input
              id="obj-title"
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              placeholder="Q4 Auth Hardening"
              autoFocus
            />
          </div>
          <div className="space-y-1">
            <Label htmlFor="obj-desc">Description</Label>
            <Textarea
              id="obj-desc"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="What does done look like?"
            />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1">
              <Label htmlFor="obj-cost">Cost budget (USD)</Label>
              <Input
                id="obj-cost"
                type="number"
                step="0.01"
                value={budgetCost}
                onChange={(e) => setBudgetCost(e.target.value)}
                placeholder="optional"
              />
            </div>
            <div className="space-y-1">
              <Label htmlFor="obj-tokens">Token budget</Label>
              <Input
                id="obj-tokens"
                type="number"
                value={budgetTokens}
                onChange={(e) => setBudgetTokens(e.target.value)}
                placeholder="optional"
              />
            </div>
          </div>
          {mutation.isError ? (
            <p className="text-[11px] text-destructive">{(mutation.error as Error).message}</p>
          ) : null}
          <div className="flex justify-end gap-2 pt-1">
            <Button variant="ghost" size="sm" onClick={onClose}>
              Cancel
            </Button>
            <Button
              size="sm"
              onClick={() => mutation.mutate()}
              disabled={!title.trim() || mutation.isPending}
            >
              {mutation.isPending ? "Creating…" : "Create objective"}
            </Button>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
