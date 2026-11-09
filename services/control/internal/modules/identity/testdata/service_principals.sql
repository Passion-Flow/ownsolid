-- Isolated repository fixture for the IAM-109 schema handoff.
CREATE TABLE service_principals (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    display_name varchar(100) NOT NULL CHECK (length(btrim(display_name)) > 0),
    created_by uuid NOT NULL REFERENCES users(id),
    creation_key uuid NOT NULL,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    revoked_by uuid REFERENCES users(id),
    FOREIGN KEY (tenant_id, workspace_id) REFERENCES workspaces(tenant_id, id),
    UNIQUE (tenant_id, workspace_id, id),
    UNIQUE (tenant_id, workspace_id, created_by, creation_key),
    CHECK (expires_at > created_at),
    CHECK ((revoked_at IS NULL) = (revoked_by IS NULL)),
    CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);
