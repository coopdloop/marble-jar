import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AnimatePresence, motion } from "framer-motion";
import { ExternalLink, Send, Trash2, X } from "lucide-react";
import { marbleJarApi } from "@/lib/api";
import type { IntegrationType, Marble } from "@/lib/types";
import {
  formatCost,
  formatDuration,
  formatRelative,
  formatTokens,
  marbleColor,
  statusTone,
  totalTokens,
} from "@/lib/utils";
import { Badge, Button, Input, Label, Select, Skeleton, Stat } from "@/components/ui/primitives";
import { useAuthStore } from "@/stores/useAppStore";

/**
 * Right-side drawer with full trace lineage, cost breakdown, dispatch actions
 * and the OBO identity each action will be performed as.
 */
export function MarbleSheet({
  marbleId,
  onClose,
}: {
  marbleId: string | null;
  onClose: () => void;
}) {
  return (
    <AnimatePresence>
      {marbleId ? (
        <>
          <motion.div
            className="fixed inset-0 z-40 bg-black/40 backdrop-blur-sm"
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            onClick={onClose}
          />
          <motion.aside
            className="fixed right-0 top-0 z-50 flex h-full w-full max-w-lg flex-col border-l border-border bg-card/95 backdrop-blur-2xl"
            initial={{ x: "100%" }}
            animate={{ x: 0 }}
            exit={{ x: "100%" }}
            transition={{ type: "spring", damping: 30, stiffness: 300 }}
          >
            <SheetBody marbleId={marbleId} onClose={onClose} />
          </motion.aside>
        </>
      ) : null}
    </AnimatePresence>
  );
}

function SheetBody({ marbleId, onClose }: { marbleId: string; onClose: () => void }) {
  const queryClient = useQueryClient();
  const user = useAuthStore((s) => s.user);

  const { data, isLoading } = useQuery({
    queryKey: ["marble", marbleId],
    queryFn: () => marbleJarApi.getMarble(marbleId),
  });

  const deleteMutation = useMutation({
    mutationFn: () => marbleJarApi.deleteMarble(marbleId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["marbles"] });
      onClose();
    },
  });

  if (isLoading || !data) {
    return (
      <div className="space-y-4 p-6">
        <Skeleton className="h-6 w-2/3" />
        <Skeleton className="h-20 w-full" />
        <Skeleton className="h-32 w-full" />
      </div>
    );
  }

  const { marble, dispatches } = data;

  return (
    <>
      <header className="flex items-start justify-between gap-3 border-b border-border/60 p-5">
        <div className="flex min-w-0 gap-3">
          <span
            className="mt-1 h-3 w-3 shrink-0 rounded-full ring-2 ring-white/15"
            style={{ backgroundColor: marbleColor(marble) }}
          />
          <div className="min-w-0">
            <h2 className="text-sm font-semibold leading-snug">{marble.summary}</h2>
            <p className="mt-1 text-[11px] text-muted-foreground">
              {[marble.project_name, marble.agent_name, marble.model]
                .filter(Boolean)
                .join(" · ")}{" "}
              · {formatRelative(marble.occurred_at)}
            </p>
          </div>
        </div>
        <Button variant="ghost" size="icon" onClick={onClose} aria-label="Close">
          <X className="h-4 w-4" />
        </Button>
      </header>

      <div className="flex-1 space-y-6 overflow-y-auto p-5">
        <section className="grid grid-cols-2 gap-4 sm:grid-cols-4">
          <Stat label="Cost" value={formatCost(marble.cost_usd)} />
          <Stat label="Tokens" value={formatTokens(totalTokens(marble))} hint={`${formatTokens(marble.tokens_in)} in / ${formatTokens(marble.tokens_out)} out`} />
          <Stat label="Duration" value={formatDuration(marble.duration_ms)} />
          <Stat label="Status" value={<Badge tone={statusTone(marble.status)}>{marble.status}</Badge>} />
        </section>

        <TraceLineagePanel marble={marble} />

        <section>
          <h3 className="mb-2 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
            Dispatch
          </h3>
          <p className="mb-3 text-[11px] text-muted-foreground">
            Actions are performed on behalf of{" "}
            <span className="font-medium text-foreground">{user?.email ?? "you"}</span> and recorded
            in the audit trail.
          </p>
          <DispatchActionBar marbleId={marble.id} />
        </section>

        {dispatches.length > 0 ? (
          <section>
            <h3 className="mb-2 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
              Dispatch history
            </h3>
            <div className="space-y-2">
              {dispatches.map((d) => (
                <div
                  key={d.id}
                  className="flex items-center justify-between gap-3 rounded-lg border border-border/60 bg-background/40 px-3 py-2"
                >
                  <div className="min-w-0">
                    <div className="text-xs font-medium capitalize">
                      {d.integration_type} · {d.action.replace(/_/g, " ")}
                    </div>
                    <div className="truncate text-[11px] text-muted-foreground">
                      {d.external_ref ?? d.error_message ?? `attempt ${d.attempt_count}`}
                    </div>
                  </div>
                  <Badge tone={statusTone(d.status)}>{d.status}</Badge>
                </div>
              ))}
            </div>
          </section>
        ) : null}

        {Object.keys(marble.metadata ?? {}).length > 0 ? (
          <section>
            <h3 className="mb-2 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
              Metadata
            </h3>
            <pre className="overflow-x-auto rounded-lg border border-border/60 bg-background/40 p-3 text-[11px] text-muted-foreground">
              {JSON.stringify(marble.metadata, null, 2)}
            </pre>
          </section>
        ) : null}
      </div>

      <footer className="border-t border-border/60 p-4">
        <Button
          variant="ghost"
          size="sm"
          className="text-destructive hover:bg-destructive/10"
          onClick={() => deleteMutation.mutate()}
          disabled={deleteMutation.isPending}
        >
          <Trash2 className="h-3.5 w-3.5" />
          Delete marble
        </Button>
      </footer>
    </>
  );
}

