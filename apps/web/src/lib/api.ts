import type {
  Agent,
  ApiKey,
  AuditEntry,
  Dispatch,
  DispatchRule,
  IntegrationStatus,
  IntegrationType,
  JarStatus,
  Marble,
  Objective,
  ObjectiveDetail,
  Organization,
  Paginated,
  Project,
  RuleCondition,
  Tokens,
  TrendPoint,
  User,
} from "./types";

export const API_BASE_URL =
  (import.meta.env.VITE_API_BASE_URL as string | undefined)?.replace(/\/+$/, "") ??
  "http://localhost:8080";

const TOKEN_KEY = "marblejar.tokens";

export function loadTokens(): Tokens | null {
  try {
    const raw = localStorage.getItem(TOKEN_KEY);
    return raw ? (JSON.parse(raw) as Tokens) : null;
  } catch {
    return null;
  }
}

export function saveTokens(tokens: Tokens): void {
  localStorage.setItem(TOKEN_KEY, JSON.stringify(tokens));
}

export function clearTokens(): void {
  localStorage.removeItem(TOKEN_KEY);
}

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

let refreshInFlight: Promise<Tokens | null> | null = null;

/** Refreshes the access token, coalescing concurrent 401s into one request. */
async function refreshTokens(): Promise<Tokens | null> {
  if (refreshInFlight) return refreshInFlight;

  const current = loadTokens();
  if (!current?.refresh_token) return null;

  refreshInFlight = (async () => {
    try {
      const res = await fetch(`${API_BASE_URL}/v1/token/refresh`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ refresh_token: current.refresh_token }),
      });
      if (!res.ok) {
        clearTokens();
        return null;
      }
      const tokens = (await res.json()) as Tokens;
      saveTokens(tokens);
      return tokens;
    } catch {
      return null;
    } finally {
      refreshInFlight = null;
    }
  })();

  return refreshInFlight;
}

interface RequestOptions {
  method?: string;
  body?: unknown;
  query?: Record<string, unknown>;
  headers?: Record<string, string>;
  skipAuth?: boolean;
  /** Internal: prevents infinite refresh recursion. */
  _retried?: boolean;
}

export async function api<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  const url = new URL(API_BASE_URL + path);
  for (const [k, v] of Object.entries(opts.query ?? {})) {
    if (v === undefined || v === null || v === "" || v === false) continue;
    url.searchParams.set(k, String(v));
  }

  const headers: Record<string, string> = { Accept: "application/json", ...opts.headers };
  if (opts.body !== undefined) headers["Content-Type"] = "application/json";

  if (!opts.skipAuth) {
    const tokens = loadTokens();
    if (tokens?.access_token) headers["Authorization"] = `Bearer ${tokens.access_token}`;
  }

  const res = await fetch(url.toString(), {
    method: opts.method ?? "GET",
    headers,
    body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
  });

  // Transparently refresh once on expiry, then replay the request.
  if (res.status === 401 && !opts.skipAuth && !opts._retried) {
    const refreshed = await refreshTokens();
    if (refreshed) return api<T>(path, { ...opts, _retried: true });
    clearTokens();
    window.dispatchEvent(new CustomEvent("marblejar:unauthorized"));
  }

  if (res.status === 204) return undefined as T;

  const text = await res.text();
  const data = text ? safeParse(text) : undefined;

  if (!res.ok) {
    const message =
      data && typeof data === "object" && "error" in data
        ? String((data as { error: unknown }).error)
        : `Request failed (${res.status})`;
    throw new ApiError(message, res.status);
  }

  return data as T;
}

function safeParse(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
}

/** Upper bound for cursor-walking bulk reads such as CSV export. */
const MAX_EXPORT_MARBLES = 2000;

