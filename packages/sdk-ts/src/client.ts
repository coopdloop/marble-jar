import { HttpClient, type ClientConfig } from "./http.js";
import { ApiError } from "./errors.js";
import type {
  AuditEntry,
  Dispatch,
  DispatchRule,
  HealthStatus,
  IntegrationType,
  JarStatus,
  ListMarblesFilters,
  LogMarbleInput,
  LogMarbleResponse,
  Marble,
  Objective,
  PaginatedResponse,
  QueueFeedEvent,
  QueueFeedListener,
  RuleCondition,
  ShipObjectiveInput,
  TrendPoint,
} from "./types.js";

/**
 * IngestionClient is the hot path agents use: log a completed unit of work and
 * optionally watch the live jar feed.
 */
export class IngestionClient {
  constructor(private readonly http: HttpClient) {}

  /**
   * Logs a completed marble.
   *
   * Pass `idempotencyKey` to make retries safe — replaying the same key
   * returns the original marble instead of creating a duplicate.
   */
  async logMarble(input: LogMarbleInput): Promise<LogMarbleResponse> {
    const summary = input.taskSummary ?? input.summary;
    if (!summary?.trim()) {
      throw new ApiError("taskSummary (or summary) is required", 400);
    }
    if (!input.model?.trim()) {
      throw new ApiError("model is required", 400);
    }

    const headers: Record<string, string> = {};
    if (input.idempotencyKey) headers["Idempotency-Key"] = input.idempotencyKey;

    return this.http.request<LogMarbleResponse>("/v1/marbles", {
      method: "POST",
      headers,
      // With an idempotency key, retrying a POST is safe.
      retry: Boolean(input.idempotencyKey),
      body: {
        summary,
        model: input.model,
        status: input.status,
        project: input.project,
        agent: input.agent,
        agent_id: input.agentId,
        harness: input.harness,
        cost_usd: input.costUsd,
        tokens_input: input.tokensInput,
        tokens_output: input.tokensOutput,
        duration_ms: input.durationMs,
        trace_id: input.traceId,
        objective_id: input.objectiveId,
        occurred_at: input.occurredAt,
        metadata: input.metadata,
        idempotency_key: input.idempotencyKey,
      },
    });
  }

  /**
   * Opens the live queue WebSocket. Returns an unsubscribe function.
   * Reconnects with exponential backoff unless `autoReconnect` is false.
   */
  subscribeQueueFeed(
    listener: QueueFeedListener,
    options: {
      autoReconnect?: boolean;
      onError?: (err: Error) => void;
      WebSocketImpl?: typeof WebSocket;
    } = {},
  ): () => void {
    const autoReconnect = options.autoReconnect ?? true;
    const WS = options.WebSocketImpl ?? (globalThis as { WebSocket?: typeof WebSocket }).WebSocket;
    if (!WS) throw new Error("No WebSocket implementation available");

    let socket: WebSocket | undefined;
    let closed = false;
    let attempt = 0;
    let timer: ReturnType<typeof setTimeout> | undefined;

    const url =
      this.http.baseUrl.replace(/^http/, "ws") +
      `/v1/ws/queue?token=${encodeURIComponent(this.http.apiKey)}`;

    const connect = () => {
      if (closed) return;
      socket = new WS(url);

      socket.onopen = () => {
        attempt = 0;
      };
      socket.onmessage = (ev: MessageEvent) => {
        try {
          listener(JSON.parse(String(ev.data)) as QueueFeedEvent);
        } catch (err) {
          options.onError?.(err instanceof Error ? err : new Error(String(err)));
        }
      };
      socket.onerror = () => {
        options.onError?.(new Error("queue feed socket error"));
      };
      socket.onclose = () => {
        if (closed || !autoReconnect) return;
        attempt += 1;
        const delay = Math.min(2 ** attempt * 500, 30_000);
        timer = setTimeout(connect, delay / 2 + Math.random() * (delay / 2));
      };
    };

    connect();

    return () => {
      closed = true;
      if (timer) clearTimeout(timer);
      socket?.close();
    };
  }

  async health(): Promise<HealthStatus> {
    return this.http.request<HealthStatus>("/health");
  }
}

/** MarblesClient queries and manages individual units of work. */
export class MarblesClient {
  constructor(private readonly http: HttpClient) {}

  async list(filters: ListMarblesFilters = {}): Promise<PaginatedResponse<Marble>> {
    return this.http.request<PaginatedResponse<Marble>>("/v1/marbles", {
      query: {
        project: filters.project,
        objective_id: filters.objectiveId,
        model: filters.model,
        agent_id: filters.agentId,
        status: filters.status,
        q: filters.q,
        from: filters.from,
        to: filters.to,
        unassigned: filters.unassigned,
        limit: filters.limit,
        cursor: filters.cursor,
      },
    });
  }

  async get(marbleId: string): Promise<{ marble: Marble; dispatches: Dispatch[] }> {
    return this.http.request(`/v1/marbles/${encodeURIComponent(marbleId)}`);
  }

