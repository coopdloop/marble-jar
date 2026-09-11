-- Marble Jar full database migration
-- Requires pgcrypto for gen_random_uuid()
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- ==================== auth_service ====================

CREATE TABLE organizations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    slug TEXT NOT NULL,
    settings JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX idx_organizations_slug ON organizations (slug);

CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    email TEXT NOT NULL,
    display_name TEXT,
    avatar_url TEXT,
    role TEXT NOT NULL DEFAULT 'member',
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_users_organization_id ON users (organization_id);
CREATE UNIQUE INDEX idx_users_email ON users (email);

CREATE TABLE api_keys (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    created_by_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    name TEXT NOT NULL,
    key_prefix TEXT NOT NULL,
    hashed_key TEXT NOT NULL,
    scopes TEXT[] NOT NULL DEFAULT '{}',
    last_used_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_api_keys_organization_id ON api_keys (organization_id);
CREATE INDEX idx_api_keys_key_prefix ON api_keys (key_prefix);

CREATE TABLE oauth_connections (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    provider_account_id TEXT,
    access_token_encrypted TEXT NOT NULL,
    refresh_token_encrypted TEXT,
    scopes TEXT[] NOT NULL DEFAULT '{}',
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_oauth_connections_org_provider ON oauth_connections (organization_id, provider);
CREATE INDEX idx_oauth_connections_user_id ON oauth_connections (user_id);

CREATE TABLE obo_audit_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    acting_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    agent_identity TEXT,
    provider TEXT NOT NULL,
    action TEXT NOT NULL,
    marble_id UUID,
    dispatch_id UUID,
    details JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_obo_audit_log_org_id ON obo_audit_log (organization_id);
CREATE INDEX idx_obo_audit_log_marble_id ON obo_audit_log (marble_id);
CREATE INDEX idx_obo_audit_log_created_at ON obo_audit_log (created_at);

-- ==================== core_api ====================

CREATE TABLE projects (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    slug TEXT NOT NULL,
    description TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_projects_organization_id ON projects (organization_id);
CREATE UNIQUE INDEX idx_projects_org_slug ON projects (organization_id, slug);

CREATE TABLE agents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    harness TEXT,
    default_model TEXT,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_agents_organization_id ON agents (organization_id);

CREATE TABLE objectives (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id UUID REFERENCES projects(id) ON DELETE SET NULL,
    title TEXT NOT NULL,
    description TEXT,
    status TEXT NOT NULL DEFAULT 'open',
    created_by_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    budget_tokens BIGINT,
    budget_cost_usd NUMERIC(12,4),
    shipped_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_objectives_organization_id ON objectives (organization_id);
CREATE INDEX idx_objectives_project_id ON objectives (project_id);
CREATE INDEX idx_objectives_status ON objectives (status);

CREATE TABLE marbles (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id UUID REFERENCES projects(id) ON DELETE SET NULL,
    objective_id UUID REFERENCES objectives(id) ON DELETE SET NULL,
    agent_id UUID REFERENCES agents(id) ON DELETE SET NULL,
    created_by_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    summary TEXT NOT NULL,
    model TEXT,
    tokens_in INTEGER,
    tokens_out INTEGER,
    cost_usd NUMERIC(12,6),
    duration_ms INTEGER,
    trace_id TEXT,
    phoenix_trace_url TEXT,
    status TEXT NOT NULL DEFAULT 'logged',
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    source TEXT NOT NULL DEFAULT 'sdk',
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_marbles_organization_id ON marbles (organization_id);
CREATE INDEX idx_marbles_project_id ON marbles (project_id);
CREATE INDEX idx_marbles_objective_id ON marbles (objective_id);
CREATE INDEX idx_marbles_agent_id ON marbles (agent_id);
CREATE INDEX idx_marbles_occurred_at ON marbles (occurred_at);
CREATE INDEX idx_marbles_trace_id ON marbles (trace_id);

CREATE TABLE dispatch_rules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id UUID REFERENCES projects(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    condition JSONB NOT NULL DEFAULT '{}'::jsonb,
    target_type TEXT NOT NULL,
    target_config JSONB NOT NULL DEFAULT '{}'::jsonb,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_by_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_dispatch_rules_organization_id ON dispatch_rules (organization_id);
CREATE INDEX idx_dispatch_rules_project_id ON dispatch_rules (project_id);
CREATE INDEX idx_dispatch_rules_target_type ON dispatch_rules (target_type);

CREATE TABLE dispatches (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    marble_id UUID REFERENCES marbles(id) ON DELETE CASCADE,
    objective_id UUID REFERENCES objectives(id) ON DELETE CASCADE,
    dispatch_rule_id UUID REFERENCES dispatch_rules(id) ON DELETE SET NULL,
    integration_type TEXT NOT NULL,
    action TEXT NOT NULL,
    acting_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    request_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    response_payload JSONB,
    external_ref TEXT,
    error_message TEXT,
    attempt_count INTEGER NOT NULL DEFAULT 0,
    dispatched_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_dispatches_organization_id ON dispatches (organization_id);
CREATE INDEX idx_dispatches_marble_id ON dispatches (marble_id);
CREATE INDEX idx_dispatches_objective_id ON dispatches (objective_id);
CREATE INDEX idx_dispatches_status ON dispatches (status);
CREATE INDEX idx_dispatches_integration_type ON dispatches (integration_type);

-- ==================== ingestion_api ====================

CREATE TABLE marble_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    marble_id UUID REFERENCES marbles(id) ON DELETE SET NULL,
    api_key_id UUID REFERENCES api_keys(id) ON DELETE SET NULL,
    raw_payload JSONB NOT NULL,
    ingest_status TEXT NOT NULL DEFAULT 'received',
    error_message TEXT,
    kafka_offset BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_marble_events_organization_id ON marble_events (organization_id);
CREATE INDEX idx_marble_events_marble_id ON marble_events (marble_id);
CREATE INDEX idx_marble_events_ingest_status ON marble_events (ingest_status);
CREATE INDEX idx_marble_events_created_at ON marble_events (created_at);

-- ==================== jira_dispatch_worker ====================

CREATE TABLE jira_dispatch_details (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    dispatch_id UUID NOT NULL REFERENCES dispatches(id) ON DELETE CASCADE,
    jira_site_id TEXT,
    issue_key TEXT,
    issue_type TEXT,
    project_key TEXT,
    action_type TEXT NOT NULL DEFAULT 'create',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX idx_jira_dispatch_details_dispatch_id ON jira_dispatch_details (dispatch_id);
CREATE INDEX idx_jira_dispatch_details_issue_key ON jira_dispatch_details (issue_key);

-- ==================== teams_dispatch_worker ====================

CREATE TABLE teams_dispatch_details (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    dispatch_id UUID NOT NULL REFERENCES dispatches(id) ON DELETE CASCADE,
    team_id TEXT,
    channel_id TEXT,
    message_id TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX idx_teams_dispatch_details_dispatch_id ON teams_dispatch_details (dispatch_id);
CREATE INDEX idx_teams_dispatch_details_channel_id ON teams_dispatch_details (channel_id);

-- ==================== slack_dispatch_worker ====================

CREATE TABLE slack_dispatch_details (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    dispatch_id UUID NOT NULL REFERENCES dispatches(id) ON DELETE CASCADE,
    slack_team_id TEXT,
    channel_id TEXT,
    message_ts TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX idx_slack_dispatch_details_dispatch_id ON slack_dispatch_details (dispatch_id);
CREATE INDEX idx_slack_dispatch_details_channel_id ON slack_dispatch_details (channel_id);

-- ==================== webhook_dispatch_worker ====================

CREATE TABLE webhook_endpoints (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    url TEXT NOT NULL,
    secret TEXT NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_webhook_endpoints_organization_id ON webhook_endpoints (organization_id);

CREATE TABLE webhook_dispatch_details (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    dispatch_id UUID NOT NULL REFERENCES dispatches(id) ON DELETE CASCADE,
    target_url TEXT NOT NULL,
    http_status INTEGER,
    signature TEXT,
    response_body TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX idx_webhook_dispatch_details_dispatch_id ON webhook_dispatch_details (dispatch_id);

-- ==================== phoenix_bridge ====================

CREATE TABLE phoenix_trace_links (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    marble_id UUID REFERENCES marbles(id) ON DELETE CASCADE,
    phoenix_project_name TEXT,
    trace_id TEXT NOT NULL,
    span_id TEXT,
    trace_url TEXT,
    rollup_metrics JSONB NOT NULL DEFAULT '{}'::jsonb,
    synced_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_phoenix_trace_links_organization_id ON phoenix_trace_links (organization_id);
CREATE INDEX idx_phoenix_trace_links_marble_id ON phoenix_trace_links (marble_id);
CREATE INDEX idx_phoenix_trace_links_trace_id ON phoenix_trace_links (trace_id);

-- ==================== mcp_log_marble_server ====================

CREATE TABLE mcp_sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    api_key_id UUID REFERENCES api_keys(id) ON DELETE SET NULL,
    agent_id UUID REFERENCES agents(id) ON DELETE SET NULL,
    client_name TEXT,
    client_version TEXT,
    last_seen_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_mcp_sessions_organization_id ON mcp_sessions (organization_id);
CREATE INDEX idx_mcp_sessions_agent_id ON mcp_sessions (agent_id);

-- ==================== mcp_monitor_server ====================

CREATE TABLE mcp_monitor_queries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    api_key_id UUID REFERENCES api_keys(id) ON DELETE SET NULL,
    agent_id UUID REFERENCES agents(id) ON DELETE SET NULL,
    query_type TEXT NOT NULL,
    query_params JSONB NOT NULL DEFAULT '{}'::jsonb,
    result_summary JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_mcp_monitor_queries_organization_id ON mcp_monitor_queries (organization_id);
CREATE INDEX idx_mcp_monitor_queries_agent_id ON mcp_monitor_queries (agent_id);
CREATE INDEX idx_mcp_monitor_queries_created_at ON mcp_monitor_queries (created_at);
