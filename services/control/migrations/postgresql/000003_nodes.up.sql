-- M03 node authority, observations and a durable allowlisted discovery task.
CREATE TABLE node_join_tokens (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    node_name varchar(100) NOT NULL,
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash)=32),
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    revoked_at timestamptz,
    FOREIGN KEY (tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id)
);
CREATE INDEX node_join_scope_idx ON node_join_tokens(tenant_id,created_at DESC,id DESC);
CREATE TABLE managed_nodes (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    name varchar(100) NOT NULL,
    status varchar(16) NOT NULL CHECK (status IN ('active','disabled','removed')),
    version bigint NOT NULL DEFAULT 1 CHECK (version BETWEEN 1 AND 9007199254740991),
    credential_hash bytea UNIQUE CHECK (credential_hash IS NULL OR octet_length(credential_hash)=32),
    protocol_version varchar(16) NOT NULL,
    facts jsonb NOT NULL,
    last_heartbeat_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (tenant_id,workspace_id,id),
    FOREIGN KEY (tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id),
    CHECK ((status='removed')=(credential_hash IS NULL))
);
CREATE UNIQUE INDEX nodes_live_name_idx ON managed_nodes(tenant_id,name COLLATE "C") WHERE status<>'removed';
CREATE INDEX nodes_scope_list_idx ON managed_nodes(tenant_id,workspace_id,created_at DESC,id DESC);
ALTER TABLE node_join_tokens ADD COLUMN node_id uuid;
ALTER TABLE node_join_tokens ADD FOREIGN KEY (tenant_id,workspace_id,node_id) REFERENCES managed_nodes(tenant_id,workspace_id,id);
CREATE TABLE node_tasks (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    node_id uuid NOT NULL,
    operation varchar(64) NOT NULL CHECK (operation='collect_facts.v1'),
    state varchar(16) NOT NULL CHECK (state IN ('pending','running','succeeded','failed','cancelled')),
    idempotency_key uuid NOT NULL,
    requested_by uuid NOT NULL REFERENCES users(id),
    attempt integer NOT NULL DEFAULT 0 CHECK (attempt BETWEEN 0 AND 3),
    lease_token_hash bytea CHECK (lease_token_hash IS NULL OR octet_length(lease_token_hash)=32),
    lease_expires_at timestamptz,
    error_code varchar(64),
    completed_lease_hash bytea CHECK (completed_lease_hash IS NULL OR octet_length(completed_lease_hash)=32),
    result_hash bytea CHECK (result_hash IS NULL OR octet_length(result_hash)=32),
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deadline_at timestamptz NOT NULL,
    completed_at timestamptz,
    UNIQUE (node_id,idempotency_key),
    FOREIGN KEY (tenant_id,workspace_id,node_id) REFERENCES managed_nodes(tenant_id,workspace_id,id),
    CHECK ((state='running')=(lease_token_hash IS NOT NULL AND lease_expires_at IS NOT NULL)),
    CHECK ((state IN ('succeeded','failed','cancelled'))=(completed_at IS NOT NULL))
);
CREATE INDEX tasks_scope_list_idx ON node_tasks(tenant_id,workspace_id,node_id,created_at DESC,id DESC);
CREATE INDEX tasks_claim_idx ON node_tasks(node_id,state,created_at) WHERE state IN ('pending','running');
CREATE UNIQUE INDEX tasks_one_inflight_idx ON node_tasks(node_id) WHERE state IN ('pending','running');