  async update(
    marbleId: string,
    input: {
      summary?: string;
      status?: string;
      objectiveId?: string;
      clearObjective?: boolean;
      metadata?: Record<string, unknown>;
    },
  ): Promise<Marble> {
    return this.http.request<Marble>(`/v1/marbles/${encodeURIComponent(marbleId)}`, {
      method: "PATCH",
      body: {
        summary: input.summary,
        status: input.status,
        objective_id: input.objectiveId,
        clear_objective: input.clearObjective,
        metadata: input.metadata,
      },
    });
  }

  async delete(marbleId: string): Promise<void> {
    await this.http.request<void>(`/v1/marbles/${encodeURIComponent(marbleId)}`, {
      method: "DELETE",
    });
  }

  /** Internal: used by phoenix_bridge to attach trace rollups. */
  async writeRollup(
    marbleId: string,
    input: {
      tokensIn?: number;
      tokensOut?: number;
      costUsd?: number;
      durationMs?: number;
      traceId?: string;
      phoenixTraceUrl?: string;
      phoenixProjectName?: string;
      spanId?: string;
      rollupMetrics?: Record<string, unknown>;
    },
  ): Promise<Marble> {
    return this.http.request<Marble>(`/v1/marbles/${encodeURIComponent(marbleId)}/rollups`, {
      method: "POST",
      body: {
        tokens_in: input.tokensIn,
        tokens_out: input.tokensOut,
        cost_usd: input.costUsd,
        duration_ms: input.durationMs,
        trace_id: input.traceId,
        phoenix_trace_url: input.phoenixTraceUrl,
        phoenix_project_name: input.phoenixProjectName,
        span_id: input.spanId,
        rollup_metrics: input.rollupMetrics,
      },
    });
  }

  /** Dispatches a marble to an integration on behalf of the caller. */
  async dispatch(
    marbleId: string,
    input: {
      integrationType: IntegrationType;
      action?: string;
      targetConfig?: Record<string, unknown>;
      payload?: Record<string, unknown>;
    },
  ): Promise<Dispatch> {
    return this.http.request<Dispatch>(`/v1/marbles/${encodeURIComponent(marbleId)}/dispatch`, {
      method: "POST",
      body: {
        integration_type: input.integrationType,
        action: input.action,
        target_config: input.targetConfig,
        payload: input.payload,
      },
    });
  }
}

/** ObjectivesClient manages "bigger jars" and the Ship It flow. */
export class ObjectivesClient {
  constructor(private readonly http: HttpClient) {}

  async list(filters: { status?: string; project?: string; limit?: number; cursor?: string } = {}) {
    return this.http.request<PaginatedResponse<Objective>>("/v1/objectives", { query: filters });
  }

  async create(input: {
    title: string;
    description?: string;
    projectId?: string;
    budgetTokens?: number;
    budgetCostUsd?: number;
  }): Promise<Objective> {
    return this.http.request<Objective>("/v1/objectives", {
      method: "POST",
      body: {
        title: input.title,
        description: input.description,
        project_id: input.projectId,
        budget_tokens: input.budgetTokens,
        budget_cost_usd: input.budgetCostUsd,
      },
    });
  }

  async get(objectiveId: string) {
    return this.http.request<{
      objective: Objective;
      rollup: Objective["rollup"];
      trend: TrendPoint[];
      summary_preview: { text: string; markdown: string; highlights: string[]; models_used: string[] };
    }>(`/v1/objectives/${encodeURIComponent(objectiveId)}`);
  }

  async update(
    objectiveId: string,
    input: {
      title?: string;
      description?: string;
      status?: string;
      budgetTokens?: number;
      budgetCostUsd?: number;
    },
  ): Promise<Objective> {
    return this.http.request<Objective>(`/v1/objectives/${encodeURIComponent(objectiveId)}`, {
      method: "PATCH",
      body: {
        title: input.title,
        description: input.description,
        status: input.status,
        budget_tokens: input.budgetTokens,
        budget_cost_usd: input.budgetCostUsd,
      },
    });
  }

  async delete(objectiveId: string): Promise<void> {
    await this.http.request<void>(`/v1/objectives/${encodeURIComponent(objectiveId)}`, {
      method: "DELETE",
    });
  }

  async listMarbles(objectiveId: string, filters: { limit?: number; cursor?: string } = {}) {
    return this.http.request<PaginatedResponse<Marble>>(
      `/v1/objectives/${encodeURIComponent(objectiveId)}/marbles`,
      { query: filters },
    );
  }

  async addMarble(objectiveId: string, marbleId: string): Promise<Marble> {
    return this.http.request<Marble>(
      `/v1/objectives/${encodeURIComponent(objectiveId)}/marbles/${encodeURIComponent(marbleId)}`,
      { method: "POST" },
    );
  }

  async removeMarble(objectiveId: string, marbleId: string): Promise<Marble> {
    return this.http.request<Marble>(
      `/v1/objectives/${encodeURIComponent(objectiveId)}/marbles/${encodeURIComponent(marbleId)}`,
      { method: "DELETE" },
    );
  }

