CREATE TABLE ownsolid_alert_candidate (
    tenant_id text NOT NULL,
    workspace_id text NOT NULL,
    occurrence_id text NOT NULL CHECK (occurrence_id ~ '^[a-f0-9]{64}$'),
    fingerprint text NOT NULL,
    started_at timestamptz NOT NULL,
    observed_at timestamptz,
    due_at timestamptz,
    ack_actor text,
    ack_at timestamptz,
    resolved_at timestamptz,
    attempt_no integer NOT NULL DEFAULT 0 CHECK (attempt_no >= 0),
    lease_until timestamptz,
    sent_at timestamptz,
    rejected_at timestamptz,
    PRIMARY KEY (tenant_id, workspace_id, occurrence_id),
    CHECK ((observed_at IS NULL) = (due_at IS NULL)),
    CHECK ((ack_actor IS NULL) = (ack_at IS NULL)),
    CHECK (observed_at IS NOT NULL OR resolved_at IS NOT NULL)
);

CREATE TABLE ownsolid_alert_attempt_candidate (
    tenant_id text NOT NULL,
    workspace_id text NOT NULL,
    occurrence_id text NOT NULL,
    attempt_no integer NOT NULL CHECK (attempt_no > 0),
    delivery_key text NOT NULL,
    started_at timestamptz NOT NULL,
    lease_until timestamptz NOT NULL,
    result text NOT NULL DEFAULT 'in_flight'
        CHECK (result IN ('in_flight', 'retryable', 'delivered', 'rejected')),
    completed_at timestamptz,
    PRIMARY KEY (tenant_id, workspace_id, occurrence_id, attempt_no),
    FOREIGN KEY (tenant_id, workspace_id, occurrence_id)
        REFERENCES ownsolid_alert_candidate (tenant_id, workspace_id, occurrence_id),
    CHECK ((result = 'in_flight') = (completed_at IS NULL))
);
