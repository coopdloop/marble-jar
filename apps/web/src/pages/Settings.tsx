import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, Copy, KeyRound, Plus, Trash2 } from "lucide-react";
import { API_BASE_URL, marbleJarApi } from "@/lib/api";
import { formatRelative } from "@/lib/utils";
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
import { useAuthStore, useUiStore } from "@/stores/useAppStore";

export function SettingsPage() {
  const { user, organization } = useAuthStore();
  const { theme, toggleTheme } = useUiStore();

  return (
    <div className="space-y-5">
      <div>
        <h1 className="text-lg font-semibold tracking-tight">Settings</h1>
        <p className="text-xs text-muted-foreground">
          {organization?.name} · {user?.email}
        </p>
      </div>

      <TokenManager />
      <OnboardingSnippet />

      <Card>
        <CardHeader>
          <CardTitle>Appearance</CardTitle>
        </CardHeader>
        <CardContent className="flex items-center justify-between">
          <p className="text-xs text-muted-foreground">
            Dark mode is designed for a second monitor or a bullpen TV.
          </p>
          <Button variant="outline" size="sm" onClick={toggleTheme}>
            {theme === "dark" ? "Switch to light" : "Switch to dark"}
          </Button>
        </CardContent>
      </Card>
    </div>
  );
}

const SCOPE_PRESETS: Record<string, string[]> = {
  "Agent (log only)": ["marbles:write", "marbles:read", "jar:read"],
  "Agent + dispatch": ["marbles:write", "marbles:read", "jar:read", "dispatch:write"],
  "Service (full)": [
    "marbles:write",
    "marbles:read",
    "jar:read",
    "dispatch:write",
    "objectives:write",
    "rules:write",
    "rollups:write",
  ],
};