function TraceLineagePanel({ marble }: { marble: Marble }) {
  return (
    <section>
      <h3 className="mb-2 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
        Trace lineage
      </h3>
      {marble.trace_id ? (
        <div className="space-y-2 rounded-lg border border-border/60 bg-background/40 p-3">
          <div className="flex items-center justify-between gap-3">
            <span className="text-[11px] text-muted-foreground">trace_id</span>
            <code className="truncate text-[11px]">{marble.trace_id}</code>
          </div>
          {marble.phoenix_trace_url ? (
            <Button variant="outline" size="sm" className="w-full" asChild>
              <a href={marble.phoenix_trace_url} target="_blank" rel="noreferrer">
                <ExternalLink className="h-3.5 w-3.5" />
                Open in Arize Phoenix
              </a>
            </Button>
          ) : null}
        </div>
      ) : (
        <p className="rounded-lg border border-dashed border-border/60 p-3 text-[11px] text-muted-foreground">
          No trace linked. Pass a <code>trace_id</code> when logging to get full lineage.
        </p>
      )}
    </section>
  );
}

const INTEGRATIONS: { value: IntegrationType; label: string; action: string }[] = [
  { value: "jira", label: "Create Jira issue", action: "create_issue" },
  { value: "slack", label: "Post to Slack", action: "post_message" },
  { value: "teams", label: "Post to Teams", action: "post_message" },
  { value: "webhook", label: "Fire webhook", action: "post" },
];

function DispatchActionBar({ marbleId }: { marbleId: string }) {
  const queryClient = useQueryClient();
  const [integration, setIntegration] = useState<IntegrationType>("slack");
  const [target, setTarget] = useState("");

  const mutation = useMutation({
    mutationFn: () =>
      marbleJarApi.dispatchMarble(marbleId, {
        integration_type: integration,
        action: INTEGRATIONS.find((i) => i.value === integration)?.action,
        target_config: buildTargetConfig(integration, target),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["marble", marbleId] });
      queryClient.invalidateQueries({ queryKey: ["dispatches"] });
      setTarget("");
    },
  });

  return (
    <div className="space-y-2.5">
      <div className="flex gap-2">
        <Select
          value={integration}
          onChange={(e) => setIntegration(e.target.value as IntegrationType)}
          className="w-44"
        >
          {INTEGRATIONS.map((i) => (
            <option key={i.value} value={i.value}>
              {i.label}
            </option>
          ))}
        </Select>
        <Input
          value={target}
          onChange={(e) => setTarget(e.target.value)}
          placeholder={targetPlaceholder(integration)}
          className="flex-1"
        />
      </div>
      <Button
        size="sm"
        className="w-full"
        onClick={() => mutation.mutate()}
        disabled={mutation.isPending || !target.trim()}
      >
        <Send className="h-3.5 w-3.5" />
        {mutation.isPending ? "Dispatching…" : "Dispatch"}
      </Button>
      {mutation.isError ? (
        <p className="text-[11px] text-destructive">{(mutation.error as Error).message}</p>
      ) : null}
      {mutation.isSuccess ? (
        <p className="text-[11px] text-[hsl(var(--success))]">Queued for dispatch.</p>
      ) : null}
      <Label>{targetHint(integration)}</Label>
    </div>
  );
}

function buildTargetConfig(integration: IntegrationType, target: string): Record<string, unknown> {
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

function targetPlaceholder(integration: IntegrationType): string {
  switch (integration) {
    case "jira":
      return "PROJ";
    case "slack":
      return "#eng-updates or webhook URL";
    case "teams":
      return "channel id or webhook URL";
    case "webhook":
      return "https://example.com/hook";
  }
}

function targetHint(integration: IntegrationType): string {
  switch (integration) {
    case "jira":
      return "Jira project key; requires a connected Atlassian account.";
    case "slack":
      return "Channel (OBO token) or an incoming-webhook URL.";
    case "teams":
      return "Channel id (Graph OBO) or an incoming-webhook URL.";
    case "webhook":
      return "Payload is HMAC-signed with X-MarbleJar-Signature.";
  }
}
