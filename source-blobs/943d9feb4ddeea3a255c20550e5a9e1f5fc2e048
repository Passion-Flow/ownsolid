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
