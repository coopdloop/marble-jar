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
  budget_tokens: number | null;
  budget_cost_usd: number | null;
  shipped_at: string | null;
  created_at: string;
  updated_at: string;
  rollup?: Rollup;
}

export type IntegrationType = "jira" | "slack" | "teams" | "webhook";

export type RuleOperator =
  | "eq" | "neq" | "gt" | "gte" | "lt" | "lte" | "contains" | "in" | "exists";

export type RuleField =
  | "project" | "model" | "agent" | "status" | "source" | "summary"
  | "cost_usd" | "tokens_in" | "tokens_out" | "tokens_total" | "duration_ms";

export interface RuleCondition {
  all?: RuleCondition[];
  any?: RuleCondition[];
  not?: RuleCondition;
  field?: RuleField;
  op?: RuleOperator;
  value?: unknown;
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

export type DispatchStatus =
  | "pending" | "running" | "succeeded" | "failed" | "retrying" | "dead_lettered";

export interface Dispatch {
  id: string;
  marble_id: string | null;
  objective_id: string | null;
  dispatch_rule_id: string | null;
  integration_type: IntegrationType;
  action: string;
  acting_user_id: string | null;
  status: DispatchStatus;
  external_ref: string | null;
  error_message: string | null;
  attempt_count: number;
  dispatched_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface AuditEntry {
  id: string;
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

export interface User {
  id: string;
  organization_id: string;
  email: string;
  display_name: string | null;
  avatar_url?: string | null;
  auth_provider?: string;
  role: string;
}

export interface Organization {
  id: string;
  name: string;
  slug: string;
}

export interface Tokens {
  access_token: string;
  refresh_token: string;
  token_type: string;
  expires_at: string;
}

export interface ApiKey {
  id: string;
  name: string;
  key_prefix: string;
  scopes: string[];
  last_used_at: string | null;
  revoked_at: string | null;
  created_at: string;
}

export interface Project {
  id: string;
  name: string;
  slug: string;
}

export interface Agent {
  id: string;
  name: string;
  harness: string | null;
  default_model: string | null;
}

export interface IntegrationStatus {
  provider: string;
  connected: boolean;
  scopes?: string[];
  expires_at?: string | null;
  connected_at?: string;
}

export interface Paginated<T> {
  items: T[];
  next_cursor: string;
}

export interface ObjectiveSummary {
  objective_id: string;
  title: string;
  text: string;
  markdown: string;
  rollup: Rollup;
  marble_count: number;
  highlights: string[];
  models_used: string[];
  budget_status: string;
}

export interface ObjectiveDetail {
  objective: Objective;
  rollup: Rollup;
  trend: TrendPoint[];
  summary_preview: ObjectiveSummary;
}

export type QueueEventType =
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

export interface QueueEvent {
  type: QueueEventType;
  timestamp: string;
  payload: unknown;
  event_id?: string;
}

export type ConnectionStatus = "connecting" | "connected" | "reconnecting" | "polling";