/** Typed endpoint surface consumed by TanStack Query hooks. */
export const marbleJarApi = {
  // auth — Google sign-in is the only human credential; passwords are gone.
  authConfig: () =>
    api<{ methods: { google: boolean } }>("/v1/auth/config", { skipAuth: true }),

  /** A browser redirect, not a fetch: where sign-in starts. */
  googleSignInUrl: () => `${API_BASE_URL}/v1/auth/google/start`,

  /** Admin-only: a link that signs its visitor into this workspace. */
  createGoogleInvite: () =>
    api<{ join_url: string; expires_at: string }>("/v1/auth/google/invite", {
      method: "POST",
    }),

  /** Redeems the single-use code the Google callback left in the URL. */
  exchangeLoginCode: (loginCode: string) =>
    api<{ user: User; organization: Organization; tokens: Tokens }>("/v1/auth/exchange", {
      method: "POST",
      body: { login_code: loginCode },
      skipAuth: true,
    }),

  me: () => api<{ user?: User; organization: Organization; principal: unknown }>("/v1/me"),

  // marbles
  createMarble: (body: Record<string, unknown>, idempotencyKey?: string) =>
    api<{ marble: Marble; marble_id: string }>("/v1/marbles", {
      method: "POST",
      body,
      headers: idempotencyKey ? { "Idempotency-Key": idempotencyKey } : undefined,
    }),

  listMarbles: (params: Record<string, unknown> = {}) =>
    api<Paginated<Marble>>("/v1/marbles", { query: params }),

  /**
   * Walks the cursor pages for a filter set so an export covers every match,
   * not just the page currently on screen. Capped to keep a stray filter from
   * turning into an unbounded download.
   */
  listAllMarbles: async (params: Record<string, unknown> = {}, max = MAX_EXPORT_MARBLES) => {
    const limit = 200;
    const items: Marble[] = [];
    let cursor: string | undefined;
    for (;;) {
      const page = await api<Paginated<Marble>>("/v1/marbles", {
        query: { ...params, limit, cursor },
      });
      items.push(...page.items);
      if (!page.next_cursor || page.items.length === 0 || items.length >= max) break;
      cursor = page.next_cursor;
    }
    return items.slice(0, max);
  },

  getMarble: (id: string) =>
    api<{ marble: Marble; dispatches: Dispatch[] }>(`/v1/marbles/${id}`),

  updateMarble: (id: string, body: Record<string, unknown>) =>
    api<Marble>(`/v1/marbles/${id}`, { method: "PATCH", body }),

  deleteMarble: (id: string) => api<void>(`/v1/marbles/${id}`, { method: "DELETE" }),

  dispatchMarble: (
    id: string,
    body: {
      integration_type: IntegrationType;
      action?: string;
      target_config?: Record<string, unknown>;
    },
  ) => api<Dispatch>(`/v1/marbles/${id}/dispatch`, { method: "POST", body }),

  // objectives
  listObjectives: (params: Record<string, unknown> = {}) =>
    api<Paginated<Objective>>("/v1/objectives", { query: params }),

  getObjective: (id: string) => api<ObjectiveDetail>(`/v1/objectives/${id}`),

  createObjective: (body: Record<string, unknown>) =>
    api<Objective>("/v1/objectives", { method: "POST", body }),

  updateObjective: (id: string, body: Record<string, unknown>) =>
    api<Objective>(`/v1/objectives/${id}`, { method: "PATCH", body }),

  deleteObjective: (id: string) => api<void>(`/v1/objectives/${id}`, { method: "DELETE" }),

  listObjectiveMarbles: (id: string, params: Record<string, unknown> = {}) =>
    api<Paginated<Marble>>(`/v1/objectives/${id}/marbles`, { query: params }),

  addMarbleToObjective: (objectiveId: string, marbleId: string) =>
    api<Marble>(`/v1/objectives/${objectiveId}/marbles/${marbleId}`, { method: "POST" }),

  removeMarbleFromObjective: (objectiveId: string, marbleId: string) =>
    api<Marble>(`/v1/objectives/${objectiveId}/marbles/${marbleId}`, { method: "DELETE" }),

  shipObjective: (
    id: string,
    body: {
      integration_type: IntegrationType;
      target_config?: Record<string, unknown>;
      summary_override?: string;
      mark_shipped?: boolean;
    },
  ) => api<{ dispatch: Dispatch; objective: Objective }>(`/v1/objectives/${id}/ship`, {
    method: "POST",
    body,
  }),

  // rules
  listRules: () => api<{ items: DispatchRule[] }>("/v1/rules"),

  createRule: (body: Record<string, unknown>) =>
    api<DispatchRule>("/v1/rules", { method: "POST", body }),

  updateRule: (id: string, body: Record<string, unknown>) =>
    api<DispatchRule>(`/v1/rules/${id}`, { method: "PATCH", body }),

  deleteRule: (id: string) => api<void>(`/v1/rules/${id}`, { method: "DELETE" }),

  testRule: (condition: RuleCondition, sample = 20) =>
    api<{
      sample_size: number;
      matched_count: number;
      results: { marble: Marble; matched: boolean }[];
    }>("/v1/rules/test", { method: "POST", body: { condition, sample } }),

  // dispatches + audit
  // Pass `status` as a comma-separated list to match several states at once.
  listDispatches: (params: Record<string, unknown> = {}) =>
    api<Paginated<Dispatch>>("/v1/dispatches", { query: params }),

  getDispatch: (id: string) => api<Dispatch>(`/v1/dispatches/${id}`),

  replayDispatch: (id: string) =>
    api<Dispatch>(`/v1/dispatches/${id}/replay`, { method: "POST" }),

  listAudit: (params: Record<string, unknown> = {}) =>
    api<Paginated<AuditEntry>>("/v1/audit-log", { query: params }),

  getAuditEntry: (id: string) => api<AuditEntry>(`/v1/audit-log/${id}`),

  // analytics
  jarStatus: () => api<JarStatus>("/v1/jar/status"),

  costTrends: (params: Record<string, unknown> = {}) =>
    api<{ points: TrendPoint[] }>("/v1/trends/cost", { query: params }),

  tokenTrends: (params: Record<string, unknown> = {}) =>
    api<{ points: TrendPoint[] }>("/v1/trends/tokens", { query: params }),

  // workspace
  listProjects: () => api<{ items: Project[] }>("/v1/projects"),
  listAgents: () => api<{ items: Agent[] }>("/v1/agents"),

  listApiKeys: () => api<{ items: ApiKey[] }>("/v1/api-keys"),
  createApiKey: (name: string, scopes?: string[]) =>
    api<{ api_key: ApiKey; key: string }>("/v1/api-keys", {
      method: "POST",
      body: { name, scopes },
    }),
  revokeApiKey: (id: string) => api<void>(`/v1/api-keys/${id}`, { method: "DELETE" }),

  listIntegrations: () => api<{ items: IntegrationStatus[] }>("/v1/integrations"),
  connectIntegration: (provider: string) =>
    api<{ authorize_url: string; state: string }>(`/v1/integrations/${provider}/connect`, {
      method: "POST",
    }),
  disconnectIntegration: (provider: string) =>
    api<void>(`/v1/integrations/${provider}/connection`, { method: "DELETE" }),
};