  /** Ship It: posts an aggregate summary of the objective's work. */
  async ship(objectiveId: string, input: ShipObjectiveInput) {
    return this.http.request<{ dispatch: Dispatch; summary: unknown; objective: Objective }>(
      `/v1/objectives/${encodeURIComponent(objectiveId)}/ship`,
      {
        method: "POST",
        body: {
          integration_type: input.integrationType,
          action: input.action,
          target_config: input.targetConfig,
          summary_override: input.summaryOverride,
          mark_shipped: input.markShipped,
        },
      },
    );
  }
}

/** RulesClient manages automated dispatch rules. */
export class RulesClient {
  constructor(private readonly http: HttpClient) {}

  async list(activeOnly = false) {
    return this.http.request<{ items: DispatchRule[] }>("/v1/rules", {
      query: { active: activeOnly || undefined },
    });
  }

  async create(input: {
    name: string;
    condition: RuleCondition;
    targetType: IntegrationType;
    targetConfig?: Record<string, unknown>;
    projectId?: string;
    isActive?: boolean;
  }): Promise<DispatchRule> {
    return this.http.request<DispatchRule>("/v1/rules", {
      method: "POST",
      body: {
        name: input.name,
        condition: input.condition,
        target_type: input.targetType,
        target_config: input.targetConfig,
        project_id: input.projectId,
        is_active: input.isActive,
      },
    });
  }

  async get(ruleId: string): Promise<DispatchRule> {
    return this.http.request<DispatchRule>(`/v1/rules/${encodeURIComponent(ruleId)}`);
  }

  async update(
    ruleId: string,
    input: {
      name?: string;
      condition?: RuleCondition;
      targetType?: IntegrationType;
      targetConfig?: Record<string, unknown>;
      isActive?: boolean;
    },
  ): Promise<DispatchRule> {
    return this.http.request<DispatchRule>(`/v1/rules/${encodeURIComponent(ruleId)}`, {
      method: "PATCH",
      body: {
        name: input.name,
        condition: input.condition,
        target_type: input.targetType,
        target_config: input.targetConfig,
        is_active: input.isActive,
      },
    });
  }

  async delete(ruleId: string): Promise<void> {
    await this.http.request<void>(`/v1/rules/${encodeURIComponent(ruleId)}`, { method: "DELETE" });
  }

  /** Previews which recent marbles a condition would have matched. */
  async test(condition: RuleCondition, sample = 20) {
    return this.http.request<{
      sample_size: number;
      matched_count: number;
      results: { marble: Marble; matched: boolean }[];
    }>("/v1/rules/test", { method: "POST", body: { condition, sample } });
  }
}

/** MonitorClient backs the marble-jar-monitor MCP tools. */
export class MonitorClient {
  constructor(private readonly http: HttpClient) {}

  async jarStatus(): Promise<JarStatus> {
    return this.http.request<JarStatus>("/v1/jar/status");
  }

  async costTrends(filters: { interval?: string; days?: number; objectiveId?: string } = {}) {
    return this.http.request<{ points: TrendPoint[] }>("/v1/trends/cost", {
      query: {
        interval: filters.interval,
        days: filters.days,
        objective_id: filters.objectiveId,
      },
    });
  }

  async tokenTrends(filters: { interval?: string; days?: number; objectiveId?: string } = {}) {
    return this.http.request<{ points: TrendPoint[] }>("/v1/trends/tokens", {
      query: {
        interval: filters.interval,
        days: filters.days,
        objective_id: filters.objectiveId,
      },
    });
  }

  /**
   * `status` accepts one status or several — the API matches any of them, e.g.
   * `["failed", "dead_lettered"]` for a stuck-work view.
   */
  async dispatches(filters: { status?: string | string[]; limit?: number } = {}) {
    return this.http.request<PaginatedResponse<Dispatch>>("/v1/dispatches", { query: filters });
  }

  async auditLog(filters: { provider?: string; limit?: number; cursor?: string } = {}) {
    return this.http.request<PaginatedResponse<AuditEntry>>("/v1/audit-log", { query: filters });
  }
}

/**
 * MarbleJar is the single entry point.
 *
 * ```ts
 * const jar = new MarbleJar({ apiKey: process.env.MARBLE_JAR_API_KEY });
 * await jar.ingestion.logMarble({ taskSummary: "Fixed the flaky test", model: "claude-sonnet-4" });
 * ```
 */
export class MarbleJar {
  readonly http: HttpClient;
  readonly ingestion: IngestionClient;
  readonly marbles: MarblesClient;
  readonly objectives: ObjectivesClient;
  readonly rules: RulesClient;
  readonly monitor: MonitorClient;

  constructor(config: ClientConfig = {}) {
    this.http = new HttpClient(config);
    this.ingestion = new IngestionClient(this.http);
    this.marbles = new MarblesClient(this.http);
    this.objectives = new ObjectivesClient(this.http);
    this.rules = new RulesClient(this.http);
    this.monitor = new MonitorClient(this.http);
  }

  /** Convenience shortcut for the most common call. */
  logMarble(input: LogMarbleInput): Promise<LogMarbleResponse> {
    return this.ingestion.logMarble(input);
  }
}
