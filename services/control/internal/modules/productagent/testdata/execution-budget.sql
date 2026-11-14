-- Candidate AGT-112 schema for isolated repository acceptance only.
-- Production requires an owned forward migration and restore-catalogue registration.
CREATE TABLE product_agent_budget_installations (
    window_start timestamptz NOT NULL,
    window_end timestamptz NOT NULL,
    executions bigint NOT NULL DEFAULT 0 CHECK (executions >= 0),
    tokens bigint NOT NULL DEFAULT 0 CHECK (tokens >= 0),
    PRIMARY KEY (window_start, window_end),
    CHECK (window_start < window_end)
);

CREATE TABLE product_agent_budget_users (
    user_id uuid NOT NULL,
    window_start timestamptz NOT NULL,
    window_end timestamptz NOT NULL,
    executions bigint NOT NULL DEFAULT 0 CHECK (executions >= 0),
    tokens bigint NOT NULL DEFAULT 0 CHECK (tokens >= 0),
    PRIMARY KEY (user_id, window_start, window_end),
    CHECK (window_start < window_end)
);

CREATE TABLE product_agent_budget_reservations (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL,
    user_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    audience text NOT NULL CHECK (audience IN ('admin', 'portal')),
    execution_sha256 text NOT NULL CHECK (execution_sha256 ~ '^[a-f0-9]{64}$'),
    maximum_tokens bigint NOT NULL CHECK (maximum_tokens >= 0),
    window_start timestamptz NOT NULL,
    window_end timestamptz NOT NULL,
    state text NOT NULL CHECK (state IN ('held', 'settled')),
    actual_tokens bigint,
    receipt_sha256 text,
    CHECK (window_start < window_end),
    CHECK (
        (state = 'held' AND actual_tokens IS NULL AND receipt_sha256 IS NULL)
        OR
        (state = 'settled' AND actual_tokens IS NOT NULL AND actual_tokens >= 0
            AND actual_tokens <= maximum_tokens AND receipt_sha256 IS NOT NULL
            AND receipt_sha256 ~ '^[a-f0-9]{64}$')
    )
);

CREATE TABLE product_agent_runs (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL,
    user_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    audience text NOT NULL CHECK (audience IN ('admin', 'portal')),
    intent_sha256 text NOT NULL CHECK (intent_sha256 ~ '^[a-f0-9]{64}$'),
    maximum_steps bigint NOT NULL CHECK (maximum_steps > 0),
    admitted_steps bigint NOT NULL DEFAULT 0 CHECK (admitted_steps >= 0 AND admitted_steps <= maximum_steps),
    state text NOT NULL DEFAULT 'active' CHECK (state IN ('active', 'stopped')),
    active_execution_id uuid
);

CREATE TABLE product_agent_steps (
    id uuid PRIMARY KEY REFERENCES product_agent_budget_reservations(id),
    run_id uuid NOT NULL REFERENCES product_agent_runs(id),
    ordinal bigint NOT NULL CHECK (ordinal > 0),
    command_id text NOT NULL CHECK (command_id ~ '^[a-z][a-z0-9_]{0,119}$'),
    execution_sha256 text NOT NULL CHECK (execution_sha256 ~ '^[a-f0-9]{64}$'),
    state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'completed', 'failed', 'suppressed')),
    UNIQUE(run_id, ordinal)
);
