-- Marble Jar migration 0002: Timescale rollups, idempotency, helpful constraints.

-- Idempotent ingestion support (SDK logMarble idempotencyKey).
ALTER TABLE marbles ADD COLUMN IF NOT EXISTS idempotency_key TEXT;
CREATE UNIQUE INDEX IF NOT EXISTS idx_marbles_org_idempotency
    ON marbles (organization_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

-- Password storage for the built-in auth path (Ory/Auth0 remains the production option).
ALTER TABLE users ADD COLUMN IF NOT EXISTS password_hash TEXT;

-- TimescaleDB is optional in local dev; guard everything behind extension availability.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_available_extensions WHERE name = 'timescaledb') THEN
        CREATE EXTENSION IF NOT EXISTS timescaledb;

        -- marbles is already keyed by id; Timescale requires the partition column in
        -- every unique index, so we keep marbles relational and use a companion
        -- hypertable for time-series rollups instead.
        CREATE TABLE IF NOT EXISTS marble_metrics (
            time TIMESTAMPTZ NOT NULL,
            organization_id UUID NOT NULL,
            marble_id UUID NOT NULL,
            project_id UUID,
            objective_id UUID,
            model TEXT,
            tokens_in INTEGER NOT NULL DEFAULT 0,
            tokens_out INTEGER NOT NULL DEFAULT 0,
            cost_usd NUMERIC(12,6) NOT NULL DEFAULT 0,
            duration_ms INTEGER NOT NULL DEFAULT 0
        );

        PERFORM create_hypertable('marble_metrics', 'time', if_not_exists => TRUE);

        CREATE INDEX IF NOT EXISTS idx_marble_metrics_org_time
            ON marble_metrics (organization_id, time DESC);
        CREATE INDEX IF NOT EXISTS idx_marble_metrics_objective
            ON marble_metrics (objective_id, time DESC);
    ELSE
        CREATE TABLE IF NOT EXISTS marble_metrics (
            time TIMESTAMPTZ NOT NULL,
            organization_id UUID NOT NULL,
            marble_id UUID NOT NULL,
            project_id UUID,
            objective_id UUID,
            model TEXT,
            tokens_in INTEGER NOT NULL DEFAULT 0,
            tokens_out INTEGER NOT NULL DEFAULT 0,
            cost_usd NUMERIC(12,6) NOT NULL DEFAULT 0,
            duration_ms INTEGER NOT NULL DEFAULT 0
        );
        CREATE INDEX IF NOT EXISTS idx_marble_metrics_org_time
            ON marble_metrics (organization_id, time DESC);
        CREATE INDEX IF NOT EXISTS idx_marble_metrics_objective
            ON marble_metrics (objective_id, time DESC);
    END IF;
END
$$;