function TokenManager() {
  const queryClient = useQueryClient();
  const [name, setName] = useState("");
  const [preset, setPreset] = useState("Agent (log only)");
  const [freshKey, setFreshKey] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);

  const { data, isLoading } = useQuery({
    queryKey: ["api-keys"],
    queryFn: () => marbleJarApi.listApiKeys(),
  });

  const createMutation = useMutation({
    mutationFn: () => marbleJarApi.createApiKey(name, SCOPE_PRESETS[preset]),
    onSuccess: (res) => {
      setFreshKey(res.key);
      setName("");
      queryClient.invalidateQueries({ queryKey: ["api-keys"] });
    },
  });

  const revokeMutation = useMutation({
    mutationFn: (id: string) => marbleJarApi.revokeApiKey(id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["api-keys"] }),
  });

  const copy = (text: string) => {
    navigator.clipboard.writeText(text);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <KeyRound className="h-3.5 w-3.5" />
          API keys
        </CardTitle>
        <p className="text-xs text-muted-foreground">
          Keys inherit your identity, so agent dispatches stay attributable to you.
        </p>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="flex flex-wrap gap-2">
          <Input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="claude-code on my laptop"
            className="min-w-[200px] flex-1"
          />
          <Select value={preset} onChange={(e) => setPreset(e.target.value)}>
            {Object.keys(SCOPE_PRESETS).map((p) => (
              <option key={p} value={p}>
                {p}
              </option>
            ))}
          </Select>
          <Button
            size="sm"
            onClick={() => createMutation.mutate()}
            disabled={!name.trim() || createMutation.isPending}
          >
            <Plus className="h-3.5 w-3.5" />
            Create
          </Button>
        </div>

        {createMutation.isError ? (
          <p className="text-[11px] text-destructive">{(createMutation.error as Error).message}</p>
        ) : null}

        {freshKey ? (
          <div className="space-y-2 rounded-lg border border-[hsl(var(--warning))]/30 bg-[hsl(var(--warning))]/5 p-3">
            <p className="text-[11px] font-medium text-[hsl(var(--warning))]">
              Copy this key now — it is shown only once.
            </p>
            <div className="flex gap-2">
              <code className="flex-1 overflow-x-auto rounded bg-background/60 px-2 py-1.5 text-[11px]">
                {freshKey}
              </code>
              <Button variant="outline" size="sm" onClick={() => copy(freshKey)}>
                {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
              </Button>
            </div>
          </div>
        ) : null}

        {isLoading ? (
          <Skeleton className="h-20 w-full" />
        ) : (
          <div className="divide-y divide-border/40">
            {data?.items.map((key) => (
              <div key={key.id} className="flex items-center gap-3 py-2.5">
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <span className="truncate text-sm font-medium">{key.name}</span>
                    {key.revoked_at ? <Badge>revoked</Badge> : null}
                  </div>
                  <div className="text-[11px] text-muted-foreground">
                    <code>{key.key_prefix}…</code> · {key.scopes.join(", ")} ·{" "}
                    {key.last_used_at ? `used ${formatRelative(key.last_used_at)}` : "never used"}
                  </div>
                </div>
                {!key.revoked_at ? (
                  <Button
                    variant="ghost"
                    size="icon"
                    className="text-muted-foreground hover:text-destructive"
                    onClick={() => revokeMutation.mutate(key.id)}
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </Button>
                ) : null}
              </div>
            ))}
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function OnboardingSnippet() {
  const [lang, setLang] = useState<"agents" | "ts" | "python" | "curl" | "mcp">("agents");
  const [copied, setCopied] = useState(false);

  const snippet = SNIPPETS[lang];

  return (
    <Card>
      <CardHeader className="flex-row items-start justify-between gap-3">
        <div>
          <CardTitle>Connect an agent</CardTitle>
          <p className="text-xs text-muted-foreground">
            Paste this where your agents will find it.
          </p>
        </div>
        <div className="flex gap-2">
          <Select value={lang} onChange={(e) => setLang(e.target.value as typeof lang)}>
            <option value="agents">agents.md</option>
            <option value="mcp">MCP config</option>
            <option value="ts">TypeScript</option>
            <option value="python">Python</option>
            <option value="curl">curl</option>
          </Select>
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              navigator.clipboard.writeText(snippet);
              setCopied(true);
              setTimeout(() => setCopied(false), 1500);
            }}
          >
            {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
          </Button>
        </div>
      </CardHeader>
      <CardContent>
        <pre className="overflow-x-auto rounded-lg border border-border/60 bg-background/50 p-3 text-[11px] leading-relaxed">
          {snippet}
        </pre>
        <Label className="mt-2 block">
          API base: <code>{API_BASE_URL}</code>
        </Label>
      </CardContent>
    </Card>
  );
}

const SNIPPETS: Record<string, string> = {
  agents: `## Reporting completed work

This project uses Marble Jar. When you finish a discrete unit of work,
call the \`log_marble\` MCP tool (or POST to the API) so the team sees it:

    log_marble(
      summary="<what you accomplished>",
      model="<model you used>",
      project="<repo slug>",
      cost_usd=<cost>, tokens_in=<in>, tokens_out=<out>,
    )`,

  mcp: `{
  "mcpServers": {
    "marble-jar": {
      "command": "marble-jar-mcp",
      "args": ["--server", "log"],
      "env": {
        "CORE_API_BASE_URL": "${API_BASE_URL}",
        "CORE_SERVICE_TOKEN": "mj_your_key_here"
      }
    },
    "marble-jar-monitor": {
      "command": "marble-jar-mcp",
      "args": ["--server", "monitor"],
      "env": {
        "CORE_API_BASE_URL": "${API_BASE_URL}",
        "CORE_SERVICE_TOKEN": "mj_your_key_here"
      }
    }
  }
}`,

  ts: `import { MarbleJar } from "@marble-jar/client";

const jar = new MarbleJar({
  baseUrl: "${API_BASE_URL}",
  apiKey: process.env.MARBLE_JAR_API_KEY,
});

await jar.logMarble({
  taskSummary: "Refactored the auth module",
  model: "claude-sonnet-4",
  project: "payments-api",
  costUsd: 0.42,
  tokensInput: 12000,
  tokensOutput: 3000,
  durationMs: 45000,
});`,

  python: `import httpx, os

httpx.post(
    "${API_BASE_URL}/v1/marbles",
    headers={"Authorization": f"Bearer {os.environ['MARBLE_JAR_API_KEY']}"},
    json={
        "summary": "Refactored the auth module",
        "model": "claude-sonnet-4",
        "project": "payments-api",
        "cost_usd": 0.42,
        "tokens": {"in": 12000, "out": 3000},
        "duration_ms": 45000,
    },
)`,

  curl: `curl -X POST ${API_BASE_URL}/v1/marbles \\
  -H "Authorization: Bearer $MARBLE_JAR_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "summary": "Refactored the auth module",
    "model": "claude-sonnet-4",
    "project": "payments-api",
    "cost_usd": 0.42,
    "tokens": {"in": 12000, "out": 3000}
  }'`,
};
