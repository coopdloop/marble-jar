/** Core domain types shared by every Marble Jar client. */

export type MarbleStatus = "logged" | "completed" | "failed" | "partial" | "dispatched";

export interface Marble {
  id: string;
  organization_id: string;
  project_id: string | null;
  objective_id: string | null;
  agent_id: string | null;
  created_by_user_id: string | null;
  summary: string;
  model: string | null;
  tokens_in: number | null;
  tokens_out: number | null;
  cost_usd: number | null;
  duration_ms: number | null;
  trace_id: string | null;
  phoenix_trace_url: string | null;
  status: string;
  metadata: Record<string, unknown>;
  source: string;
  occurred_at: string;
  created_at: string;
  updated_at: string;
  project_name?: string | null;
  agent_name?: string | null;
}

export interface LogMarbleInput {
  /** Human-readable description of the completed unit of work. Required. */
  taskSummary?: string;
  summary?: string;
  /** Model that performed the work. Required. */
  model: string;
  status?: "completed" | "failed" | "partial";
  project?: string;
  agent?: string;
  agentId?: string;
  harness?: string;
  costUsd?: number;
  tokensInput?: number;
  tokensOutput?: number;
  durationMs?: number;
  traceId?: string;
  objectiveId?: string;
  occurredAt?: string;
  metadata?: Record<string, unknown>;
  /** Makes retries safe: replaying the same key returns the original marble. */
  idempotencyKey?: string;
}

export interface LogMarbleResponse {
  marble_id: string;
  accepted_at: string;
  marble: Marble;
  created: boolean;
}

export interface Rollup {
  marble_count: number;
  tokens_in: number;
  tokens_out: number;
  total_tokens: number;
  cost_usd: number;
  duration_ms: number;
  budget_pct_cost?: number;
  budget_pct_tokens?: number;
}

export interface Objective {
  id: string;
  organization_id: string;
  project_id: string | null;
  title: string;
  description: string | null;
  status: string;
  created_by_user_id: string | null;
  budget_tokens: number | null;
  budget_cost_usd: number | null;
  shipped_at: string | null;
  created_at: string;
  updated_at: string;
  rollup?: Rollup;
}

export interface DispatchRule {
  id: string;
  organization_id: string;
  project_id: string | null;
  name: string;
  condition: RuleCondition;
  target_type: IntegrationType;
  target_config: Record<string, unknown>;
  is_active: boolean;
  created_at: string;
  updated_at: string;
}

export type IntegrationType = "jira" | "slack" | "teams" | "webhook";

export type RuleOperator =
  | "eq" | "neq" | "gt" | "gte" | "lt" | "lte" | "contains" | "in" | "exists";

export type RuleField =
  | "project" | "project_id" | "model" | "agent" | "agent_id" | "status"
  | "source" | "summary" | "cost_usd" | "tokens_in" | "tokens_out"
  | "tokens_total" | "duration_ms" | "objective_id";

export interface RuleCondition {
  all?: RuleCondition[];
  any?: RuleCondition[];
  not?: RuleCondition;
  field?: RuleField;
  op?: RuleOperator;
  value?: unknown;
}

export interface Dispatch {
  id: string;
  organization_id: string;
  marble_id: string | null;
  objective_id: string | null;
  dispatch_rule_id: string | null;
  integration_type: IntegrationType;
  action: string;
  acting_user_id: string | null;
  status: "pending" | "running" | "succeeded" | "failed" | "retrying" | "dead_lettered";
  request_payload: Record<string, unknown>;
  response_payload: Record<string, unknown> | null;
  external_ref: string | null;
  error_message: string | null;
  attempt_count: number;
  dispatched_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface AuditEntry {
  id: string;
  organization_id: string;
  acting_user_id: string | null;
  agent_identity: string | null;
  provider: string;
  action: string;
  marble_id: string | null;
  dispatch_id: string | null;
  details: Record<string, unknown>;
  created_at: string;
}

export interface TrendPoint {
  bucket: string;
  marble_count: number;
  cost_usd: number;
  tokens_in: number;
  tokens_out: number;
  duration_ms: number;
}

export interface JarStatus {
  marbles_total: number;
  marbles_today: number;
  marbles_last_hour: number;
  cost_today_usd: number;
  tokens_today: number;
  open_objectives: number;
  pending_dispatches: number;
  top_models: string[];
}

export interface PaginatedResponse<T> {
  items: T[];
  next_cursor: string;
}

export interface HealthStatus {
  status: string;
  database?: string;
  ws_connections?: number;
  time?: string;
}

/** Events pushed over the live queue WebSocket. */
export interface QueueFeedEvent {
  type:
    | "connection.ready"
    | "marble.created"
    | "marble.updated"
    | "marble.deleted"
    | "objective.created"
    | "objective.updated"
    | "objective.deleted"
    | "objective.shipped"
    | "dispatch.queued"
    | "dispatch.updated";
  timestamp: string;
  payload: unknown;
  event_id?: string;
}

export type QueueFeedListener = (event: QueueFeedEvent) => void;

export interface ListMarblesFilters {
  project?: string;
  objectiveId?: string;
  model?: string;
  agentId?: string;
  status?: string;
  q?: string;
  from?: string;
  to?: string;
  unassigned?: boolean;
  limit?: number;
  cursor?: string;
}

export interface ShipObjectiveInput {
  integrationType: IntegrationType;
  action?: string;
  targetConfig?: Record<string, unknown>;
  summaryOverride?: string;
  markShipped?: boolean;
}
