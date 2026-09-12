import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link2, Link2Off, ShieldCheck, X } from "lucide-react";
import { useSearchParams } from "react-router-dom";
import { useState } from "react";
import { marbleJarApi } from "@/lib/api";
import type { IntegrationStatus } from "@/lib/types";
import { formatRelative } from "@/lib/utils";
import {
  Badge,
  Button,
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  Skeleton,
} from "@/components/ui/primitives";

const LABELS: Record<string, { name: string; blurb: string }> = {
  jira: { name: "Jira", blurb: "Create and update issues as you." },
  slack: { name: "Slack", blurb: "Post updates to channels as you." },
  teams: { name: "Microsoft Teams", blurb: "Post channel messages as you." },
};

export function IntegrationsPage() {
  const queryClient = useQueryClient();
  const [searchParams, setSearchParams] = useSearchParams();
  const connected = searchParams.get("connected");
  const oauthError = searchParams.get("error");
  const [dismissed, setDismissed] = useState(false);

  const clearNotice = () => {
    setDismissed(true);
    setSearchParams({}, { replace: true });
  };

  const { data, isLoading } = useQuery({
    queryKey: ["integrations"],
    queryFn: () => marbleJarApi.listIntegrations(),
  });

  const { data: audit } = useQuery({
    queryKey: ["audit"],
    queryFn: () => marbleJarApi.listAudit({ limit: 50 }),
  });

  const connectMutation = useMutation({
    mutationFn: (provider: string) => marbleJarApi.connectIntegration(provider),
    onSuccess: (res) => {
      if (res.authorize_url) window.open(res.authorize_url, "_blank", "noopener");
      queryClient.invalidateQueries({ queryKey: ["integrations"] });
    },
  });

  const disconnectMutation = useMutation({
    mutationFn: (provider: string) => marbleJarApi.disconnectIntegration(provider),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["integrations"] }),
  });

  return (
    <div className="space-y-5">
      <div>
        <h1 className="text-lg font-semibold tracking-tight">Integrations</h1>
        <p className="text-xs text-muted-foreground">
          Every dispatch runs on-behalf-of a real person — never an anonymous bot.
        </p>
      </div>

      {!dismissed && (connected || oauthError) ? (
        <div
          className={
            connected
              ? "flex items-center justify-between rounded-xl border border-[hsl(var(--success))]/25 bg-[hsl(var(--success))]/10 px-4 py-2.5 text-xs text-[hsl(var(--success))]"
              : "flex items-center justify-between rounded-xl border border-destructive/25 bg-destructive/10 px-4 py-2.5 text-xs text-destructive"
          }
        >
          <span>
            {connected
              ? `${connected} connected — dispatches now act as you.`
              : `Connection failed: ${oauthError}`}
          </span>
          <button onClick={clearNotice} aria-label="Dismiss">
            <X className="h-3.5 w-3.5" />
          </button>
        </div>
      ) : null}

      {isLoading ? (
        <div className="grid gap-4 sm:grid-cols-3">
          {Array.from({ length: 3 }).map((_, i) => (
            <Skeleton key={i} className="h-36" />
          ))}
        </div>
      ) : (
        <div className="grid gap-4 sm:grid-cols-3">
          {data?.items.map((integration) => (
            <IntegrationCard
              key={integration.provider}
              integration={integration}
              onConnect={() => connectMutation.mutate(integration.provider)}
              onDisconnect={() => disconnectMutation.mutate(integration.provider)}
              pending={connectMutation.isPending || disconnectMutation.isPending}
            />
          ))}
        </div>
      )}

      {connectMutation.isError ? (
        <p className="text-xs text-[hsl(var(--warning))]">
          {(connectMutation.error as Error).message}
        </p>
      ) : null}

      <Card className="overflow-hidden">
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <ShieldCheck className="h-3.5 w-3.5" />
            Audit trail
          </CardTitle>
          <p className="text-xs text-muted-foreground">
            Which identity performed which action, and when.
          </p>
        </CardHeader>
        {(audit?.items.length ?? 0) === 0 ? (
          <CardContent>
            <p className="text-xs text-muted-foreground">No dispatch activity yet.</p>
          </CardContent>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-left text-xs">
              <thead>
                <tr className="border-b border-border/60 text-[11px] uppercase tracking-wider text-muted-foreground">
                  <th className="px-5 py-2">Action</th>
                  <th className="px-5 py-2">Provider</th>
                  <th className="px-5 py-2">Agent</th>
                  <th className="px-5 py-2">Acting user</th>
                  <th className="px-5 py-2">When</th>
                </tr>
              </thead>
              <tbody>
                {audit?.items.map((entry) => (
                  <tr key={entry.id} className="border-b border-border/40 last:border-0">
                    <td className="px-5 py-2 font-medium">{entry.action}</td>
                    <td className="px-5 py-2 capitalize text-muted-foreground">{entry.provider}</td>
                    <td className="px-5 py-2 text-muted-foreground">
                      {entry.agent_identity ?? "—"}
                    </td>
                    <td className="px-5 py-2 font-mono text-[10px] text-muted-foreground">
                      {entry.acting_user_id?.slice(0, 8) ?? "—"}
                    </td>
                    <td className="whitespace-nowrap px-5 py-2 text-muted-foreground">
                      {formatRelative(entry.created_at)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
    </div>
  );
}

function IntegrationCard({
  integration,
  onConnect,
  onDisconnect,
  pending,
}: {
  integration: IntegrationStatus;
  onConnect: () => void;
  onDisconnect: () => void;
  pending: boolean;
}) {
  const meta = LABELS[integration.provider] ?? {
    name: integration.provider,
    blurb: "",
  };

  return (
    <Card className="glass-hover flex flex-col">
      <CardHeader className="flex-1">
        <div className="flex items-start justify-between gap-2">
          <CardTitle>{meta.name}</CardTitle>
          <Badge
            tone={
              integration.connected
                ? "border-[hsl(var(--success))]/25 bg-[hsl(var(--success))]/10 text-[hsl(var(--success))]"
                : undefined
            }
          >
            {integration.connected ? "connected" : "not connected"}
          </Badge>
        </div>
        <p className="text-xs text-muted-foreground">{meta.blurb}</p>
        {integration.connected && integration.connected_at ? (
          <p className="mt-1 text-[11px] text-muted-foreground">
            since {formatRelative(integration.connected_at)}
          </p>
        ) : null}
      </CardHeader>
      <CardContent>
        {integration.connected ? (
          <Button variant="outline" size="sm" className="w-full" onClick={onDisconnect} disabled={pending}>
            <Link2Off className="h-3.5 w-3.5" />
            Disconnect
          </Button>
        ) : (
          <Button size="sm" className="w-full" onClick={onConnect} disabled={pending}>
            <Link2 className="h-3.5 w-3.5" />
            Connect
          </Button>
        )}
      </CardContent>
    </Card>
  );
}
