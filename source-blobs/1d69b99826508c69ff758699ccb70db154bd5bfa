\set ON_ERROR_STOP on
-- Metadata derivation only: this never substitutes for production Migrate.
BEGIN;
SET LOCAL search_path = :"schema";
CREATE TABLE schema_migrations(version integer PRIMARY KEY,checksum text NOT NULL,applied_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP);
-- Exact source: services/control/migrations/postgresql/000001_identity.up.sql
-- M01 PostgreSQL identity migration, version 0.1.0.
-- UUIDs and secret hashes are supplied by the application; no extension required.

CREATE TABLE tenants (
    id uuid PRIMARY KEY,
    name varchar(100) NOT NULL,
    status varchar(16) NOT NULL CHECK (status IN ('active', 'disabled')),
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE workspaces (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id),
    name varchar(100) NOT NULL,
    status varchar(16) NOT NULL CHECK (status IN ('active', 'disabled')),
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (tenant_id, id)
);

CREATE TABLE users (
    id uuid PRIMARY KEY,
    email_canonical varchar(254) COLLATE "C" NOT NULL UNIQUE,
    email_display varchar(254) NOT NULL,
    display_name varchar(100) NOT NULL,
    locale varchar(16) NOT NULL CHECK (locale IN ('en', 'zh-CN')),
    status varchar(16) NOT NULL CHECK (status IN ('active', 'disabled')),
    is_platform_admin boolean NOT NULL DEFAULT false,
    email_verified_at timestamptz,
    auth_epoch bigint NOT NULL DEFAULT 1 CHECK (auth_epoch BETWEEN 1 AND 9007199254740991),
    version bigint NOT NULL DEFAULT 1 CHECK (version BETWEEN 1 AND 9007199254740991),
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (email_canonical = lower(email_canonical))
);
CREATE INDEX users_list_idx ON users (created_at DESC, id DESC);
CREATE INDEX users_status_list_idx ON users (status, created_at DESC, id DESC);

CREATE TABLE password_credentials (
    user_id uuid PRIMARY KEY REFERENCES users(id),
    password_phc varchar(512) NOT NULL,
    version bigint NOT NULL DEFAULT 1 CHECK (version BETWEEN 1 AND 9007199254740991),
    changed_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE workspace_memberships (
    user_id uuid NOT NULL REFERENCES users(id),
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    role varchar(16) NOT NULL CHECK (role IN ('owner', 'member')),
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id, workspace_id),
    FOREIGN KEY (tenant_id, workspace_id) REFERENCES workspaces(tenant_id, id)
);

CREATE TABLE installation_state (
    id smallint PRIMARY KEY CHECK (id = 1),
    bootstrap_complete boolean NOT NULL DEFAULT false,
    tenant_id uuid REFERENCES tenants(id),
    default_workspace_id uuid,
    primary_admin_user_id uuid REFERENCES users(id),
    bootstrapped_at timestamptz,
    FOREIGN KEY (tenant_id, default_workspace_id) REFERENCES workspaces(tenant_id, id),
    CHECK (
        (bootstrap_complete AND tenant_id IS NOT NULL AND default_workspace_id IS NOT NULL
         AND primary_admin_user_id IS NOT NULL AND bootstrapped_at IS NOT NULL)
        OR (NOT bootstrap_complete AND tenant_id IS NULL AND default_workspace_id IS NULL
            AND primary_admin_user_id IS NULL AND bootstrapped_at IS NULL)
    )
);
INSERT INTO installation_state (id) VALUES (1);

CREATE TABLE sessions (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id),
    audience varchar(16) NOT NULL CHECK (audience IN ('portal', 'admin')),
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    auth_epoch bigint NOT NULL CHECK (auth_epoch BETWEEN 1 AND 9007199254740991),
    created_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    absolute_expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    revoke_reason varchar(48),
    CHECK (absolute_expires_at > created_at),
    CHECK (last_seen_at >= created_at),
    CHECK ((revoked_at IS NULL) = (revoke_reason IS NULL))
);
CREATE INDEX sessions_user_live_idx ON sessions (user_id) WHERE revoked_at IS NULL;
CREATE INDEX sessions_expiry_idx ON sessions (absolute_expires_at);

CREATE TABLE invitations (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    email_canonical varchar(254) COLLATE "C" NOT NULL,
    locale varchar(16) NOT NULL CHECK (locale IN ('en', 'zh-CN')),
    role varchar(16) NOT NULL DEFAULT 'member' CHECK (role = 'member'),
    status varchar(16) NOT NULL CHECK (status IN ('pending', 'accepted', 'revoked', 'expired')),
    token_hash bytea UNIQUE CHECK (token_hash IS NULL OR octet_length(token_hash) = 32),
    token_generation integer NOT NULL DEFAULT 1 CHECK (token_generation >= 1),
    version bigint NOT NULL DEFAULT 1 CHECK (version BETWEEN 1 AND 9007199254740991),
    created_by uuid NOT NULL REFERENCES users(id),
    accepted_user_id uuid REFERENCES users(id),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    accepted_at timestamptz,
    revoked_at timestamptz,
    FOREIGN KEY (tenant_id, workspace_id) REFERENCES workspaces(tenant_id, id),
    CHECK (email_canonical = lower(email_canonical)),
    CHECK (expires_at > created_at),
    CHECK ((status = 'pending') = (token_hash IS NOT NULL)),
    CHECK (
        (status = 'accepted' AND accepted_user_id IS NOT NULL AND accepted_at IS NOT NULL)
        OR (status <> 'accepted' AND accepted_user_id IS NULL AND accepted_at IS NULL)
    ),
    CHECK ((status = 'revoked') = (revoked_at IS NOT NULL))
);
CREATE UNIQUE INDEX invitations_pending_email_idx ON invitations(email_canonical) WHERE status = 'pending';
CREATE INDEX invitations_list_idx ON invitations(created_at DESC, id DESC);

CREATE TABLE password_reset_tokens (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id),
    audience varchar(16) NOT NULL CHECK (audience IN ('portal', 'admin')),
    token_hash bytea UNIQUE CHECK (token_hash IS NULL OR octet_length(token_hash) = 32),
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    revoked_at timestamptz,
    UNIQUE (id, user_id),
    CHECK (expires_at > created_at),
    CHECK (NOT (consumed_at IS NOT NULL AND revoked_at IS NOT NULL)),
    CHECK ((consumed_at IS NULL AND revoked_at IS NULL) = (token_hash IS NOT NULL))
);
CREATE INDEX reset_tokens_user_live_idx ON password_reset_tokens(user_id, expires_at)
    WHERE consumed_at IS NULL AND revoked_at IS NULL;

CREATE TABLE mail_deliveries (
    id uuid PRIMARY KEY,
    purpose varchar(24) NOT NULL CHECK (purpose IN ('invitation', 'password_reset', 'password_changed')),
    recipient_email varchar(254) NOT NULL,
    locale varchar(16) NOT NULL CHECK (locale IN ('en', 'zh-CN')),
    invitation_id uuid REFERENCES invitations(id),
    invitation_generation integer,
    reset_token_id uuid REFERENCES password_reset_tokens(id),
    user_id uuid REFERENCES users(id),
    state varchar(16) NOT NULL CHECK (state IN ('queued', 'sending', 'sent', 'failed', 'cancelled')),
    payload_key_id varchar(64),
    payload_nonce bytea,
    payload_ciphertext bytea,
    payload_schema_version integer NOT NULL DEFAULT 1 CHECK (payload_schema_version = 1),
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count BETWEEN 0 AND 5),
    next_attempt_at timestamptz NOT NULL,
    lease_token uuid,
    locked_until timestamptz,
    error_category varchar(64),
    message_id varchar(254) NOT NULL UNIQUE,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    completed_at timestamptz,
    CHECK (
        (purpose = 'invitation' AND invitation_id IS NOT NULL AND invitation_generation IS NOT NULL
         AND reset_token_id IS NULL AND user_id IS NULL)
        OR (purpose = 'password_reset' AND reset_token_id IS NOT NULL AND invitation_id IS NULL
            AND invitation_generation IS NULL AND user_id IS NOT NULL)
        OR (purpose = 'password_changed' AND user_id IS NOT NULL AND invitation_id IS NULL
            AND invitation_generation IS NULL AND reset_token_id IS NULL)
    ),
    CHECK (
        (state IN ('queued', 'sending') AND payload_key_id IS NOT NULL AND payload_nonce IS NOT NULL
         AND octet_length(payload_nonce) = 12 AND payload_ciphertext IS NOT NULL AND completed_at IS NULL)
        OR (state IN ('sent', 'failed', 'cancelled') AND payload_key_id IS NULL AND payload_nonce IS NULL
            AND payload_ciphertext IS NULL AND completed_at IS NOT NULL)
    ),
    FOREIGN KEY (reset_token_id, user_id) REFERENCES password_reset_tokens(id, user_id),
    CHECK (
        (state = 'sending' AND lease_token IS NOT NULL AND locked_until IS NOT NULL)
        OR (state <> 'sending' AND lease_token IS NULL AND locked_until IS NULL)
    )
);
CREATE INDEX mail_claim_idx ON mail_deliveries(state, next_attempt_at);
CREATE INDEX mail_list_idx ON mail_deliveries(created_at DESC, id DESC);

CREATE TABLE audit_events (
    id uuid PRIMARY KEY,
    occurred_at timestamptz NOT NULL,
    actor_user_id uuid REFERENCES users(id),
    subject_user_id uuid REFERENCES users(id),
    tenant_id uuid REFERENCES tenants(id),
    workspace_id uuid,
    audience varchar(16) NOT NULL CHECK (audience IN ('portal', 'admin', 'operator', 'system')),
    action varchar(64) NOT NULL,
    outcome varchar(16) NOT NULL CHECK (outcome IN ('success', 'failure', 'accepted')),
    target_type varchar(48),
    target_id uuid,
    request_id uuid NOT NULL,
    identifier_digest bytea CHECK (identifier_digest IS NULL OR octet_length(identifier_digest) = 32),
    reason varchar(500),
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    FOREIGN KEY (tenant_id, workspace_id) REFERENCES workspaces(tenant_id, id),
    CHECK (workspace_id IS NULL OR tenant_id IS NOT NULL),
    CHECK (jsonb_typeof(metadata) = 'object'),
    CHECK (octet_length(metadata::text) <= 4096)
);
CREATE INDEX audit_list_idx ON audit_events(occurred_at DESC, id DESC);
CREATE INDEX audit_action_idx ON audit_events(action, occurred_at DESC, id DESC);

CREATE TABLE rate_limit_buckets (
    bucket_key bytea PRIMARY KEY CHECK (octet_length(bucket_key) = 32),
    window_started_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    attempt_count integer NOT NULL CHECK (attempt_count >= 0),
    last_admitted_at timestamptz,
    CHECK (expires_at > window_started_at)
);
CREATE INDEX rate_limit_expiry_idx ON rate_limit_buckets(expires_at);

CREATE TABLE idempotency_records (
    actor_user_id uuid NOT NULL REFERENCES users(id),
    operation_id varchar(64) NOT NULL CHECK (operation_id IN ('createInvitation', 'resendInvitation')),
    idempotency_key uuid NOT NULL,
    request_hash bytea NOT NULL CHECK (octet_length(request_hash) = 32),
    response_status integer NOT NULL CHECK (response_status IN (200, 201)),
    response_body jsonb NOT NULL,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (actor_user_id, operation_id, idempotency_key),
    CHECK (expires_at > created_at),
    CHECK (jsonb_typeof(response_body) = 'object'),
    CHECK (octet_length(response_body::text) <= 16384)
);
CREATE INDEX idempotency_expiry_idx ON idempotency_records(expires_at);

INSERT INTO schema_migrations(version,checksum) VALUES(1,'7c04677a399b7c8ff0b2e0571aeb0ada340f63e3c631057f54a9dc7539d9dc27');
-- Exact source: services/control/migrations/postgresql/000002_local_session_source.up.sql
-- Account profile only: retain the current identity wire/storage shape without an SSO runtime.
ALTER TABLE sessions ADD COLUMN sso_tenant_id uuid,
 ADD COLUMN sso_connector_id uuid, ADD COLUMN sso_binding_id uuid,
 ADD COLUMN sso_auth_version bigint, ADD COLUMN sso_flow_id uuid,
 ADD CONSTRAINT account_local_session_only CHECK(sso_tenant_id IS NULL AND sso_connector_id IS NULL AND sso_binding_id IS NULL AND sso_auth_version IS NULL AND sso_flow_id IS NULL);

INSERT INTO schema_migrations(version,checksum) VALUES(2,'8eb5ba9a9415f20aa190ab3045f4a2671b56583c2bcf4d82e504a9ca7464e616');
-- Exact source: services/control/migrations/postgresql/000003_licensing.up.sql
CREATE TABLE platform_license_state (
    id smallint PRIMARY KEY CHECK (id = 1),
    deployment_id uuid NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    observed_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
INSERT INTO platform_license_state(id) VALUES (1);

CREATE TABLE platform_license_artifacts (
    license_id uuid PRIMARY KEY,
    sequence integer NOT NULL UNIQUE CHECK (sequence BETWEEN 1 AND 1024),
    deployment_id uuid NOT NULL REFERENCES platform_license_state(deployment_id) ON DELETE RESTRICT,
    artifact_sha256 varchar(64) NOT NULL UNIQUE CHECK (artifact_sha256 ~ '^[a-f0-9]{64}$'),
    artifact bytea NOT NULL CHECK (octet_length(artifact) BETWEEN 1 AND 65536),
    predecessor_id uuid REFERENCES platform_license_artifacts(license_id) ON DELETE RESTRICT,
    imported_by uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    imported_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    reason varchar(500) NOT NULL CHECK (length(reason) > 0),
    CHECK (predecessor_id IS NULL OR predecessor_id <> license_id)
);

INSERT INTO schema_migrations(version,checksum) VALUES(3,'f79788dcbbfefd20b19d1096617f8f824ed0ca99a7b917aff7372bb7d9a5888f');
-- Exact source: services/control/migrations/postgresql/000004_audit_operation_lookup.up.sql
CREATE INDEX audit_operation_lookup_idx ON audit_events(request_id,actor_user_id,audience,tenant_id);

INSERT INTO schema_migrations(version,checksum) VALUES(4,'08576ce8973aa582bd38d64f33eee7485e037753afb6e08ffbec0ec27bc1a755');
-- Exact source: services/control/migrations/postgresql/000005_workspaces.up.sql
-- M02 additive workspace lifecycle, fixed roles and an actual scoped note resource.
ALTER TABLE workspaces ADD COLUMN version bigint NOT NULL DEFAULT 1 CHECK (version BETWEEN 1 AND 9007199254740991);
ALTER TABLE workspaces ADD COLUMN updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP;
CREATE UNIQUE INDEX workspaces_tenant_name_idx ON workspaces(tenant_id, name COLLATE "C");
ALTER TABLE workspace_memberships DROP CONSTRAINT workspace_memberships_role_check;
ALTER TABLE workspace_memberships ADD CONSTRAINT workspace_memberships_role_check CHECK (role IN ('owner','member','viewer'));
ALTER TABLE workspace_memberships ADD COLUMN version bigint NOT NULL DEFAULT 1 CHECK (version BETWEEN 1 AND 9007199254740991);
ALTER TABLE workspace_memberships ADD COLUMN updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP;
CREATE INDEX memberships_scope_idx ON workspace_memberships(tenant_id,workspace_id,user_id);
CREATE TABLE workspace_notes (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    title varchar(100) NOT NULL,
    text text NOT NULL CHECK (octet_length(text)<=8192),
    author_user_id uuid NOT NULL REFERENCES users(id),
    version bigint NOT NULL DEFAULT 1 CHECK (version BETWEEN 1 AND 9007199254740991),
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at timestamptz,
    FOREIGN KEY (tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id)
);
CREATE INDEX notes_scope_list_idx ON workspace_notes(tenant_id,workspace_id,created_at DESC,id DESC) WHERE deleted_at IS NULL;

INSERT INTO schema_migrations(version,checksum) VALUES(5,'d108d424591ad71ca5dcac5d5738d93f46e00ecb2d9b634ca9c2f9eff5439f2b');
-- Exact source: services/control/migrations/postgresql/000006_workspace_authority.up.sql
-- M01+M02 ledger version 6. Immutable membership incarnation identity.
-- Existing memberships retain tenant/workspace/user keys, roles and timestamps.
-- No credentials, sessions, account/mail/license/audit rows are changed.
ALTER TABLE workspace_memberships ADD COLUMN authority_id uuid NOT NULL DEFAULT gen_random_uuid();
CREATE UNIQUE INDEX account_membership_authority_idx ON workspace_memberships(authority_id);

INSERT INTO schema_migrations(version,checksum) VALUES(6,'f95313b32548304658242a6314669d1920a0059f7cf110b6aee37fa0745fd73f');
-- Exact source: services/control/migrations/postgresql/000007_nodes.up.sql
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

INSERT INTO schema_migrations(version,checksum) VALUES(7,'dd8809e3e02f77296120c430eb6480da5d83cacd9293e66c0e175d2f15367dd8');
-- Exact source: services/control/migrations/postgresql/000008_node_protocol.up.sql
-- Minimal M03 uses the existing shared node repository, with discovery-only invariants.
ALTER TABLE node_join_tokens ADD COLUMN execution_profile varchar(32) NOT NULL DEFAULT 'read_only' CHECK (execution_profile='read_only');
ALTER TABLE managed_nodes ADD COLUMN execution_profile varchar(32) NOT NULL DEFAULT 'read_only' CHECK (execution_profile='read_only');
ALTER TABLE managed_nodes ADD COLUMN capabilities jsonb NOT NULL DEFAULT '["collect_facts.v1"]'::jsonb CHECK (capabilities='["collect_facts.v1"]'::jsonb);
-- Reserved repository fields remain inert; no workload owner/schema is constructed.
ALTER TABLE managed_nodes ADD COLUMN inference_fenced_at timestamptz CHECK (inference_fenced_at IS NULL);
ALTER TABLE node_tasks ADD COLUMN payload jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (payload='{}'::jsonb);
ALTER TABLE node_tasks ADD COLUMN request_fingerprint bytea CHECK (request_fingerprint IS NULL);
ALTER TABLE node_tasks ADD COLUMN inference_requested_epoch bigint CHECK (inference_requested_epoch IS NULL);
ALTER TABLE node_tasks ADD CONSTRAINT node_tasks_scope_identity_key UNIQUE(tenant_id,workspace_id,node_id,id);

INSERT INTO schema_migrations(version,checksum) VALUES(8,'e2f175558817a63403fe7459d4719d6d6fc7e6314a6e98f076b9d417485aa224');
-- Exact source: services/control/migrations/postgresql/000009_components.up.sql
-- M04 cumulative evolution. Entries 1-8 are immutable installed M03 history.
-- Reuse the already-present repository columns; admit only the M04 authority.
ALTER TABLE node_join_tokens DROP CONSTRAINT node_join_tokens_execution_profile_check;
ALTER TABLE node_join_tokens ADD CONSTRAINT node_join_tokens_execution_profile_check CHECK(execution_profile IN ('read_only','systemd_components'));
ALTER TABLE managed_nodes DROP CONSTRAINT managed_nodes_execution_profile_check;
ALTER TABLE managed_nodes ADD CONSTRAINT managed_nodes_execution_profile_check CHECK(execution_profile IN ('read_only','systemd_components'));
ALTER TABLE managed_nodes DROP CONSTRAINT managed_nodes_capabilities_check;
ALTER TABLE managed_nodes ADD CONSTRAINT managed_nodes_capabilities_check CHECK(
 (execution_profile='read_only' AND capabilities='["collect_facts.v1"]'::jsonb) OR
 (execution_profile='systemd_components' AND capabilities='["collect_facts.v1","node_exporter.install.v1","node_exporter.start.v1","node_exporter.stop.v1","node_exporter.remove.v1","node_exporter.observe.v1"]'::jsonb));
ALTER TABLE node_tasks DROP CONSTRAINT node_tasks_operation_check;
ALTER TABLE node_tasks ADD CONSTRAINT node_tasks_operation_check CHECK(operation IN ('collect_facts.v1','node_exporter.install.v1','node_exporter.start.v1','node_exporter.stop.v1','node_exporter.remove.v1','node_exporter.observe.v1'));
ALTER TABLE node_tasks DROP CONSTRAINT node_tasks_payload_check;
ALTER TABLE node_tasks ADD CONSTRAINT node_tasks_payload_check CHECK(jsonb_typeof(payload)='object' AND octet_length(payload::text)<=4096);
ALTER TABLE node_tasks DROP CONSTRAINT node_tasks_request_fingerprint_check;
ALTER TABLE node_tasks ADD CONSTRAINT node_tasks_request_fingerprint_check CHECK(request_fingerprint IS NULL OR octet_length(request_fingerprint)=32);
-- Compatibility fields satisfy the shared current repository, without M05 authority.
CREATE TABLE managed_components (
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 node_id uuid NOT NULL,
 component varchar(32) NOT NULL CHECK(component='node_exporter'),
 version varchar(32) NOT NULL CHECK(version='1.12.1'),
 desired_state varchar(16) NOT NULL CHECK(desired_state IN ('running','stopped','not_installed')),
 observed_state varchar(16) NOT NULL CHECK(observed_state IN ('running','stopped','not_installed','unknown')),
 managed boolean NOT NULL,
 observation jsonb NOT NULL CHECK(jsonb_typeof(observation)='object' AND octet_length(observation::text)<=8192),
 observed_at timestamptz NOT NULL,
 last_task_id uuid,
 updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 desired_version varchar(32) NOT NULL DEFAULT '1.12.1' CHECK(desired_version='1.12.1'),
 desired_config jsonb DEFAULT '{"log_level":"info","max_scrape_requests":40}'::jsonb CHECK(desired_config IS NULL OR desired_config='{"log_level":"info","max_scrape_requests":40}'::jsonb),
 applied_config jsonb CHECK(applied_config IS NULL OR applied_config='{"log_level":"info","max_scrape_requests":40}'::jsonb),
 previous_version varchar(32) CHECK(previous_version IS NULL),
 last_verified_at timestamptz,
 PRIMARY KEY(node_id,component),
 FOREIGN KEY(tenant_id,workspace_id,node_id) REFERENCES managed_nodes(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,node_id,last_task_id) REFERENCES node_tasks(tenant_id,workspace_id,node_id,id)
);
CREATE INDEX components_scope_idx ON managed_components(tenant_id,workspace_id,node_id);

INSERT INTO schema_migrations(version,checksum) VALUES(9,'998ba6c066c8ac50e0a1fa281b8e22867a8987cfef493fbdaec863fbac3c8938');
-- Exact source: services/control/migrations/postgresql/000010_maintenance.up.sql
-- M05 cumulative evolution. Entries 1-9 remain immutable installed M04 history.
-- Reuse the already-present repository columns; admit only the M05 authority.
ALTER TABLE node_join_tokens DROP CONSTRAINT node_join_tokens_execution_profile_check;
ALTER TABLE node_join_tokens ADD CONSTRAINT node_join_tokens_execution_profile_check CHECK(execution_profile IN ('read_only','systemd_components','systemd_maintenance'));
ALTER TABLE managed_nodes DROP CONSTRAINT managed_nodes_execution_profile_check;
ALTER TABLE managed_nodes ADD CONSTRAINT managed_nodes_execution_profile_check CHECK(execution_profile IN ('read_only','systemd_components','systemd_maintenance'));
ALTER TABLE managed_nodes DROP CONSTRAINT managed_nodes_capabilities_check;
ALTER TABLE managed_nodes ADD CONSTRAINT managed_nodes_capabilities_check CHECK(
 (execution_profile='read_only' AND capabilities='["collect_facts.v1"]'::jsonb) OR
 (execution_profile='systemd_components' AND capabilities='["collect_facts.v1","node_exporter.install.v1","node_exporter.start.v1","node_exporter.stop.v1","node_exporter.remove.v1","node_exporter.observe.v1"]'::jsonb) OR
 (execution_profile='systemd_maintenance' AND capabilities='["collect_facts.v1","node_exporter.install.v1","node_exporter.start.v1","node_exporter.stop.v1","node_exporter.remove.v1","node_exporter.observe.v1","node_exporter.configure.v1","node_exporter.restart.v1","node_exporter.upgrade.v1","node_exporter.rollback.v1","node_exporter.logs.v1"]'::jsonb));
ALTER TABLE node_tasks DROP CONSTRAINT node_tasks_operation_check;
ALTER TABLE node_tasks ADD CONSTRAINT node_tasks_operation_check CHECK(operation IN ('collect_facts.v1','node_exporter.install.v1','node_exporter.start.v1','node_exporter.stop.v1','node_exporter.remove.v1','node_exporter.observe.v1','node_exporter.configure.v1','node_exporter.restart.v1','node_exporter.upgrade.v1','node_exporter.rollback.v1','node_exporter.logs.v1'));
ALTER TABLE node_tasks DROP CONSTRAINT node_tasks_payload_check;
ALTER TABLE node_tasks ADD CONSTRAINT node_tasks_payload_check CHECK(jsonb_typeof(payload)='object' AND octet_length(payload::text)<=4096);
ALTER TABLE node_tasks DROP CONSTRAINT node_tasks_request_fingerprint_check;
ALTER TABLE node_tasks ADD CONSTRAINT node_tasks_request_fingerprint_check CHECK(request_fingerprint IS NULL OR octet_length(request_fingerprint)=32);
ALTER TABLE managed_components DROP CONSTRAINT managed_components_version_check;
ALTER TABLE managed_components ADD CONSTRAINT managed_components_version_check CHECK(version IN ('1.12.0','1.12.1'));
ALTER TABLE managed_components DROP CONSTRAINT managed_components_desired_version_check;
ALTER TABLE managed_components ADD CONSTRAINT managed_components_desired_version_check CHECK(desired_version IN ('1.12.0','1.12.1'));
ALTER TABLE managed_components DROP CONSTRAINT managed_components_previous_version_check;
ALTER TABLE managed_components ADD CONSTRAINT managed_components_previous_version_check CHECK(previous_version IS NULL OR previous_version IN ('1.12.0','1.12.1'));
ALTER TABLE managed_components DROP CONSTRAINT managed_components_desired_config_check;
ALTER TABLE managed_components ADD CONSTRAINT managed_components_desired_config_check CHECK(desired_config IS NULL OR (jsonb_typeof(desired_config)='object' AND desired_config ? 'log_level' AND desired_config ? 'max_scrape_requests' AND desired_config - 'log_level' - 'max_scrape_requests'='{}'::jsonb AND desired_config->>'log_level' IN ('debug','info','warn','error') AND jsonb_typeof(desired_config->'max_scrape_requests')='number' AND desired_config->>'max_scrape_requests' ~ '^([1-9]|[1-3][0-9]|40)$'));
ALTER TABLE managed_components DROP CONSTRAINT managed_components_applied_config_check;
ALTER TABLE managed_components ADD CONSTRAINT managed_components_applied_config_check CHECK(applied_config IS NULL OR (jsonb_typeof(applied_config)='object' AND applied_config ? 'log_level' AND applied_config ? 'max_scrape_requests' AND applied_config - 'log_level' - 'max_scrape_requests'='{}'::jsonb AND applied_config->>'log_level' IN ('debug','info','warn','error') AND jsonb_typeof(applied_config->'max_scrape_requests')='number' AND applied_config->>'max_scrape_requests' ~ '^([1-9]|[1-3][0-9]|40)$'));
CREATE TABLE component_log_snapshots (
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 node_id uuid NOT NULL,
 component varchar(32) NOT NULL CHECK(component='node_exporter'),
 task_id uuid NOT NULL,
 snapshot jsonb NOT NULL CHECK(jsonb_typeof(snapshot)='object' AND octet_length(snapshot::text)<=16384),
 received_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(node_id,component),
 FOREIGN KEY(tenant_id,workspace_id,node_id) REFERENCES managed_nodes(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,node_id,task_id) REFERENCES node_tasks(tenant_id,workspace_id,node_id,id)
);
CREATE INDEX component_logs_scope_idx ON component_log_snapshots(tenant_id,workspace_id,node_id);

INSERT INTO schema_migrations(version,checksum) VALUES(10,'c24b9dc8e7064436413792f9a308ded7aa2bf4613bfc86c24b941d5937ea41c1');
-- Exact source: services/control/migrations/postgresql/000011_models.up.sql
-- M06 cumulative evolution from immutable M05 entries1-10. No platform replay.
CREATE TABLE model_versions (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 name varchar(100) NOT NULL,
 model_key varchar(64) NOT NULL CHECK(model_key='qwen3-0.6b'),
 catalog_digest varchar(64) NOT NULL CHECK(catalog_digest ~ '^[a-f0-9]{64}$'),
 manifest jsonb NOT NULL CHECK(jsonb_typeof(manifest)='object' AND octet_length(manifest::text)<=16384),
 state varchar(16) NOT NULL CHECK(state IN ('ready','deleted')),
 created_by uuid NOT NULL REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 deleted_at timestamptz,
 UNIQUE(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id)
);
CREATE UNIQUE INDEX model_ready_catalog_idx ON model_versions(tenant_id,workspace_id,catalog_digest) WHERE state='ready';
CREATE TABLE model_imports (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 name varchar(100) NOT NULL,
 catalog_digest varchar(64) NOT NULL CHECK(catalog_digest ~ '^[a-f0-9]{64}$'),
 expected_bytes bigint NOT NULL CHECK(expected_bytes BETWEEN 1 AND 2147483648),
 uploaded_bytes bigint NOT NULL DEFAULT 0 CHECK(uploaded_bytes>=0 AND uploaded_bytes<=expected_bytes),
 state varchar(16) NOT NULL CHECK(state IN ('uploading','queued','verifying','ready','failed','cancelled')),
 error_code varchar(64),
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 3),
 requested_by uuid NOT NULL REFERENCES users(id),
 requested_epoch bigint NOT NULL CHECK(requested_epoch>=0),
 idempotency_key uuid NOT NULL,
 fingerprint bytea NOT NULL CHECK(octet_length(fingerprint)=32),
 reason varchar(500) NOT NULL,
 model_id uuid,
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,workspace_id,id),
 UNIQUE(tenant_id,workspace_id,idempotency_key),
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id),
 FOREIGN KEY(tenant_id,workspace_id,model_id) REFERENCES model_versions(tenant_id,workspace_id,id)
);
CREATE UNIQUE INDEX model_import_active_workspace_idx ON model_imports(tenant_id,workspace_id) WHERE state NOT IN ('ready','cancelled');
CREATE TABLE model_import_chunks (
 import_id uuid NOT NULL REFERENCES model_imports(id),
 byte_offset bigint NOT NULL CHECK(byte_offset>=0 AND byte_offset%8388608=0),
 size_bytes integer NOT NULL CHECK(size_bytes BETWEEN 1 AND 8388608),
 sha256 varchar(64) NOT NULL CHECK(sha256 ~ '^[a-f0-9]{64}$'),
 PRIMARY KEY(import_id,byte_offset)
);
CREATE INDEX model_import_queue_idx ON model_imports(state,created_at) WHERE state IN ('queued','verifying');
-- Inference authority is explicit; no existing enrollment gains capabilities.
ALTER TABLE node_join_tokens DROP CONSTRAINT node_join_tokens_execution_profile_check;
ALTER TABLE node_join_tokens ADD CONSTRAINT node_join_tokens_execution_profile_check CHECK(execution_profile IN ('read_only','systemd_components','systemd_maintenance','docker_inference'));
ALTER TABLE managed_nodes DROP CONSTRAINT managed_nodes_execution_profile_check;
ALTER TABLE managed_nodes ADD CONSTRAINT managed_nodes_execution_profile_check CHECK(execution_profile IN ('read_only','systemd_components','systemd_maintenance','docker_inference'));
ALTER TABLE node_tasks DROP CONSTRAINT node_tasks_operation_check;
ALTER TABLE node_tasks ADD CONSTRAINT node_tasks_operation_check CHECK(operation IN ('collect_facts.v1','node_exporter.install.v1','node_exporter.start.v1','node_exporter.stop.v1','node_exporter.remove.v1','node_exporter.observe.v1','node_exporter.configure.v1','node_exporter.restart.v1','node_exporter.upgrade.v1','node_exporter.rollback.v1','node_exporter.logs.v1','inference.deploy.v1','inference.stop.v1','inference.remove.v1','inference.observe.v1','inference.verify.v1'));
CREATE TABLE inference_nodes (
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 node_id uuid PRIMARY KEY,
 observation jsonb NOT NULL CHECK(jsonb_typeof(observation)='object' AND octet_length(observation::text)<=32768),
 received_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 FOREIGN KEY(tenant_id,workspace_id,node_id) REFERENCES managed_nodes(tenant_id,workspace_id,id)
);
CREATE TABLE deployments (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 node_id uuid NOT NULL,
 model_id uuid NOT NULL,
 gpu_uuid varchar(64) NOT NULL CHECK(gpu_uuid ~ '^GPU-[a-f0-9-]{36}$'),
 desired_state varchar(16) NOT NULL CHECK(desired_state IN ('running','stopped','removed')),
 observed_state varchar(16) NOT NULL DEFAULT 'unknown' CHECK(observed_state IN ('running','loading','stopped','not_installed','unknown','failed')),
 revision bigint NOT NULL CHECK(revision BETWEEN 1 AND 9007199254740991),
 observation jsonb CHECK(observation IS NULL OR jsonb_typeof(observation)='object' AND octet_length(observation::text)<=8192),
 observed_at timestamptz,
 last_task_id uuid,
 removed_at timestamptz,
 created_by uuid NOT NULL REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,workspace_id,id),
 UNIQUE(tenant_id,workspace_id,node_id,id),
 FOREIGN KEY(tenant_id,workspace_id,node_id) REFERENCES managed_nodes(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,model_id) REFERENCES model_versions(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,node_id,last_task_id) REFERENCES node_tasks(tenant_id,workspace_id,node_id,id)
);
CREATE UNIQUE INDEX deployment_one_retained_node_idx ON deployments(node_id) WHERE removed_at IS NULL;
CREATE INDEX deployment_scope_idx ON deployments(tenant_id,workspace_id,created_at DESC,id DESC);
CREATE TABLE gpu_leases (
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 node_id uuid NOT NULL,
 gpu_uuid varchar(64) NOT NULL,
 deployment_id uuid NOT NULL,
 revision bigint NOT NULL CHECK(revision BETWEEN 1 AND 9007199254740991),
 state varchar(24) NOT NULL CHECK(state IN ('held','released','recovery_required')),
 updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(deployment_id),
 FOREIGN KEY(tenant_id,workspace_id,node_id,deployment_id) REFERENCES deployments(tenant_id,workspace_id,node_id,id)
);
CREATE UNIQUE INDEX gpu_exclusive_node_idx ON gpu_leases(node_id) WHERE state IN ('held','recovery_required');
ALTER TABLE managed_nodes DROP CONSTRAINT managed_nodes_inference_fenced_at_check;
ALTER TABLE deployments ADD COLUMN recovery_receipt_sha256 varchar(64) CHECK(recovery_receipt_sha256 IS NULL OR recovery_receipt_sha256 ~ '^[a-f0-9]{64}$');
-- A new node identity cannot bypass an old uncertain reservation of the same physical GPU.
CREATE UNIQUE INDEX gpu_exclusive_uuid_idx ON gpu_leases(gpu_uuid) WHERE state IN ('held','recovery_required');

-- Required shared repository fields; later ownership is structurally unavailable.
ALTER TABLE model_versions ADD COLUMN kind varchar(24) NOT NULL DEFAULT 'approved_base' CHECK(kind='approved_base');
ALTER TABLE model_versions ADD CONSTRAINT model_version_kind_catalog_identity UNIQUE(tenant_id,workspace_id,id,kind,catalog_digest);
ALTER TABLE deployments ADD COLUMN model_kind varchar(20) NOT NULL DEFAULT 'approved_base' CHECK(model_kind='approved_base');
ALTER TABLE deployments ADD COLUMN catalog_digest varchar(64) NOT NULL CHECK(catalog_digest ~ '^[a-f0-9]{64}$');
ALTER TABLE deployments ADD COLUMN serving_recipe_sha256 varchar(64) CHECK(serving_recipe_sha256 IS NULL);
ALTER TABLE deployments ADD CONSTRAINT deployment_model_catalog_scope FOREIGN KEY(tenant_id,workspace_id,model_id,model_kind,catalog_digest) REFERENCES model_versions(tenant_id,workspace_id,id,kind,catalog_digest);
-- The selected repository names an owner identity; no training/preparation owner is admitted.
ALTER TABLE gpu_leases ADD COLUMN id uuid NOT NULL UNIQUE;
ALTER TABLE gpu_leases ADD CONSTRAINT gpu_lease_identity_check CHECK(id=deployment_id);
ALTER TABLE managed_nodes DROP CONSTRAINT managed_nodes_capabilities_check;
ALTER TABLE managed_nodes ADD CONSTRAINT managed_nodes_capabilities_check CHECK(
 (execution_profile='read_only' AND capabilities='["collect_facts.v1"]'::jsonb) OR
 (execution_profile='systemd_components' AND capabilities='["collect_facts.v1","node_exporter.install.v1","node_exporter.start.v1","node_exporter.stop.v1","node_exporter.remove.v1","node_exporter.observe.v1"]'::jsonb) OR
 (execution_profile='systemd_maintenance' AND capabilities='["collect_facts.v1","node_exporter.install.v1","node_exporter.start.v1","node_exporter.stop.v1","node_exporter.remove.v1","node_exporter.observe.v1","node_exporter.configure.v1","node_exporter.restart.v1","node_exporter.upgrade.v1","node_exporter.rollback.v1","node_exporter.logs.v1"]'::jsonb) OR
 (execution_profile='docker_inference' AND capabilities='["collect_facts.v1","inference.deploy.v1","inference.stop.v1","inference.remove.v1","inference.observe.v1","inference.verify.v1"]'::jsonb));

INSERT INTO schema_migrations(version,checksum) VALUES(11,'50e93d068fc0eb2b145990f93f6e0086f7971e018516aafefa05aded7e88c1cd');
-- Exact source: services/control/migrations/postgresql/000012_gateway.up.sql
-- M07 cumulative entry12; existing M02 membership authority is retained byte-for-byte.
-- M07 scoped gateway authority, immutable intents and metadata-only usage receipts.
CREATE TABLE gateway_instances (
 client_origin text NOT NULL CHECK (length(client_origin) BETWEEN 9 AND 2048),
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 node_id uuid NOT NULL,
 name varchar(100) NOT NULL,
 control_origin_sha256 varchar(64) NOT NULL CHECK(control_origin_sha256 ~ '^[a-f0-9]{64}$'),
 credential_hash bytea CHECK(credential_hash IS NULL OR octet_length(credential_hash)=32),
 created_by uuid NOT NULL REFERENCES users(id),
 requested_epoch bigint NOT NULL CHECK(requested_epoch>=0),
 idempotency_key uuid NOT NULL,
 fingerprint bytea NOT NULL CHECK(octet_length(fingerprint)=32),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 revoked_at timestamptz,
 revoke_reason varchar(500),
 authority_invalidated_at timestamptz,
 UNIQUE(tenant_id,workspace_id,id),
 UNIQUE(tenant_id,workspace_id,node_id,id),
 UNIQUE(tenant_id,workspace_id,created_by,idempotency_key),
 FOREIGN KEY(tenant_id,workspace_id,node_id) REFERENCES managed_nodes(tenant_id,workspace_id,id),
 CHECK(revoked_at IS NULL OR (revoke_reason IS NOT NULL AND length(revoke_reason)>0)),
 CHECK(credential_hash IS NOT NULL OR authority_invalidated_at IS NOT NULL)
);
CREATE UNIQUE INDEX gateway_one_active_node_idx ON gateway_instances(node_id) WHERE revoked_at IS NULL AND authority_invalidated_at IS NULL;
CREATE TABLE model_aliases (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 node_id uuid NOT NULL,
 gateway_id uuid NOT NULL,
 name varchar(64) NOT NULL CHECK(name ~ '^[a-z][a-z0-9.-]{0,63}$'),
 deployment_id uuid NOT NULL,
 deployment_revision bigint NOT NULL CHECK(deployment_revision BETWEEN 1 AND 9007199254740991),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9007199254740991),
 created_by uuid NOT NULL REFERENCES users(id),
 idempotency_key uuid NOT NULL,
 fingerprint bytea NOT NULL CHECK(octet_length(fingerprint)=32),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 disabled_at timestamptz,
 UNIQUE(tenant_id,workspace_id,id),
 UNIQUE(tenant_id,workspace_id,name),
 UNIQUE(tenant_id,workspace_id,created_by,idempotency_key),
 FOREIGN KEY(tenant_id,workspace_id,node_id,gateway_id) REFERENCES gateway_instances(tenant_id,workspace_id,node_id,id),
 FOREIGN KEY(tenant_id,workspace_id,node_id,deployment_id) REFERENCES deployments(tenant_id,workspace_id,node_id,id)
);
CREATE TABLE model_alias_history (
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 alias_id uuid NOT NULL,
 revision bigint NOT NULL CHECK(revision BETWEEN 1 AND 9007199254740991),
 action varchar(16) NOT NULL CHECK(action IN ('published','disabled')),
 actor_user_id uuid NOT NULL REFERENCES users(id),
 reason varchar(500) NOT NULL CHECK(length(reason)>0),
 occurred_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(tenant_id,workspace_id,alias_id,revision),
 FOREIGN KEY(tenant_id,workspace_id,alias_id) REFERENCES model_aliases(tenant_id,workspace_id,id)
);
CREATE TABLE api_keys (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 alias_id uuid NOT NULL,
 label varchar(100) NOT NULL,
 secret_hash bytea NOT NULL CHECK(octet_length(secret_hash)=32),
 created_by uuid NOT NULL REFERENCES users(id),
 requested_epoch bigint NOT NULL CHECK(requested_epoch>=0),
 membership_authority uuid NOT NULL,
 idempotency_key uuid NOT NULL,
 fingerprint bytea NOT NULL CHECK(octet_length(fingerprint)=32),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 expires_at timestamptz NOT NULL,
 last_used_at timestamptz,
 revoked_at timestamptz,
 revoke_reason varchar(500),
 UNIQUE(tenant_id,workspace_id,id),
 UNIQUE(tenant_id,workspace_id,created_by,idempotency_key),
 FOREIGN KEY(tenant_id,workspace_id,alias_id) REFERENCES model_aliases(tenant_id,workspace_id,id),
 CHECK(expires_at>created_at),
 CHECK(revoked_at IS NULL OR (revoke_reason IS NOT NULL AND length(revoke_reason)>0))
);
CREATE INDEX api_key_owner_idx ON api_keys(tenant_id,workspace_id,created_by,created_at DESC,id DESC);
CREATE TABLE gateway_requests (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 node_id uuid NOT NULL,
 gateway_id uuid NOT NULL,
 api_key_id uuid NOT NULL,
 actor_user_id uuid NOT NULL REFERENCES users(id),
 actor_epoch bigint NOT NULL CHECK(actor_epoch>=0),
 alias_id uuid NOT NULL,
 alias_revision bigint NOT NULL,
 deployment_id uuid NOT NULL,
 deployment_revision bigint NOT NULL CHECK(deployment_revision BETWEEN 1 AND 9007199254740991),
 maximum_output_tokens integer NOT NULL CHECK(maximum_output_tokens BETWEEN 1 AND 512),
 maximum_context_tokens integer NOT NULL CHECK(maximum_context_tokens BETWEEN 1 AND 65536),
 maximum_request_seconds integer NOT NULL CHECK(maximum_request_seconds BETWEEN 1 AND 300),
 stream boolean NOT NULL,
 state varchar(16) NOT NULL CHECK(state IN ('admitted','reported','reconciled')),
 capacity_held boolean NOT NULL DEFAULT true,
 admitted_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 deadline_at timestamptz NOT NULL,
 resolved_at timestamptz,
 outcome jsonb CHECK(outcome IS NULL OR (jsonb_typeof(outcome)='object' AND octet_length(outcome::text)<=4096 AND outcome ?& ARRAY['request_id','status','error_code','usage','elapsed_milliseconds','output_started']::text[] AND outcome - ARRAY['request_id','status','error_code','usage','elapsed_milliseconds','output_started']::text[]='{}'::jsonb AND outcome->>'request_id'=id::text)),
 outcome_sha256 varchar(64) CHECK(outcome_sha256 IS NULL OR outcome_sha256 ~ '^[a-f0-9]{64}$'),
 reported_at timestamptz,
 reconciled_by uuid REFERENCES users(id),
 reconciled_at timestamptz,
 reconciliation_reason varchar(500),
 UNIQUE(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,node_id,gateway_id) REFERENCES gateway_instances(tenant_id,workspace_id,node_id,id),
 FOREIGN KEY(tenant_id,workspace_id,api_key_id) REFERENCES api_keys(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,alias_id,alias_revision) REFERENCES model_alias_history(tenant_id,workspace_id,alias_id,revision),
 FOREIGN KEY(tenant_id,workspace_id,node_id,deployment_id) REFERENCES deployments(tenant_id,workspace_id,node_id,id),
 CHECK(deadline_at>admitted_at),
 CHECK(state<>'admitted' OR capacity_held),
 CHECK(state<>'reconciled' OR NOT capacity_held),
 CHECK(outcome IS NULL OR reconciled_at IS NOT NULL OR (outcome->>'status' IN ('succeeded','client_cancelled','upstream_timeout','upstream_error','invalid_response','gateway_shutdown') AND COALESCE(outcome->>'error_code','')<>'gateway_interrupted') OR capacity_held),
 CHECK((state='admitted' AND resolved_at IS NULL AND outcome IS NULL AND outcome_sha256 IS NULL AND reported_at IS NULL AND reconciled_at IS NULL)
    OR (state='reported' AND resolved_at IS NOT NULL AND outcome IS NOT NULL AND outcome_sha256 IS NOT NULL AND reported_at IS NOT NULL)
    OR (state='reconciled' AND resolved_at IS NOT NULL AND outcome IS NULL AND outcome_sha256 IS NULL AND reported_at IS NULL AND reconciled_at IS NOT NULL)),
 CHECK((reconciled_at IS NULL AND reconciled_by IS NULL AND reconciliation_reason IS NULL)
    OR (reconciled_at IS NOT NULL AND reconciled_by IS NOT NULL AND reconciliation_reason IS NOT NULL AND length(reconciliation_reason)>0))
);
CREATE INDEX gateway_request_held_deployment_idx ON gateway_requests(deployment_id) WHERE capacity_held;
CREATE INDEX gateway_request_owner_idx ON gateway_requests(tenant_id,workspace_id,actor_user_id,admitted_at DESC,id DESC);
CREATE INDEX gateway_request_instance_idx ON gateway_requests(tenant_id,workspace_id,gateway_id,admitted_at DESC,id DESC);
CREATE TABLE gateway_recoveries (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 request_id uuid NOT NULL,
 requested_by uuid NOT NULL REFERENCES users(id),
 reason varchar(500) NOT NULL CHECK(length(reason)>0),
 idempotency_key uuid NOT NULL,
 fingerprint bytea NOT NULL CHECK(octet_length(fingerprint)=32),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 expires_at timestamptz NOT NULL,
 superseded_at timestamptz,
 invalidated_at timestamptz,
 fence_receipt jsonb CHECK(fence_receipt IS NULL OR (jsonb_typeof(fence_receipt)='object' AND octet_length(fence_receipt::text)<=4096)),
 fence_receipt_sha256 varchar(64) CHECK(fence_receipt_sha256 IS NULL OR fence_receipt_sha256 ~ '^[a-f0-9]{64}$'),
 fence_accepted_at timestamptz,
 fence_accepted_by uuid REFERENCES users(id),
 confirmed_at timestamptz,
 confirmation_fingerprint bytea CHECK(confirmation_fingerprint IS NULL OR octet_length(confirmation_fingerprint)=32),
 confirmed_deployment_revision bigint CHECK(confirmed_deployment_revision IS NULL OR confirmed_deployment_revision BETWEEN 1 AND 9007199254740991),
 UNIQUE(tenant_id,workspace_id,request_id,id),
 UNIQUE(tenant_id,workspace_id,request_id,requested_by,idempotency_key),
 FOREIGN KEY(tenant_id,workspace_id,request_id) REFERENCES gateway_requests(tenant_id,workspace_id,id),
 CHECK(expires_at>created_at),
 CHECK((fence_receipt IS NULL AND fence_receipt_sha256 IS NULL AND fence_accepted_at IS NULL AND fence_accepted_by IS NULL)
    OR (fence_receipt IS NOT NULL AND fence_receipt_sha256 IS NOT NULL AND fence_accepted_at IS NOT NULL AND fence_accepted_by IS NOT NULL)),
 CHECK((confirmed_at IS NULL AND confirmation_fingerprint IS NULL AND confirmed_deployment_revision IS NULL)
    OR (confirmed_at IS NOT NULL AND fence_accepted_at IS NOT NULL AND confirmation_fingerprint IS NOT NULL AND confirmed_deployment_revision IS NOT NULL))
);
ALTER TABLE gateway_requests ADD COLUMN active_recovery_id uuid;
ALTER TABLE gateway_requests ADD FOREIGN KEY(tenant_id,workspace_id,id,active_recovery_id) REFERENCES gateway_recoveries(tenant_id,workspace_id,request_id,id);
CREATE INDEX gateway_recovery_request_idx ON gateway_recoveries(tenant_id,workspace_id,request_id,created_at DESC,id DESC);

-- Current shared alias repository retains immutable target snapshots only; no cutover owner.
CREATE TABLE model_alias_target_versions (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 alias_id uuid NOT NULL,
 revision bigint NOT NULL CHECK(revision BETWEEN 1 AND 9007199254740991),
 source_revision bigint CHECK(source_revision IS NULL),
 action varchar(20) NOT NULL CHECK(action IN ('published','disabled')),
 deployment_id uuid NOT NULL,
 deployment_revision bigint NOT NULL CHECK(deployment_revision BETWEEN 1 AND 9007199254740991),
 model_id uuid NOT NULL,
 model_kind varchar(20) NOT NULL CHECK(model_kind='approved_base'),
 catalog_digest char(64) NOT NULL CHECK(catalog_digest~'^[a-f0-9]{64}$'),
 serving_recipe_sha256 char(64),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,workspace_id,alias_id,revision),
 FOREIGN KEY(tenant_id,workspace_id,alias_id,revision) REFERENCES model_alias_history(tenant_id,workspace_id,alias_id,revision),
 FOREIGN KEY(tenant_id,workspace_id,alias_id,source_revision) REFERENCES model_alias_target_versions(tenant_id,workspace_id,alias_id,revision),
 CHECK((action='rolled_back' AND source_revision IS NOT NULL AND source_revision<revision AND source_revision>=1) OR (action<>'rolled_back' AND source_revision IS NULL)),
 FOREIGN KEY(tenant_id,workspace_id,deployment_id) REFERENCES deployments(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,model_id,model_kind,catalog_digest) REFERENCES model_versions(tenant_id,workspace_id,id,kind,catalog_digest),
 CHECK((model_kind='approved_base' AND serving_recipe_sha256 IS NULL) OR (model_kind='merged_lora' AND serving_recipe_sha256 IS NOT NULL AND serving_recipe_sha256~'^[a-f0-9]{64}$'))
);

CREATE INDEX model_alias_target_page_idx ON model_alias_target_versions(tenant_id,workspace_id,alias_id,created_at DESC,id DESC);
CREATE FUNCTION protect_alias_cutover_record() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'ALIAS_TARGET_HISTORY_IMMUTABLE';END;$$;
CREATE TRIGGER model_alias_target_immutable BEFORE UPDATE OR DELETE ON model_alias_target_versions FOR EACH ROW EXECUTE FUNCTION protect_alias_cutover_record();
CREATE TRIGGER model_alias_history_immutable BEFORE UPDATE OR DELETE ON model_alias_history FOR EACH ROW EXECUTE FUNCTION protect_alias_cutover_record();
CREATE FUNCTION protect_alias_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.node_id,NEW.gateway_id,NEW.name,NEW.created_by,NEW.idempotency_key,NEW.fingerprint,NEW.created_at)
 IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.node_id,OLD.gateway_id,OLD.name,OLD.created_by,OLD.idempotency_key,OLD.fingerprint,OLD.created_at)
 THEN RAISE EXCEPTION 'MODEL_ALIAS_IDENTITY_IMMUTABLE';END IF;
 RETURN NEW;
END;$$;
CREATE TRIGGER model_alias_identity_immutable BEFORE UPDATE ON model_aliases FOR EACH ROW EXECUTE FUNCTION protect_alias_identity();

CREATE FUNCTION require_alias_target_snapshot() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM model_aliases a JOIN deployments d ON d.tenant_id=a.tenant_id AND d.workspace_id=a.workspace_id AND d.node_id=a.node_id AND d.id=a.deployment_id
 WHERE a.tenant_id=NEW.tenant_id AND a.workspace_id=NEW.workspace_id AND a.id=NEW.alias_id AND a.revision=NEW.revision AND a.deployment_revision=NEW.deployment_revision
 AND d.id=NEW.deployment_id AND d.model_id=NEW.model_id AND d.model_kind=NEW.model_kind AND d.catalog_digest=NEW.catalog_digest AND d.serving_recipe_sha256 IS NOT DISTINCT FROM NEW.serving_recipe_sha256)
 THEN RAISE EXCEPTION 'ALIAS_CURRENT_TARGET_SNAPSHOT_REQUIRED';END IF;
 RETURN NEW;
END;$$;
CREATE TRIGGER model_alias_current_target_snapshot BEFORE INSERT ON model_alias_target_versions FOR EACH ROW EXECUTE FUNCTION require_alias_target_snapshot();

INSERT INTO schema_migrations(version,checksum) VALUES(12,'179c0c58e295575dc88c956b96742e0458a490568626b3edaf36991b1c5beb22');
-- Exact source: services/control/migrations/postgresql/000013_datasets.up.sql
-- M08 is in progress; schema7 and all earlier migrations remain frozen.
CREATE TABLE datasets (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 name varchar(100) NOT NULL CHECK(length(name)>0),
 name_key varchar(200) NOT NULL CHECK(length(name_key)>0),
 origin varchar(2000) NOT NULL CHECK(length(origin)>0),
 license varchar(500) NOT NULL CHECK(length(license)>0),
 rights_asserted boolean NOT NULL CHECK(rights_asserted),
 created_by uuid NOT NULL REFERENCES users(id),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9007199254740991),
 next_version bigint NOT NULL DEFAULT 1 CHECK(next_version BETWEEN 1 AND 9007199254740991),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 retired_at timestamptz,
 retire_reason varchar(500),
 creation_key uuid NOT NULL,
 creation_fingerprint bytea NOT NULL CHECK(octet_length(creation_fingerprint)=32),
 UNIQUE(tenant_id,workspace_id,id),
 UNIQUE(tenant_id,workspace_id,created_by,creation_key),
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id),
 CHECK((retired_at IS NULL AND retire_reason IS NULL) OR (retired_at IS NOT NULL AND length(retire_reason)>0))
);
CREATE UNIQUE INDEX dataset_active_name_idx ON datasets(tenant_id,workspace_id,name_key) WHERE retired_at IS NULL;
CREATE INDEX dataset_workspace_page_idx ON datasets(tenant_id,workspace_id,created_at DESC,id DESC);
CREATE TABLE dataset_drafts (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 dataset_id uuid NOT NULL,
 created_by uuid NOT NULL REFERENCES users(id),
 requested_audience varchar(8) NOT NULL CHECK(requested_audience IN ('portal','admin')),
 requested_epoch bigint NOT NULL CHECK(requested_epoch>=0),
 membership_authority uuid,
 parent_version_id uuid,
 source_object_id uuid NOT NULL,
 source_name varchar(255) NOT NULL CHECK(length(source_name)>0),
 source_sha256 varchar(64) NOT NULL CHECK(source_sha256 ~ '^[a-f0-9]{64}$'),
 expected_bytes bigint NOT NULL CHECK(expected_bytes BETWEEN 1 AND 67108864),
 source_received_at timestamptz,
 upload_attempt_id uuid,
 upload_expires_at timestamptz,
 state varchar(16) NOT NULL CHECK(state IN ('uploading','validating','editing','publishing','published','failed','cancelled')),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9007199254740991),
 total_rows integer NOT NULL DEFAULT 0 CHECK(total_rows BETWEEN 0 AND 10000),
 invalid_rows integer NOT NULL DEFAULT 0 CHECK(invalid_rows BETWEEN 0 AND 10001),
 validation_completed_at timestamptz,
 fatal_source_error boolean NOT NULL DEFAULT false,
 validation_errors jsonb NOT NULL DEFAULT '[]'::jsonb CHECK(jsonb_typeof(validation_errors)='array' AND octet_length(validation_errors::text)<=8388608),
 errors_truncated boolean NOT NULL DEFAULT false,
 error_code varchar(80),
 error_line_number integer CHECK(error_line_number BETWEEN 0 AND 10001),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 creation_key uuid NOT NULL,
 creation_fingerprint bytea NOT NULL CHECK(octet_length(creation_fingerprint)=32),
 UNIQUE(tenant_id,workspace_id,dataset_id,id),
 UNIQUE(tenant_id,workspace_id,id),
 UNIQUE(tenant_id,workspace_id,dataset_id,created_by,creation_key),
 FOREIGN KEY(tenant_id,workspace_id,dataset_id) REFERENCES datasets(tenant_id,workspace_id,id),
 CHECK(requested_audience='admin' OR membership_authority IS NOT NULL),
 CHECK((upload_attempt_id IS NULL)=(upload_expires_at IS NULL)),
 CHECK(source_received_at IS NOT NULL OR state IN ('uploading','failed','cancelled'))
);
CREATE INDEX dataset_draft_page_idx ON dataset_drafts(tenant_id,workspace_id,dataset_id,created_at DESC,id DESC);
CREATE TABLE dataset_rows (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 dataset_id uuid NOT NULL,
 draft_id uuid NOT NULL,
 line_number integer NOT NULL CHECK(line_number BETWEEN 1 AND 10000),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9007199254740991),
 source_id text CHECK(source_id IS NULL OR octet_length(source_id) BETWEEN 1 AND 512),
 prompt text CHECK(prompt IS NULL OR octet_length(prompt) BETWEEN 1 AND 32768),
 completion text CHECK(completion IS NULL OR octet_length(completion) BETWEEN 1 AND 32768),
 error_code varchar(80),
 raw_preview text NOT NULL DEFAULT '' CHECK(octet_length(raw_preview)<=4096),
 preview_replaced boolean NOT NULL DEFAULT false,
 deleted boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,workspace_id,dataset_id,draft_id,id),
 UNIQUE(draft_id,line_number),
 FOREIGN KEY(tenant_id,workspace_id,dataset_id,draft_id) REFERENCES dataset_drafts(tenant_id,workspace_id,dataset_id,id),
 CHECK((error_code IS NULL AND source_id IS NOT NULL AND prompt IS NOT NULL AND completion IS NOT NULL)
    OR (error_code IS NOT NULL AND source_id IS NULL AND prompt IS NULL AND completion IS NULL))
);
CREATE INDEX dataset_row_page_idx ON dataset_rows(tenant_id,workspace_id,draft_id,line_number,id) WHERE NOT deleted;
CREATE TABLE dataset_row_history (
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 dataset_id uuid NOT NULL,
 draft_id uuid NOT NULL,
 row_id uuid NOT NULL,
 revision bigint NOT NULL CHECK(revision BETWEEN 1 AND 9007199254740991),
 actor_user_id uuid REFERENCES users(id),
 action varchar(16) NOT NULL CHECK(action IN ('imported','appended','replaced','deleted')),
 content_sha256 varchar(64) CHECK(content_sha256 IS NULL OR content_sha256 ~ '^[a-f0-9]{64}$'),
 reason varchar(500) NOT NULL CHECK(length(reason)>0),
 occurred_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(tenant_id,workspace_id,draft_id,row_id,revision),
 FOREIGN KEY(tenant_id,workspace_id,dataset_id,draft_id,row_id) REFERENCES dataset_rows(tenant_id,workspace_id,dataset_id,draft_id,id)
);
CREATE TABLE dataset_jobs (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 dataset_id uuid NOT NULL,
 draft_id uuid NOT NULL,
 kind varchar(8) NOT NULL CHECK(kind IN ('import','publish')),
 requested_by uuid NOT NULL REFERENCES users(id),
 requested_audience varchar(8) NOT NULL CHECK(requested_audience IN ('portal','admin')),
 requested_epoch bigint NOT NULL CHECK(requested_epoch>=0),
 membership_authority uuid,
 reason varchar(500) NOT NULL CHECK(length(reason)>0),
 expected_revision bigint NOT NULL CHECK(expected_revision BETWEEN 1 AND 9007199254740991),
 state varchar(12) NOT NULL CHECK(state IN ('pending','running','succeeded','failed','cancelled')),
 version_id uuid,
 input_sha256 varchar(64) CHECK(input_sha256 IS NULL OR input_sha256 ~ '^[a-f0-9]{64}$'),
 input_policy jsonb NOT NULL CHECK(jsonb_typeof(input_policy)='object' AND octet_length(input_policy::text)<=16384),
 split_seed varchar(32) CHECK(split_seed IS NULL OR split_seed ~ '^[a-f0-9]{32}$'),
 validation_basis_points integer CHECK(validation_basis_points BETWEEN 100 AND 5000),
 attempt_count integer NOT NULL DEFAULT 0 CHECK(attempt_count BETWEEN 0 AND 5),
 attempt_id uuid,
 lease_expires_at timestamptz,
 cancel_requested_at timestamptz,
 cancel_requested_by uuid REFERENCES users(id),
 error_code varchar(80),
 error_line_number integer CHECK(error_line_number BETWEEN 0 AND 10001),
 retryable boolean NOT NULL DEFAULT false,
 retry_of uuid,
 not_before timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 completed_at timestamptz,
 creation_key uuid NOT NULL,
 creation_fingerprint bytea NOT NULL CHECK(octet_length(creation_fingerprint)=32),
 UNIQUE(tenant_id,workspace_id,dataset_id,draft_id,id),
 UNIQUE(tenant_id,workspace_id,id),
 UNIQUE(tenant_id,workspace_id,dataset_id,draft_id,requested_by,creation_key),
 FOREIGN KEY(tenant_id,workspace_id,dataset_id,draft_id) REFERENCES dataset_drafts(tenant_id,workspace_id,dataset_id,id),
 FOREIGN KEY(tenant_id,workspace_id,retry_of) REFERENCES dataset_jobs(tenant_id,workspace_id,id),
 CHECK(requested_audience='admin' OR membership_authority IS NOT NULL),
 CHECK((kind='publish' AND version_id IS NOT NULL AND split_seed IS NOT NULL AND validation_basis_points IS NOT NULL) OR (kind='import' AND version_id IS NULL AND split_seed IS NULL AND validation_basis_points IS NULL)),
 CHECK((state='running' AND attempt_id IS NOT NULL AND lease_expires_at IS NOT NULL AND completed_at IS NULL) OR (state='pending' AND lease_expires_at IS NULL AND completed_at IS NULL) OR (state IN ('succeeded','failed','cancelled') AND lease_expires_at IS NULL AND completed_at IS NOT NULL)),
 CHECK((cancel_requested_at IS NULL)=(cancel_requested_by IS NULL))
);
CREATE INDEX dataset_job_claim_idx ON dataset_jobs(state,not_before,created_at,id) WHERE state IN ('pending','running');
CREATE TABLE dataset_job_attempts (
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 dataset_id uuid NOT NULL,
 draft_id uuid NOT NULL,
 job_id uuid NOT NULL,
 id uuid NOT NULL,
 attempt_number integer NOT NULL CHECK(attempt_number BETWEEN 1 AND 5),
 state varchar(12) NOT NULL CHECK(state IN ('running','succeeded','failed','cancelled','expired')),
 started_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 finished_at timestamptz,
 error_code varchar(80),
 memory_enforcement varchar(100),
 PRIMARY KEY(tenant_id,workspace_id,job_id,id),
 UNIQUE(job_id,attempt_number),
 FOREIGN KEY(tenant_id,workspace_id,dataset_id,draft_id,job_id) REFERENCES dataset_jobs(tenant_id,workspace_id,dataset_id,draft_id,id),
 CHECK((state='running' AND finished_at IS NULL) OR (state<>'running' AND finished_at IS NOT NULL))
);
CREATE TABLE dataset_versions (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 dataset_id uuid NOT NULL,
 draft_id uuid NOT NULL,
 job_id uuid NOT NULL,
 attempt_id uuid NOT NULL,
 ordinal bigint NOT NULL CHECK(ordinal BETWEEN 1 AND 9007199254740991),
 parent_version_id uuid,
 published_by uuid NOT NULL REFERENCES users(id),
 published_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 raw_source_sha256 varchar(64) NOT NULL CHECK(raw_source_sha256 ~ '^[a-f0-9]{64}$'),
 effective_source_sha256 varchar(64) NOT NULL CHECK(effective_source_sha256 ~ '^[a-f0-9]{64}$'),
 manifest_sha256 varchar(64) NOT NULL CHECK(manifest_sha256 ~ '^[a-f0-9]{64}$'),
 manifest jsonb NOT NULL CHECK(jsonb_typeof(manifest)='object' AND octet_length(manifest::text)<=8388608),
 row_mapping_sha256 varchar(64) NOT NULL CHECK(row_mapping_sha256 ~ '^[a-f0-9]{64}$'),
 retired_at timestamptz,
 retire_reason varchar(500),
 UNIQUE(tenant_id,workspace_id,dataset_id,id),
 UNIQUE(tenant_id,workspace_id,dataset_id,draft_id,id),
 UNIQUE(dataset_id,ordinal),
 UNIQUE(job_id),
 FOREIGN KEY(tenant_id,workspace_id,dataset_id,draft_id,job_id) REFERENCES dataset_jobs(tenant_id,workspace_id,dataset_id,draft_id,id),
 FOREIGN KEY(tenant_id,workspace_id,job_id,attempt_id) REFERENCES dataset_job_attempts(tenant_id,workspace_id,job_id,id),
 FOREIGN KEY(tenant_id,workspace_id,dataset_id,parent_version_id) REFERENCES dataset_versions(tenant_id,workspace_id,dataset_id,id),
 CHECK((retired_at IS NULL AND retire_reason IS NULL) OR (retired_at IS NOT NULL AND length(retire_reason)>0))
);
CREATE INDEX dataset_version_page_idx ON dataset_versions(tenant_id,workspace_id,dataset_id,published_at DESC,id DESC);
ALTER TABLE dataset_drafts ADD COLUMN current_job_id uuid;
ALTER TABLE dataset_drafts ADD COLUMN published_version_id uuid;
ALTER TABLE dataset_drafts ADD FOREIGN KEY(tenant_id,workspace_id,dataset_id,id,current_job_id) REFERENCES dataset_jobs(tenant_id,workspace_id,dataset_id,draft_id,id);
ALTER TABLE dataset_drafts ADD FOREIGN KEY(tenant_id,workspace_id,dataset_id,id,published_version_id) REFERENCES dataset_versions(tenant_id,workspace_id,dataset_id,draft_id,id);
ALTER TABLE dataset_drafts ADD FOREIGN KEY(tenant_id,workspace_id,dataset_id,parent_version_id) REFERENCES dataset_versions(tenant_id,workspace_id,dataset_id,id);

INSERT INTO schema_migrations(version,checksum) VALUES(13,'25f65a1c1824b863ae6ca42f7123c025b101dcc903800cd4c9ad89ebd4f332f1');
-- Exact source: services/control/migrations/postgresql/000014_dataset_retirement_reason.up.sql
-- Preserve frozen migration8. CHECK accepts SQL UNKNOWN, so require the
-- conditionally mandatory reason explicitly. Existing invalid rows refuse the
-- entire migration transaction; no operator reason is fabricated or backfilled.
ALTER TABLE datasets ADD CONSTRAINT dataset_retirement_reason_required
 CHECK((retired_at IS NULL AND retire_reason IS NULL)
    OR (retired_at IS NOT NULL AND retire_reason IS NOT NULL AND length(retire_reason)>0));
ALTER TABLE dataset_versions ADD CONSTRAINT dataset_version_retirement_reason_required
 CHECK((retired_at IS NULL AND retire_reason IS NULL)
    OR (retired_at IS NOT NULL AND retire_reason IS NOT NULL AND length(retire_reason)>0));

INSERT INTO schema_migrations(version,checksum) VALUES(14,'662444cdb343b16c610af6ee82bfcc8faca8d9822feff572ea4b2d93ee8b68cc');
-- Exact source: services/control/migrations/postgresql/000015_training.up.sql
-- M09 cumulative forward extension; the accepted fourteen-entry prefix is immutable.
ALTER TABLE node_join_tokens DROP CONSTRAINT node_join_tokens_execution_profile_check;
ALTER TABLE node_join_tokens ADD CONSTRAINT node_join_tokens_execution_profile_check CHECK(execution_profile IN ('read_only','systemd_components','systemd_maintenance','docker_inference','docker_ai'));
ALTER TABLE managed_nodes DROP CONSTRAINT managed_nodes_execution_profile_check;
ALTER TABLE managed_nodes ADD CONSTRAINT managed_nodes_execution_profile_check CHECK(execution_profile IN ('read_only','systemd_components','systemd_maintenance','docker_inference','docker_ai'));
ALTER TABLE node_tasks DROP CONSTRAINT node_tasks_operation_check;
ALTER TABLE node_tasks ADD CONSTRAINT node_tasks_operation_check CHECK(operation IN ('collect_facts.v1','node_exporter.install.v1','node_exporter.start.v1','node_exporter.stop.v1','node_exporter.remove.v1','node_exporter.observe.v1','node_exporter.configure.v1','node_exporter.restart.v1','node_exporter.upgrade.v1','node_exporter.rollback.v1','node_exporter.logs.v1','inference.deploy.v1','inference.stop.v1','inference.remove.v1','inference.observe.v1','inference.verify.v1','training.run.v1'));
-- Typed training commands carry frozen input/resource identities, never source data or arbitrary code.
ALTER TABLE node_tasks DROP CONSTRAINT node_tasks_payload_check;
ALTER TABLE node_tasks ADD CONSTRAINT node_tasks_payload_check CHECK(jsonb_typeof(payload)='object' AND octet_length(payload::text)<=32768);

CREATE TABLE training_jobs (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 node_id uuid NOT NULL,
 current_node_id uuid NOT NULL,
 model_id uuid NOT NULL,
 dataset_id uuid NOT NULL,
 dataset_version_id uuid NOT NULL,
 gpu_uuid varchar(64) NOT NULL CHECK(gpu_uuid ~ '^GPU-[a-f0-9-]{36}$'),
 configuration jsonb NOT NULL CHECK(jsonb_typeof(configuration)='object' AND octet_length(configuration::text)<=16384),
 configuration_sha256 varchar(64) NOT NULL CHECK(configuration_sha256 ~ '^[a-f0-9]{64}$'),
 execution_recipe jsonb NOT NULL CHECK(jsonb_typeof(execution_recipe)='object' AND octet_length(execution_recipe::text)<=32768),
 dataset_manifest_sha256 varchar(64) NOT NULL CHECK(dataset_manifest_sha256 ~ '^[a-f0-9]{64}$'),
 model_catalog_sha256 varchar(64) NOT NULL CHECK(model_catalog_sha256 ~ '^[a-f0-9]{64}$'),
 created_by uuid NOT NULL REFERENCES users(id),
 requested_by uuid NOT NULL REFERENCES users(id),
 requested_audience varchar(8) NOT NULL CHECK(requested_audience IN ('portal','admin')),
 requested_epoch bigint NOT NULL CHECK(requested_epoch>=0),
 membership_authority uuid,
 reason varchar(500) NOT NULL CHECK(length(btrim(reason))>0),
 creation_key uuid NOT NULL,
 creation_fingerprint bytea NOT NULL CHECK(octet_length(creation_fingerprint)=32),
 state varchar(24) NOT NULL CHECK(state IN ('queued','preparing','running','cancelling','succeeded','cancelled','failed','recovery_required')),
 wait_code varchar(80),
 next_dispatch_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 revision bigint NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9007199254740991),
 attempt_number integer NOT NULL DEFAULT 0 CHECK(attempt_number BETWEEN 0 AND 1000),
 current_attempt_id uuid,
 accepted_artifact_id uuid,
 pending_recovery_id uuid,
 cancel_requested_at timestamptz,
 error_code varchar(80),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 finished_at timestamptz,
 UNIQUE(tenant_id,workspace_id,id),
 UNIQUE(tenant_id,workspace_id,node_id,id),
 UNIQUE(tenant_id,workspace_id,created_by,creation_key),
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id),
 FOREIGN KEY(tenant_id,workspace_id,node_id) REFERENCES managed_nodes(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,current_node_id) REFERENCES managed_nodes(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,model_id) REFERENCES model_versions(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,dataset_id,dataset_version_id) REFERENCES dataset_versions(tenant_id,workspace_id,dataset_id,id),
 CHECK(requested_audience='admin' OR membership_authority IS NOT NULL)
);
CREATE INDEX training_job_scope_page_idx ON training_jobs(tenant_id,workspace_id,created_at DESC,id DESC);
CREATE INDEX training_job_queue_idx ON training_jobs(next_dispatch_at,created_at,id) WHERE state='queued';

CREATE TABLE training_attempts (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 job_id uuid NOT NULL,
 node_id uuid NOT NULL,
 task_id uuid,
 attempt_number integer NOT NULL CHECK(attempt_number BETWEEN 1 AND 1000),
 state varchar(24) NOT NULL CHECK(state IN ('pending','preparing','running','stopping','succeeded','cancelled','failed','recovery_required','stopped')),
 requested_by uuid NOT NULL REFERENCES users(id),
 requested_audience varchar(8) NOT NULL CHECK(requested_audience IN ('portal','admin')),
 requested_epoch bigint NOT NULL CHECK(requested_epoch>=0),
 membership_authority uuid,
 input_inventory jsonb NOT NULL CHECK(jsonb_typeof(input_inventory)='object' AND octet_length(input_inventory::text)<=65536),
 resume_checkpoint_id uuid,
 last_event_sequence bigint NOT NULL DEFAULT 0 CHECK(last_event_sequence BETWEEN 0 AND 1000000),
 last_global_step integer NOT NULL DEFAULT 0 CHECK(last_global_step BETWEEN 0 AND 1000),
 stop_token_hash bytea CHECK(stop_token_hash IS NULL OR octet_length(stop_token_hash)=32),
 outcome_sha256 varchar(64) CHECK(outcome_sha256 IS NULL OR outcome_sha256 ~ '^[a-f0-9]{64}$'),
 outcome jsonb CHECK(outcome IS NULL OR jsonb_typeof(outcome)='object' AND octet_length(outcome::text)<=16777216),
 physical_stop_receipt jsonb CHECK(physical_stop_receipt IS NULL OR jsonb_typeof(physical_stop_receipt)='object' AND octet_length(physical_stop_receipt::text)<=32768),
 physical_stop_sha256 varchar(64) CHECK(physical_stop_sha256 IS NULL OR physical_stop_sha256 ~ '^[a-f0-9]{64}$'),
 physical_stopped_at timestamptz,
 error_code varchar(80),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 started_at timestamptz,
 finished_at timestamptz,
 updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,workspace_id,job_id,id),
 UNIQUE(job_id,attempt_number),
 UNIQUE(task_id),
 FOREIGN KEY(tenant_id,workspace_id,job_id) REFERENCES training_jobs(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,node_id) REFERENCES managed_nodes(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,node_id,task_id) REFERENCES node_tasks(tenant_id,workspace_id,node_id,id),
 CHECK(requested_audience='admin' OR membership_authority IS NOT NULL),
 CHECK((physical_stop_receipt IS NULL)=(physical_stop_sha256 IS NULL)),
 CHECK((physical_stop_receipt IS NULL)=(physical_stopped_at IS NULL)),
 CHECK((outcome IS NULL)=(outcome_sha256 IS NULL))
);
CREATE UNIQUE INDEX training_attempt_one_uncleared_job_idx ON training_attempts(job_id) WHERE state IN ('pending','preparing','running','stopping','recovery_required') AND physical_stopped_at IS NULL;
CREATE INDEX training_attempt_scope_page_idx ON training_attempts(tenant_id,workspace_id,job_id,created_at DESC,id DESC);
ALTER TABLE training_jobs ADD CONSTRAINT training_job_current_attempt_fk FOREIGN KEY(tenant_id,workspace_id,id,current_attempt_id) REFERENCES training_attempts(tenant_id,workspace_id,job_id,id);

CREATE TABLE training_events (
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 job_id uuid NOT NULL,
 attempt_id uuid NOT NULL,
 sequence bigint NOT NULL CHECK(sequence BETWEEN 1 AND 1000000),
 kind varchar(16) NOT NULL CHECK(kind IN ('phase','metric','checkpoint')),
 global_step integer NOT NULL CHECK(global_step BETWEEN 0 AND 1000),
 payload jsonb NOT NULL CHECK(jsonb_typeof(payload)='object' AND octet_length(payload::text)<=16384),
 canonical_sha256 varchar(64) NOT NULL CHECK(canonical_sha256 ~ '^[a-f0-9]{64}$'),
 recorded_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(attempt_id,sequence),
 FOREIGN KEY(tenant_id,workspace_id,job_id,attempt_id) REFERENCES training_attempts(tenant_id,workspace_id,job_id,id)
);
CREATE INDEX training_event_job_page_idx ON training_events(tenant_id,workspace_id,job_id,attempt_id,sequence);
CREATE UNIQUE INDEX training_checkpoint_event_step_idx ON training_events(attempt_id,global_step) WHERE kind='checkpoint';

CREATE TABLE training_checkpoints (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 job_id uuid NOT NULL,
 attempt_id uuid NOT NULL,
 global_step integer NOT NULL CHECK(global_step BETWEEN 1 AND 1000),
 receipt_sha256 varchar(64) NOT NULL CHECK(receipt_sha256 ~ '^[a-f0-9]{64}$'),
 receipt jsonb NOT NULL CHECK(jsonb_typeof(receipt)='object' AND octet_length(receipt::text)<=1048576),
 file_inventory jsonb NOT NULL CHECK(jsonb_typeof(file_inventory)='object' AND octet_length(file_inventory::text)<=1048576),
 storage_key varchar(160) NOT NULL CHECK(storage_key ~ '^checkpoints/[a-f0-9-]{36}/[a-f0-9-]{36}/[a-f0-9-]{36}$'),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,workspace_id,job_id,id),
 UNIQUE(attempt_id,global_step),
 FOREIGN KEY(tenant_id,workspace_id,job_id,attempt_id) REFERENCES training_attempts(tenant_id,workspace_id,job_id,id)
);
CREATE INDEX training_checkpoint_scope_page_idx ON training_checkpoints(tenant_id,workspace_id,job_id,created_at DESC,id DESC);
ALTER TABLE training_attempts ADD CONSTRAINT training_attempt_resume_checkpoint_fk FOREIGN KEY(tenant_id,workspace_id,job_id,resume_checkpoint_id) REFERENCES training_checkpoints(tenant_id,workspace_id,job_id,id);

CREATE TABLE training_artifacts (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 job_id uuid NOT NULL,
 attempt_id uuid NOT NULL,
 manifest_sha256 varchar(64) NOT NULL CHECK(manifest_sha256 ~ '^[a-f0-9]{64}$'),
 manifest jsonb NOT NULL CHECK(jsonb_typeof(manifest)='object' AND octet_length(manifest::text)<=1048576),
 file_inventory jsonb NOT NULL CHECK(jsonb_typeof(file_inventory)='object' AND octet_length(file_inventory::text)<=1048576),
 storage_key varchar(160) NOT NULL CHECK(storage_key ~ '^artifacts/[a-f0-9-]{36}/[a-f0-9-]{36}/[a-f0-9-]{36}$'),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,workspace_id,job_id,id),
 UNIQUE(attempt_id),
 FOREIGN KEY(tenant_id,workspace_id,job_id,attempt_id) REFERENCES training_attempts(tenant_id,workspace_id,job_id,id)
);
ALTER TABLE training_jobs ADD CONSTRAINT training_job_accepted_artifact_fk FOREIGN KEY(tenant_id,workspace_id,id,accepted_artifact_id) REFERENCES training_artifacts(tenant_id,workspace_id,job_id,id);

CREATE TABLE training_recoveries (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 job_id uuid NOT NULL,
 previous_attempt_id uuid,
 next_attempt_id uuid,
 checkpoint_id uuid,
 previous_node_id uuid NOT NULL,
 next_node_id uuid NOT NULL,
 mode varchar(16) NOT NULL CHECK(mode IN ('checkpoint','restart')),
 requested_by uuid NOT NULL REFERENCES users(id),
 reason varchar(500) NOT NULL CHECK(length(btrim(reason))>0),
 creation_key uuid NOT NULL,
 creation_fingerprint bytea NOT NULL CHECK(octet_length(creation_fingerprint)=32),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,workspace_id,job_id,id),
 UNIQUE(job_id,creation_key),
 FOREIGN KEY(tenant_id,workspace_id,job_id) REFERENCES training_jobs(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,job_id,previous_attempt_id) REFERENCES training_attempts(tenant_id,workspace_id,job_id,id),
 FOREIGN KEY(tenant_id,workspace_id,job_id,next_attempt_id) REFERENCES training_attempts(tenant_id,workspace_id,job_id,id),
 FOREIGN KEY(tenant_id,workspace_id,job_id,checkpoint_id) REFERENCES training_checkpoints(tenant_id,workspace_id,job_id,id),
 FOREIGN KEY(tenant_id,workspace_id,previous_node_id) REFERENCES managed_nodes(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,next_node_id) REFERENCES managed_nodes(tenant_id,workspace_id,id),
 CHECK((mode='checkpoint')=(checkpoint_id IS NOT NULL))
);
ALTER TABLE training_jobs ADD CONSTRAINT training_job_pending_recovery_fk FOREIGN KEY(tenant_id,workspace_id,id,pending_recovery_id) REFERENCES training_recoveries(tenant_id,workspace_id,job_id,id);

CREATE TABLE training_uploads (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 job_id uuid NOT NULL,
 attempt_id uuid NOT NULL,
 kind varchar(16) NOT NULL CHECK(kind IN ('checkpoint','artifact')),
 global_step integer NOT NULL CHECK(global_step BETWEEN 0 AND 1000),
 manifest_sha256 varchar(64) NOT NULL CHECK(manifest_sha256 ~ '^[a-f0-9]{64}$'),
 manifest jsonb NOT NULL CHECK(jsonb_typeof(manifest)='object' AND octet_length(manifest::text)<=1048576),
 state varchar(16) NOT NULL CHECK(state IN ('uploading','accepted','failed')),
 manifest_bytes bytea NOT NULL CHECK(octet_length(manifest_bytes) BETWEEN 2 AND 1048576),
 active_chunk_id uuid,
 active_asset varchar(100) CHECK(active_asset IS NULL OR active_asset ~ '^[A-Za-z0-9_.-]{1,100}$'),
 active_offset bigint CHECK(active_offset IS NULL OR active_offset>=0 AND active_offset%8388608=0),
 active_expires_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,workspace_id,job_id,attempt_id,id),
 UNIQUE(attempt_id,kind,global_step),
 FOREIGN KEY(tenant_id,workspace_id,job_id,attempt_id) REFERENCES training_attempts(tenant_id,workspace_id,job_id,id),
 CHECK(num_nonnulls(active_chunk_id,active_asset,active_offset,active_expires_at) IN (0,4))
);
CREATE TABLE training_upload_chunks (
 upload_id uuid NOT NULL REFERENCES training_uploads(id),
 asset varchar(100) NOT NULL CHECK(asset ~ '^[A-Za-z0-9_.-]{1,100}$'),
 byte_offset bigint NOT NULL CHECK(byte_offset>=0 AND byte_offset%8388608=0),
 size_bytes integer NOT NULL CHECK(size_bytes BETWEEN 1 AND 8388608),
 sha256 varchar(64) NOT NULL CHECK(sha256 ~ '^[a-f0-9]{64}$'),
 PRIMARY KEY(upload_id,asset,byte_offset)
);
ALTER TABLE training_checkpoints ADD CONSTRAINT training_checkpoint_upload_fk FOREIGN KEY(tenant_id,workspace_id,job_id,attempt_id,id) REFERENCES training_uploads(tenant_id,workspace_id,job_id,attempt_id,id);
ALTER TABLE training_artifacts ADD CONSTRAINT training_artifact_upload_fk FOREIGN KEY(tenant_id,workspace_id,job_id,attempt_id,id) REFERENCES training_uploads(tenant_id,workspace_id,job_id,attempt_id,id);

-- Preserve deployment lookup/identity while extending the same physical reservation uniqueness domain.
ALTER TABLE gpu_leases DROP CONSTRAINT gpu_lease_identity_check;
ALTER TABLE gpu_leases ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE gpu_leases ALTER COLUMN id SET NOT NULL;
ALTER TABLE gpu_leases DROP CONSTRAINT gpu_leases_pkey;
ALTER TABLE gpu_leases ADD PRIMARY KEY(id);
ALTER TABLE gpu_leases ALTER COLUMN deployment_id DROP NOT NULL;
ALTER TABLE gpu_leases ADD UNIQUE(deployment_id);
ALTER TABLE gpu_leases ADD COLUMN training_job_id uuid UNIQUE;
ALTER TABLE gpu_leases ADD CONSTRAINT gpu_lease_exact_owner_check CHECK(num_nonnulls(deployment_id,training_job_id)=1);
ALTER TABLE gpu_leases ADD CONSTRAINT gpu_lease_training_scope_fk FOREIGN KEY(tenant_id,workspace_id,training_job_id) REFERENCES training_jobs(tenant_id,workspace_id,id);
ALTER TABLE gpu_leases ADD CONSTRAINT gpu_lease_node_scope_fk FOREIGN KEY(tenant_id,workspace_id,node_id) REFERENCES managed_nodes(tenant_id,workspace_id,id);

CREATE FUNCTION protect_training_job_inputs() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.node_id,NEW.model_id,NEW.dataset_id,NEW.dataset_version_id,NEW.gpu_uuid,NEW.configuration,NEW.configuration_sha256,NEW.execution_recipe,NEW.dataset_manifest_sha256,NEW.model_catalog_sha256,NEW.created_by,NEW.reason,NEW.creation_key,NEW.creation_fingerprint,NEW.created_at)
 IS DISTINCT FROM ROW(OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.node_id,OLD.model_id,OLD.dataset_id,OLD.dataset_version_id,OLD.gpu_uuid,OLD.configuration,OLD.configuration_sha256,OLD.execution_recipe,OLD.dataset_manifest_sha256,OLD.model_catalog_sha256,OLD.created_by,OLD.reason,OLD.creation_key,OLD.creation_fingerprint,OLD.created_at)
 THEN RAISE EXCEPTION 'TRAINING_INPUTS_IMMUTABLE' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END$$;
CREATE TRIGGER training_job_inputs_immutable BEFORE UPDATE ON training_jobs FOR EACH ROW EXECUTE FUNCTION protect_training_job_inputs();

CREATE FUNCTION protect_training_attempt_inputs() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.job_id,NEW.node_id,NEW.task_id,NEW.attempt_number,NEW.requested_by,NEW.requested_audience,NEW.requested_epoch,NEW.membership_authority,NEW.input_inventory,NEW.resume_checkpoint_id,NEW.created_at)
 IS DISTINCT FROM ROW(OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.job_id,OLD.node_id,OLD.task_id,OLD.attempt_number,OLD.requested_by,OLD.requested_audience,OLD.requested_epoch,OLD.membership_authority,OLD.input_inventory,OLD.resume_checkpoint_id,OLD.created_at)
 OR OLD.outcome IS NOT NULL AND ROW(NEW.outcome,NEW.outcome_sha256) IS DISTINCT FROM ROW(OLD.outcome,OLD.outcome_sha256)
 OR OLD.stop_token_hash IS NOT NULL AND NEW.stop_token_hash IS DISTINCT FROM OLD.stop_token_hash
 OR OLD.physical_stop_receipt IS NOT NULL AND ROW(NEW.physical_stop_receipt,NEW.physical_stop_sha256,NEW.physical_stopped_at) IS DISTINCT FROM ROW(OLD.physical_stop_receipt,OLD.physical_stop_sha256,OLD.physical_stopped_at)
 THEN RAISE EXCEPTION 'TRAINING_ATTEMPT_INPUTS_IMMUTABLE' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END$$;
CREATE TRIGGER training_attempt_inputs_immutable BEFORE UPDATE ON training_attempts FOR EACH ROW EXECUTE FUNCTION protect_training_attempt_inputs();

CREATE FUNCTION reject_training_immutable_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'TRAINING_ACCEPTED_METADATA_IMMUTABLE' USING ERRCODE='23514';
END$$;
CREATE TRIGGER training_checkpoint_immutable BEFORE UPDATE OR DELETE ON training_checkpoints FOR EACH ROW EXECUTE FUNCTION reject_training_immutable_change();
CREATE TRIGGER training_artifact_immutable BEFORE UPDATE OR DELETE ON training_artifacts FOR EACH ROW EXECUTE FUNCTION reject_training_immutable_change();
CREATE TRIGGER training_event_immutable BEFORE UPDATE OR DELETE ON training_events FOR EACH ROW EXECUTE FUNCTION reject_training_immutable_change();
CREATE TRIGGER training_chunk_immutable BEFORE UPDATE OR DELETE ON training_upload_chunks FOR EACH ROW EXECUTE FUNCTION reject_training_immutable_change();

CREATE FUNCTION protect_training_upload_inputs() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.job_id,NEW.attempt_id,NEW.kind,NEW.global_step,NEW.manifest_sha256,NEW.manifest,NEW.manifest_bytes,NEW.created_at)
 IS DISTINCT FROM ROW(OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.job_id,OLD.attempt_id,OLD.kind,OLD.global_step,OLD.manifest_sha256,OLD.manifest,OLD.manifest_bytes,OLD.created_at)
 OR OLD.state='accepted' AND NEW.state IS DISTINCT FROM OLD.state
 THEN RAISE EXCEPTION 'TRAINING_UPLOAD_INPUTS_IMMUTABLE' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END$$;
CREATE TRIGGER training_upload_inputs_immutable BEFORE UPDATE ON training_uploads FOR EACH ROW EXECUTE FUNCTION protect_training_upload_inputs();

CREATE FUNCTION protect_training_recovery_inputs() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' OR ROW(NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.job_id,NEW.previous_attempt_id,NEW.checkpoint_id,NEW.previous_node_id,NEW.next_node_id,NEW.mode,NEW.requested_by,NEW.reason,NEW.creation_key,NEW.creation_fingerprint,NEW.created_at)
 IS DISTINCT FROM ROW(OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.job_id,OLD.previous_attempt_id,OLD.checkpoint_id,OLD.previous_node_id,OLD.next_node_id,OLD.mode,OLD.requested_by,OLD.reason,OLD.creation_key,OLD.creation_fingerprint,OLD.created_at)
 OR OLD.next_attempt_id IS NOT NULL AND NEW.next_attempt_id IS DISTINCT FROM OLD.next_attempt_id
 THEN RAISE EXCEPTION 'TRAINING_RECOVERY_INPUTS_IMMUTABLE' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END$$;
CREATE TRIGGER training_recovery_inputs_immutable BEFORE UPDATE OR DELETE ON training_recoveries FOR EACH ROW EXECUTE FUNCTION protect_training_recovery_inputs();

ALTER TABLE gpu_leases ADD CONSTRAINT gpu_lease_inference_identity CHECK(deployment_id IS NULL OR id=deployment_id);

ALTER TABLE managed_nodes DROP CONSTRAINT managed_nodes_capabilities_check;
ALTER TABLE managed_nodes ADD CONSTRAINT managed_nodes_capabilities_check CHECK(
 (execution_profile='read_only' AND capabilities='["collect_facts.v1"]'::jsonb) OR
 (execution_profile='systemd_components' AND capabilities='["collect_facts.v1","node_exporter.install.v1","node_exporter.start.v1","node_exporter.stop.v1","node_exporter.remove.v1","node_exporter.observe.v1"]'::jsonb) OR
 (execution_profile='systemd_maintenance' AND capabilities='["collect_facts.v1","node_exporter.install.v1","node_exporter.start.v1","node_exporter.stop.v1","node_exporter.remove.v1","node_exporter.observe.v1","node_exporter.configure.v1","node_exporter.restart.v1","node_exporter.upgrade.v1","node_exporter.rollback.v1","node_exporter.logs.v1"]'::jsonb) OR
 (execution_profile='docker_inference' AND capabilities='["collect_facts.v1","inference.deploy.v1","inference.stop.v1","inference.remove.v1","inference.observe.v1","inference.verify.v1"]'::jsonb) OR
 (execution_profile='docker_ai' AND capabilities='["collect_facts.v1","inference.deploy.v1","inference.stop.v1","inference.remove.v1","inference.observe.v1","inference.verify.v1","training.run.v1"]'::jsonb));

INSERT INTO schema_migrations(version,checksum) VALUES(15,'7317a1664c39d6cacf53715ac5c7f44b835a08f2d8c8ce6ea85b46e418cc1b87');
-- Exact source: services/control/migrations/postgresql/000016_lifecycle.up.sql
-- M10 forward lifecycle extension of the immutable cumulative M09 fifteen-entry prefix.
-- A new enrollment explicitly selects lifecycle authority. Existing rows/capabilities are never upgraded.
ALTER TABLE node_join_tokens DROP CONSTRAINT node_join_tokens_execution_profile_check;
ALTER TABLE node_join_tokens ADD CONSTRAINT node_join_tokens_execution_profile_check CHECK(execution_profile IN ('read_only','systemd_components','systemd_maintenance','docker_inference','docker_ai','docker_model_lifecycle'));
ALTER TABLE managed_nodes DROP CONSTRAINT managed_nodes_execution_profile_check;
ALTER TABLE managed_nodes ADD CONSTRAINT managed_nodes_execution_profile_check CHECK(execution_profile IN ('read_only','systemd_components','systemd_maintenance','docker_inference','docker_ai','docker_model_lifecycle'));
ALTER TABLE node_tasks DROP CONSTRAINT node_tasks_operation_check;
ALTER TABLE node_tasks ADD CONSTRAINT node_tasks_operation_check CHECK(operation IN ('collect_facts.v1','node_exporter.install.v1','node_exporter.start.v1','node_exporter.stop.v1','node_exporter.remove.v1','node_exporter.observe.v1','node_exporter.configure.v1','node_exporter.restart.v1','node_exporter.upgrade.v1','node_exporter.rollback.v1','node_exporter.logs.v1','inference.deploy.v1','inference.stop.v1','inference.remove.v1','inference.observe.v1','inference.verify.v1','training.run.v1','model.prepare.v1','inference.derived.v1'));
CREATE TABLE model_holdouts (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 dataset_id uuid NOT NULL,
 dataset_version_id uuid NOT NULL,
 name varchar(100) NOT NULL CHECK(length(btrim(name))>0),
 manifest_sha256 varchar(64) NOT NULL CHECK(manifest_sha256 ~ '^[a-f0-9]{64}$'),
 effective_source_sha256 varchar(64) NOT NULL CHECK(effective_source_sha256 ~ '^[a-f0-9]{64}$'),
 row_mapping_sha256 varchar(64) NOT NULL CHECK(row_mapping_sha256 ~ '^[a-f0-9]{64}$'),
 source_bytes bigint NOT NULL CHECK(source_bytes BETWEEN 1 AND 67108864),
 row_count bigint NOT NULL CHECK(row_count BETWEEN 3 AND 256),
 label_counts jsonb NOT NULL CHECK(jsonb_typeof(label_counts)='object' AND octet_length(label_counts::text)<=1024),
 recipe_id varchar(80) NOT NULL,
 recipe_sha256 varchar(64) NOT NULL CHECK(recipe_sha256 ~ '^[a-f0-9]{64}$'),
 instruction_sha256 varchar(64) NOT NULL CHECK(instruction_sha256 ~ '^[a-f0-9]{64}$'),
 fingerprint_algorithm varchar(100) NOT NULL,
 created_by uuid NOT NULL REFERENCES users(id),
 audience varchar(8) NOT NULL CHECK(audience IN ('portal','admin')),
 reason varchar(500) NOT NULL CHECK(length(btrim(reason))>0),
 creation_key uuid NOT NULL,
 creation_fingerprint bytea NOT NULL CHECK(octet_length(creation_fingerprint)=32),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,workspace_id,id),
 CONSTRAINT model_holdout_version_unique UNIQUE(tenant_id,workspace_id,dataset_version_id,recipe_id),
 CONSTRAINT model_holdout_creation_unique UNIQUE(tenant_id,workspace_id,created_by,audience,creation_key),
 CHECK(label_counts ?& ARRAY['account','billing','technical'] AND label_counts - ARRAY['account','billing','technical'] = '{}'::jsonb),
 CHECK(jsonb_typeof(label_counts->'account')='number' AND jsonb_typeof(label_counts->'billing')='number' AND jsonb_typeof(label_counts->'technical')='number'),
 CHECK((label_counts->>'account') ~ '^[1-9][0-9]*$' AND (label_counts->>'billing') ~ '^[1-9][0-9]*$' AND (label_counts->>'technical') ~ '^[1-9][0-9]*$'),
 CHECK((label_counts->>'account')::bigint BETWEEN 1 AND 256 AND (label_counts->>'billing')::bigint BETWEEN 1 AND 256 AND (label_counts->>'technical')::bigint BETWEEN 1 AND 256),
 CHECK((label_counts->>'account')::bigint+(label_counts->>'billing')::bigint+(label_counts->>'technical')::bigint=row_count),
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id),
 FOREIGN KEY(tenant_id,workspace_id,dataset_id,dataset_version_id) REFERENCES dataset_versions(tenant_id,workspace_id,dataset_id,id)
);
CREATE INDEX model_holdout_page_idx ON model_holdouts(tenant_id,workspace_id,created_at DESC,id DESC);
CREATE TABLE model_holdout_sources (
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 holdout_id uuid NOT NULL,
 source_sha256 bytea NOT NULL CHECK(octet_length(source_sha256)=32),
 PRIMARY KEY(tenant_id,workspace_id,holdout_id,source_sha256),
 FOREIGN KEY(tenant_id,workspace_id,holdout_id) REFERENCES model_holdouts(tenant_id,workspace_id,id)
);
CREATE INDEX model_holdout_source_reservation_idx ON model_holdout_sources(tenant_id,workspace_id,source_sha256);
CREATE TABLE model_holdout_prompts (
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 holdout_id uuid NOT NULL,
 prompt_sha256 bytea NOT NULL CHECK(octet_length(prompt_sha256)=32),
 PRIMARY KEY(tenant_id,workspace_id,holdout_id,prompt_sha256),
 FOREIGN KEY(tenant_id,workspace_id,holdout_id) REFERENCES model_holdouts(tenant_id,workspace_id,id)
);
CREATE INDEX model_holdout_prompt_reservation_idx ON model_holdout_prompts(tenant_id,workspace_id,prompt_sha256);
CREATE FUNCTION protect_model_holdout_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'MODEL_HOLDOUT_IMMUTABLE';
END;
$$;
CREATE TRIGGER model_holdout_immutable BEFORE UPDATE OR DELETE ON model_holdouts FOR EACH ROW EXECUTE FUNCTION protect_model_holdout_immutable();
CREATE TRIGGER model_holdout_source_immutable BEFORE UPDATE OR DELETE ON model_holdout_sources FOR EACH ROW EXECUTE FUNCTION protect_model_holdout_immutable();
CREATE TRIGGER model_holdout_prompt_immutable BEFORE UPDATE OR DELETE ON model_holdout_prompts FOR EACH ROW EXECUTE FUNCTION protect_model_holdout_immutable();

CREATE TABLE model_preparations (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 node_id uuid NOT NULL,
 current_node_id uuid NOT NULL,
 model_id uuid NOT NULL,
 training_job_id uuid NOT NULL,
 training_attempt_id uuid NOT NULL,
 training_artifact_id uuid NOT NULL,
 heldout_id uuid NOT NULL,
 gpu_uuid varchar(64) NOT NULL CHECK(gpu_uuid ~ '^GPU-[a-f0-9-]{36}$'),
 input_snapshot jsonb NOT NULL CHECK(jsonb_typeof(input_snapshot)='object' AND octet_length(input_snapshot::text)<=65536),
 input_sha256 varchar(64) NOT NULL CHECK(input_sha256 ~ '^[a-f0-9]{64}$'),
 configuration jsonb NOT NULL CHECK(jsonb_typeof(configuration)='object' AND octet_length(configuration::text)<=16384),
 configuration_sha256 varchar(64) NOT NULL CHECK(configuration_sha256 ~ '^[a-f0-9]{64}$'),
 execution_recipe jsonb NOT NULL CHECK(jsonb_typeof(execution_recipe)='object' AND octet_length(execution_recipe::text)<=32768),
 created_by uuid NOT NULL REFERENCES users(id),
 requested_by uuid NOT NULL REFERENCES users(id),
 creation_audience varchar(8) NOT NULL CHECK(creation_audience IN ('portal','admin')),
 requested_audience varchar(8) NOT NULL CHECK(requested_audience IN ('portal','admin')),
 requested_epoch bigint NOT NULL CHECK(requested_epoch>=0),
 membership_authority uuid,
 reason varchar(500) NOT NULL CHECK(length(btrim(reason))>0),
 creation_key uuid NOT NULL,
 creation_fingerprint bytea NOT NULL CHECK(octet_length(creation_fingerprint)=32),
 state varchar(24) NOT NULL CHECK(state IN ('queued','preparing','running','cancelling','recovery_required','quality_failed','awaiting_review','approved','rejected','cancelled','failed')),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9007199254740991),
 wait_code varchar(80) CHECK(wait_code IS NULL OR wait_code ~ '^[A-Z][A-Z0-9_]{1,79}$'),
 error_code varchar(80) CHECK(error_code IS NULL OR error_code ~ '^[A-Z][A-Z0-9_]{1,79}$'),
 cancel_requested_at timestamptz,
 cancel_requested_by uuid REFERENCES users(id),
 next_dispatch_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 finished_at timestamptz,
 UNIQUE(tenant_id,workspace_id,id),
 CONSTRAINT model_preparation_creation_unique UNIQUE(tenant_id,workspace_id,created_by,creation_audience,creation_key),
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id),
 FOREIGN KEY(tenant_id,workspace_id,node_id) REFERENCES managed_nodes(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,current_node_id) REFERENCES managed_nodes(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,model_id) REFERENCES model_versions(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,training_job_id) REFERENCES training_jobs(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,training_job_id,training_attempt_id) REFERENCES training_attempts(tenant_id,workspace_id,job_id,id),
 FOREIGN KEY(tenant_id,workspace_id,training_job_id,training_artifact_id) REFERENCES training_artifacts(tenant_id,workspace_id,job_id,id),
 FOREIGN KEY(tenant_id,workspace_id,heldout_id) REFERENCES model_holdouts(tenant_id,workspace_id,id),
 CHECK(requested_audience='admin' OR membership_authority IS NOT NULL),
 CHECK((cancel_requested_at IS NULL)=(cancel_requested_by IS NULL))
);
CREATE INDEX model_preparation_scope_page_idx ON model_preparations(tenant_id,workspace_id,created_at DESC,id DESC);
CREATE INDEX model_preparation_queue_idx ON model_preparations(next_dispatch_at,created_at,id) WHERE state='queued';
CREATE FUNCTION protect_model_preparation_inputs() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'MODEL_PREPARATION_IMMUTABLE'; END IF;
 IF (NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.node_id,NEW.model_id,NEW.training_job_id,NEW.training_attempt_id,NEW.training_artifact_id,NEW.heldout_id,NEW.gpu_uuid,NEW.input_snapshot,NEW.input_sha256,NEW.configuration,NEW.configuration_sha256,NEW.execution_recipe,NEW.created_by,NEW.creation_audience,NEW.reason,NEW.creation_key,NEW.creation_fingerprint,NEW.created_at)
 IS DISTINCT FROM
 (OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.node_id,OLD.model_id,OLD.training_job_id,OLD.training_attempt_id,OLD.training_artifact_id,OLD.heldout_id,OLD.gpu_uuid,OLD.input_snapshot,OLD.input_sha256,OLD.configuration,OLD.configuration_sha256,OLD.execution_recipe,OLD.created_by,OLD.creation_audience,OLD.reason,OLD.creation_key,OLD.creation_fingerprint,OLD.created_at)
 THEN RAISE EXCEPTION 'MODEL_PREPARATION_INPUTS_IMMUTABLE'; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER model_preparation_inputs_immutable BEFORE UPDATE OR DELETE ON model_preparations FOR EACH ROW EXECUTE FUNCTION protect_model_preparation_inputs();

ALTER TABLE model_preparations ADD COLUMN attempt_number integer NOT NULL DEFAULT 0 CHECK(attempt_number BETWEEN 0 AND 1000);
ALTER TABLE model_preparations ADD COLUMN current_attempt_id uuid;
CREATE TABLE model_preparation_attempts (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 preparation_id uuid NOT NULL,
 node_id uuid NOT NULL,
 task_id uuid NOT NULL UNIQUE,
 attempt_number integer NOT NULL CHECK(attempt_number BETWEEN 1 AND 1000),
 state varchar(24) NOT NULL CHECK(state IN ('pending','preparing','running','stopping','recovery_required','stopped','prepared','cancelled','failed')),
 requested_by uuid NOT NULL REFERENCES users(id),
 requested_audience varchar(8) NOT NULL CHECK(requested_audience IN ('portal','admin')),
 requested_epoch bigint NOT NULL CHECK(requested_epoch>=0),
 membership_authority uuid,
 command_sha256 varchar(64) NOT NULL CHECK(command_sha256 ~ '^[a-f0-9]{64}$'),
 last_event_sequence bigint NOT NULL DEFAULT 0 CHECK(last_event_sequence BETWEEN 0 AND 1024),
 stop_token_hash bytea CHECK(stop_token_hash IS NULL OR octet_length(stop_token_hash)=32),
 outcome_sha256 varchar(64) CHECK(outcome_sha256 IS NULL OR outcome_sha256 ~ '^[a-f0-9]{64}$'),
 outcome jsonb CHECK(outcome IS NULL OR jsonb_typeof(outcome)='object' AND octet_length(outcome::text)<=8388608),
 physical_stop_receipt jsonb CHECK(physical_stop_receipt IS NULL OR jsonb_typeof(physical_stop_receipt)='object' AND octet_length(physical_stop_receipt::text)<=32768),
 physical_stop_sha256 varchar(64) CHECK(physical_stop_sha256 IS NULL OR physical_stop_sha256 ~ '^[a-f0-9]{64}$'),
 physical_stopped_at timestamptz,
 error_code varchar(80) CHECK(error_code IS NULL OR error_code ~ '^[A-Z][A-Z0-9_]{1,79}$'),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 started_at timestamptz,
 finished_at timestamptz,
 updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,workspace_id,preparation_id,id),
 UNIQUE(preparation_id,attempt_number),
 FOREIGN KEY(tenant_id,workspace_id,preparation_id) REFERENCES model_preparations(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,node_id) REFERENCES managed_nodes(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,node_id,task_id) REFERENCES node_tasks(tenant_id,workspace_id,node_id,id),
 CHECK(requested_audience='admin' OR membership_authority IS NOT NULL),
 CHECK((outcome IS NULL)=(outcome_sha256 IS NULL)),
 CHECK((physical_stop_receipt IS NULL)=(physical_stop_sha256 IS NULL)),
 CHECK((physical_stop_receipt IS NULL)=(physical_stopped_at IS NULL))
);
CREATE UNIQUE INDEX model_preparation_one_uncleared_attempt_idx ON model_preparation_attempts(preparation_id) WHERE physical_stopped_at IS NULL AND state IN ('pending','preparing','running','stopping','recovery_required');
ALTER TABLE model_preparations ADD CONSTRAINT model_preparation_current_attempt_fk FOREIGN KEY(tenant_id,workspace_id,id,current_attempt_id) REFERENCES model_preparation_attempts(tenant_id,workspace_id,preparation_id,id);
ALTER TABLE gpu_leases ADD COLUMN model_preparation_id uuid UNIQUE;
ALTER TABLE gpu_leases DROP CONSTRAINT gpu_lease_exact_owner_check;
ALTER TABLE gpu_leases ADD CONSTRAINT gpu_lease_exact_owner_check CHECK(num_nonnulls(deployment_id,training_job_id,model_preparation_id)=1);
ALTER TABLE gpu_leases ADD CONSTRAINT gpu_lease_preparation_scope_fk FOREIGN KEY(tenant_id,workspace_id,model_preparation_id) REFERENCES model_preparations(tenant_id,workspace_id,id);
CREATE FUNCTION protect_model_preparation_attempt_inputs() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'MODEL_PREPARATION_ATTEMPT_IMMUTABLE'; END IF;
 IF (NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.preparation_id,NEW.node_id,NEW.task_id,NEW.attempt_number,NEW.requested_by,NEW.requested_audience,NEW.requested_epoch,NEW.membership_authority,NEW.command_sha256,NEW.created_at)
 IS DISTINCT FROM
 (OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.preparation_id,OLD.node_id,OLD.task_id,OLD.attempt_number,OLD.requested_by,OLD.requested_audience,OLD.requested_epoch,OLD.membership_authority,OLD.command_sha256,OLD.created_at)
 THEN RAISE EXCEPTION 'MODEL_PREPARATION_ATTEMPT_INPUTS_IMMUTABLE'; END IF;
 IF OLD.stop_token_hash IS NOT NULL AND NEW.stop_token_hash IS DISTINCT FROM OLD.stop_token_hash THEN RAISE EXCEPTION 'MODEL_PREPARATION_STOP_AUTHORITY_IMMUTABLE'; END IF;
 IF OLD.physical_stopped_at IS NOT NULL AND (NEW.physical_stopped_at,NEW.physical_stop_receipt,NEW.physical_stop_sha256) IS DISTINCT FROM (OLD.physical_stopped_at,OLD.physical_stop_receipt,OLD.physical_stop_sha256) THEN RAISE EXCEPTION 'MODEL_PREPARATION_STOP_PROOF_IMMUTABLE'; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER model_preparation_attempt_inputs_immutable BEFORE UPDATE OR DELETE ON model_preparation_attempts FOR EACH ROW EXECUTE FUNCTION protect_model_preparation_attempt_inputs();

ALTER TABLE model_preparation_attempts ADD COLUMN event_bytes bigint NOT NULL DEFAULT 0 CHECK(event_bytes BETWEEN 0 AND 16777216);
CREATE TABLE model_preparation_events (
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 preparation_id uuid NOT NULL,
 attempt_id uuid NOT NULL,
 sequence bigint NOT NULL CHECK(sequence BETWEEN 1 AND 1024),
 kind varchar(24) NOT NULL CHECK(kind IN ('phase','evaluation_progress')),
 payload jsonb NOT NULL CHECK(jsonb_typeof(payload)='object' AND octet_length(payload::text)<=16384),
 canonical_sha256 varchar(64) NOT NULL CHECK(canonical_sha256 ~ '^[a-f0-9]{64}$'),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(attempt_id,sequence),
 FOREIGN KEY(tenant_id,workspace_id,preparation_id,attempt_id) REFERENCES model_preparation_attempts(tenant_id,workspace_id,preparation_id,id)
);
CREATE FUNCTION protect_model_preparation_event() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'MODEL_PREPARATION_EVENT_IMMUTABLE'; END;
$$;
CREATE TRIGGER model_preparation_event_immutable BEFORE UPDATE OR DELETE ON model_preparation_events FOR EACH ROW EXECUTE FUNCTION protect_model_preparation_event();

CREATE TABLE model_preparation_recoveries (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 preparation_id uuid NOT NULL,
 previous_attempt_id uuid NOT NULL,
 previous_node_id uuid NOT NULL,
 next_node_id uuid NOT NULL,
 next_attempt_number integer NOT NULL CHECK(next_attempt_number BETWEEN 2 AND 1000),
 requested_by uuid NOT NULL REFERENCES users(id),
 requested_audience varchar(8) NOT NULL CHECK(requested_audience IN ('portal','admin')),
 requested_epoch bigint NOT NULL CHECK(requested_epoch>=0),
 membership_authority uuid,
 reason varchar(500) NOT NULL CHECK(length(btrim(reason))>0),
 creation_key uuid NOT NULL,
 creation_fingerprint bytea NOT NULL CHECK(octet_length(creation_fingerprint)=32),
 dispatched_attempt_id uuid,
 applied_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,workspace_id,preparation_id,id),
 UNIQUE(preparation_id,requested_by,requested_audience,creation_key),
 FOREIGN KEY(tenant_id,workspace_id,preparation_id) REFERENCES model_preparations(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,preparation_id,previous_attempt_id) REFERENCES model_preparation_attempts(tenant_id,workspace_id,preparation_id,id),
 FOREIGN KEY(tenant_id,workspace_id,previous_node_id) REFERENCES managed_nodes(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,next_node_id) REFERENCES managed_nodes(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,preparation_id,dispatched_attempt_id) REFERENCES model_preparation_attempts(tenant_id,workspace_id,preparation_id,id),
 CHECK(requested_audience='admin' OR membership_authority IS NOT NULL),
 CHECK((dispatched_attempt_id IS NULL)=(applied_at IS NULL))
);
ALTER TABLE model_preparations ADD COLUMN pending_recovery_id uuid;
ALTER TABLE model_preparations ADD CONSTRAINT model_preparation_pending_recovery_fk FOREIGN KEY(tenant_id,workspace_id,id,pending_recovery_id) REFERENCES model_preparation_recoveries(tenant_id,workspace_id,preparation_id,id);
CREATE FUNCTION protect_model_preparation_recovery() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'MODEL_PREPARATION_RECOVERY_IMMUTABLE'; END IF;
 IF (NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.preparation_id,NEW.previous_attempt_id,NEW.previous_node_id,NEW.next_node_id,NEW.next_attempt_number,NEW.requested_by,NEW.requested_audience,NEW.requested_epoch,NEW.membership_authority,NEW.reason,NEW.creation_key,NEW.creation_fingerprint,NEW.created_at)
 IS DISTINCT FROM
 (OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.preparation_id,OLD.previous_attempt_id,OLD.previous_node_id,OLD.next_node_id,OLD.next_attempt_number,OLD.requested_by,OLD.requested_audience,OLD.requested_epoch,OLD.membership_authority,OLD.reason,OLD.creation_key,OLD.creation_fingerprint,OLD.created_at)
 THEN RAISE EXCEPTION 'MODEL_PREPARATION_RECOVERY_IMMUTABLE'; END IF;
 IF OLD.applied_at IS NOT NULL AND (NEW.dispatched_attempt_id,NEW.applied_at) IS DISTINCT FROM (OLD.dispatched_attempt_id,OLD.applied_at) THEN RAISE EXCEPTION 'MODEL_PREPARATION_RECOVERY_APPLIED_IMMUTABLE'; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER model_preparation_recovery_immutable BEFORE UPDATE OR DELETE ON model_preparation_recoveries FOR EACH ROW EXECUTE FUNCTION protect_model_preparation_recovery();

ALTER TABLE model_preparations ADD CONSTRAINT model_preparation_attempt_presence_check CHECK((attempt_number=0)=(current_attempt_id IS NULL));
ALTER TABLE model_preparations ADD CONSTRAINT model_preparation_recovery_presence_check CHECK(pending_recovery_id IS NULL OR attempt_number>0);

CREATE TABLE model_preparation_uploads (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 preparation_id uuid NOT NULL,
 attempt_id uuid NOT NULL UNIQUE,
 manifest_sha256 varchar(64) NOT NULL CHECK(manifest_sha256 ~ '^[a-f0-9]{64}$'),
 manifest jsonb NOT NULL CHECK(jsonb_typeof(manifest)='object' AND octet_length(manifest::text)<=8388608),
 manifest_bytes bytea NOT NULL CHECK(octet_length(manifest_bytes) BETWEEN 2 AND 8388608),
 descriptor jsonb NOT NULL CHECK(jsonb_typeof(descriptor)='object' AND octet_length(descriptor::text)<=32768),
 state varchar(16) NOT NULL DEFAULT 'uploading' CHECK(state IN ('uploading','verifying','accepted','failed')),
 active_chunk_id uuid,
 active_asset varchar(100),
 active_offset bigint CHECK(active_offset IS NULL OR active_offset BETWEEN 0 AND 2147483648),
 active_expires_at timestamptz,
 error_code varchar(80) CHECK(error_code IS NULL OR error_code ~ '^[A-Z][A-Z0-9_]{1,79}$'),
 accepted_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,workspace_id,preparation_id,attempt_id,id),
 FOREIGN KEY(tenant_id,workspace_id,preparation_id,attempt_id) REFERENCES model_preparation_attempts(tenant_id,workspace_id,preparation_id,id),
 CHECK((active_chunk_id IS NULL)=(active_asset IS NULL)),
 CHECK((active_chunk_id IS NULL)=(active_offset IS NULL)),
 CHECK((active_chunk_id IS NULL)=(active_expires_at IS NULL)),
 CHECK((state='accepted')=(accepted_at IS NOT NULL))
);
CREATE TABLE model_preparation_upload_chunks (
 upload_id uuid NOT NULL REFERENCES model_preparation_uploads(id),
 asset varchar(100) NOT NULL CHECK(asset ~ '^[A-Za-z0-9_.-]{1,100}$' AND asset NOT IN ('.','..')),
 byte_offset bigint NOT NULL CHECK(byte_offset BETWEEN 0 AND 2147483648 AND byte_offset % 8388608=0),
 size_bytes bigint NOT NULL CHECK(size_bytes BETWEEN 1 AND 8388608),
 sha256 varchar(64) NOT NULL CHECK(sha256 ~ '^[a-f0-9]{64}$'),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(upload_id,asset,byte_offset)
);
CREATE TABLE model_preparation_artifacts (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 preparation_id uuid NOT NULL,
 attempt_id uuid NOT NULL UNIQUE,
 upload_id uuid NOT NULL UNIQUE,
 manifest_sha256 varchar(64) NOT NULL CHECK(manifest_sha256 ~ '^[a-f0-9]{64}$'),
 descriptor jsonb NOT NULL CHECK(jsonb_typeof(descriptor)='object' AND octet_length(descriptor::text)<=32768),
 report jsonb NOT NULL CHECK(jsonb_typeof(report)='object' AND octet_length(report::text)<=8388608),
 report_bytes bytea NOT NULL CHECK(octet_length(report_bytes) BETWEEN 2 AND 8388608),
 storage_path varchar(512) NOT NULL,
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,workspace_id,preparation_id,attempt_id,id),
 FOREIGN KEY(tenant_id,workspace_id,preparation_id,attempt_id) REFERENCES model_preparation_attempts(tenant_id,workspace_id,preparation_id,id),
 FOREIGN KEY(tenant_id,workspace_id,preparation_id,attempt_id,upload_id) REFERENCES model_preparation_uploads(tenant_id,workspace_id,preparation_id,attempt_id,id)
);
ALTER TABLE model_preparation_attempts ADD COLUMN outcome_bytes bytea CHECK(outcome_bytes IS NULL OR octet_length(outcome_bytes) BETWEEN 2 AND 8388608);
ALTER TABLE model_preparation_attempts ADD CONSTRAINT model_preparation_outcome_bytes_presence_check CHECK((outcome IS NULL)=(outcome_bytes IS NULL));
CREATE FUNCTION protect_model_preparation_upload() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'MODEL_PREPARATION_UPLOAD_IMMUTABLE'; END IF;
 IF (NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.preparation_id,NEW.attempt_id,NEW.manifest_sha256,NEW.manifest,NEW.manifest_bytes,NEW.descriptor,NEW.created_at)
 IS DISTINCT FROM
 (OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.preparation_id,OLD.attempt_id,OLD.manifest_sha256,OLD.manifest,OLD.manifest_bytes,OLD.descriptor,OLD.created_at)
 THEN RAISE EXCEPTION 'MODEL_PREPARATION_UPLOAD_IMMUTABLE'; END IF;
 IF OLD.accepted_at IS NOT NULL AND (NEW.state,NEW.accepted_at) IS DISTINCT FROM (OLD.state,OLD.accepted_at) THEN RAISE EXCEPTION 'MODEL_PREPARATION_ACCEPTED_UPLOAD_IMMUTABLE'; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER model_preparation_upload_immutable BEFORE UPDATE OR DELETE ON model_preparation_uploads FOR EACH ROW EXECUTE FUNCTION protect_model_preparation_upload();
CREATE FUNCTION protect_model_preparation_output_record() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'MODEL_PREPARATION_OUTPUT_RECORD_IMMUTABLE'; END;
$$;
CREATE TRIGGER model_preparation_chunk_immutable BEFORE UPDATE OR DELETE ON model_preparation_upload_chunks FOR EACH ROW EXECUTE FUNCTION protect_model_preparation_output_record();
CREATE TRIGGER model_preparation_artifact_immutable BEFORE UPDATE OR DELETE ON model_preparation_artifacts FOR EACH ROW EXECUTE FUNCTION protect_model_preparation_output_record();
CREATE FUNCTION protect_model_preparation_outcome() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.outcome_sha256 IS NOT NULL AND (NEW.outcome,NEW.outcome_bytes,NEW.outcome_sha256) IS DISTINCT FROM (OLD.outcome,OLD.outcome_bytes,OLD.outcome_sha256) THEN RAISE EXCEPTION 'MODEL_PREPARATION_OUTCOME_IMMUTABLE'; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER model_preparation_outcome_immutable BEFORE UPDATE ON model_preparation_attempts FOR EACH ROW EXECUTE FUNCTION protect_model_preparation_outcome();

-- Explicit Admin review publishes a scoped local derivative, never an approved-base revision.
ALTER TABLE model_versions DROP CONSTRAINT model_versions_kind_check;
ALTER TABLE model_versions ADD CONSTRAINT model_versions_kind_check CHECK(kind IN ('approved_base','merged_lora'));
ALTER TABLE model_versions ADD CONSTRAINT model_version_catalog_identity UNIQUE(tenant_id,workspace_id,id,catalog_digest);
CREATE TABLE model_preparation_reviews (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 preparation_id uuid NOT NULL,
 attempt_id uuid NOT NULL,
 artifact_id uuid NOT NULL,
 report_sha256 varchar(64) NOT NULL CHECK(report_sha256 ~ '^[a-f0-9]{64}$'),
 expected_revision bigint NOT NULL CHECK(expected_revision BETWEEN 1 AND 9007199254740991),
 decision varchar(16) NOT NULL CHECK(decision IN ('approve','reject')),
 model_id uuid,
 model_name varchar(100) NOT NULL,
 reviewed_by uuid NOT NULL REFERENCES users(id),
 audience varchar(16) NOT NULL CHECK(audience='admin'),
 reason varchar(500) NOT NULL,
 idempotency_key uuid NOT NULL,
 fingerprint bytea NOT NULL CHECK(octet_length(fingerprint)=32),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 CHECK((decision='approve' AND model_id=artifact_id AND length(model_name)>0) OR (decision='reject' AND model_id IS NULL AND model_name='')),
 UNIQUE(tenant_id,workspace_id,id),
 UNIQUE(tenant_id,workspace_id,preparation_id,attempt_id),
 UNIQUE(tenant_id,workspace_id,reviewed_by,idempotency_key),
 FOREIGN KEY(tenant_id,workspace_id,preparation_id,attempt_id,artifact_id) REFERENCES model_preparation_artifacts(tenant_id,workspace_id,preparation_id,attempt_id,id),
 -- The decision and owning model registration commit together; neither may survive alone.
 FOREIGN KEY(tenant_id,workspace_id,model_id) REFERENCES model_versions(tenant_id,workspace_id,id) DEFERRABLE INITIALLY DEFERRED
);
CREATE TABLE model_derivations (
 model_id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 preparation_id uuid NOT NULL,
 attempt_id uuid NOT NULL,
 artifact_id uuid NOT NULL UNIQUE,
 base_model_id uuid NOT NULL,
 review_id uuid NOT NULL UNIQUE,
 catalog_digest varchar(64) NOT NULL CHECK(catalog_digest ~ '^[a-f0-9]{64}$'),
 catalog jsonb NOT NULL CHECK(jsonb_typeof(catalog)='object' AND octet_length(catalog::text)<=16384),
 report_sha256 varchar(64) NOT NULL CHECK(report_sha256 ~ '^[a-f0-9]{64}$'),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 CHECK(model_id=artifact_id AND model_id<>base_model_id),
 UNIQUE(tenant_id,workspace_id,model_id),
 FOREIGN KEY(tenant_id,workspace_id,model_id,catalog_digest) REFERENCES model_versions(tenant_id,workspace_id,id,catalog_digest),
 FOREIGN KEY(tenant_id,workspace_id,base_model_id) REFERENCES model_versions(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,review_id) REFERENCES model_preparation_reviews(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,preparation_id,attempt_id,artifact_id) REFERENCES model_preparation_artifacts(tenant_id,workspace_id,preparation_id,attempt_id,id)
);
ALTER TABLE model_preparations ADD COLUMN review_id uuid;
ALTER TABLE model_preparations ADD COLUMN derived_model_id uuid;
ALTER TABLE model_preparations ADD CONSTRAINT model_preparation_review_scope FOREIGN KEY(tenant_id,workspace_id,review_id) REFERENCES model_preparation_reviews(tenant_id,workspace_id,id);
ALTER TABLE model_preparations ADD CONSTRAINT model_preparation_derived_scope FOREIGN KEY(tenant_id,workspace_id,derived_model_id) REFERENCES model_versions(tenant_id,workspace_id,id);
ALTER TABLE model_preparations ADD CONSTRAINT model_preparation_review_presence CHECK(
 (state='approved' AND review_id IS NOT NULL AND derived_model_id IS NOT NULL) OR
 (state='rejected' AND review_id IS NOT NULL AND derived_model_id IS NULL) OR
 (state NOT IN ('approved','rejected') AND review_id IS NULL AND derived_model_id IS NULL)
);
CREATE TRIGGER model_preparation_review_immutable BEFORE UPDATE OR DELETE ON model_preparation_reviews FOR EACH ROW EXECUTE FUNCTION protect_model_preparation_output_record();
CREATE TRIGGER model_derivation_immutable BEFORE UPDATE OR DELETE ON model_derivations FOR EACH ROW EXECUTE FUNCTION protect_model_preparation_output_record();
CREATE FUNCTION protect_derived_model_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.kind='merged_lora' THEN
  IF TG_OP='DELETE' THEN RAISE EXCEPTION 'MODEL_DERIVED_IDENTITY_IMMUTABLE';END IF;
  IF (NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.kind,NEW.name,NEW.model_key,NEW.catalog_digest,NEW.manifest,NEW.created_by,NEW.created_at)
   IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.kind,OLD.name,OLD.model_key,OLD.catalog_digest,OLD.manifest,OLD.created_by,OLD.created_at)
  THEN RAISE EXCEPTION 'MODEL_DERIVED_IDENTITY_IMMUTABLE';END IF;
 ELSIF TG_OP='UPDATE' AND NEW.kind IS DISTINCT FROM OLD.kind THEN RAISE EXCEPTION 'MODEL_KIND_IMMUTABLE';
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD;END IF;
 RETURN NEW;
END;$$;
CREATE TRIGGER model_derived_identity_immutable BEFORE UPDATE OR DELETE ON model_versions FOR EACH ROW EXECUTE FUNCTION protect_derived_model_identity();
CREATE FUNCTION require_derived_model_review_graph() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.kind='merged_lora' AND NOT EXISTS(
  SELECT 1 FROM model_derivations d
  JOIN model_preparation_reviews r ON r.tenant_id=d.tenant_id AND r.workspace_id=d.workspace_id AND r.id=d.review_id
  JOIN model_preparations p ON p.tenant_id=d.tenant_id AND p.workspace_id=d.workspace_id AND p.id=d.preparation_id
  WHERE d.model_id=NEW.id AND d.tenant_id=NEW.tenant_id AND d.workspace_id=NEW.workspace_id AND d.catalog_digest=NEW.catalog_digest AND d.catalog=NEW.manifest
   AND r.decision='approve' AND r.model_id=NEW.id AND r.reviewed_by=NEW.created_by AND r.artifact_id=d.artifact_id AND r.report_sha256=d.report_sha256
   AND p.state='approved' AND p.derived_model_id=NEW.id AND p.review_id=r.id AND p.current_attempt_id=d.attempt_id
 ) THEN RAISE EXCEPTION 'MODEL_DERIVED_REVIEW_GRAPH_REQUIRED';END IF;
 RETURN NEW;
END;$$;
CREATE CONSTRAINT TRIGGER model_derived_review_graph AFTER INSERT OR UPDATE ON model_versions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION require_derived_model_review_graph();

-- Only explicitly selected derived tasks capture current administrator delegation.
ALTER TABLE node_tasks DROP CONSTRAINT node_tasks_inference_requested_epoch_check;
ALTER TABLE node_tasks ADD CONSTRAINT node_derived_delegation_presence CHECK(
 (operation='inference.derived.v1' AND inference_requested_epoch IS NOT NULL AND inference_requested_epoch>=1)
 OR (operation<>'inference.derived.v1' AND inference_requested_epoch IS NULL)
);
CREATE FUNCTION protect_derived_inference_task() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.operation='inference.derived.v1' OR NEW.operation='inference.derived.v1' THEN
  IF (NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.node_id,NEW.operation,NEW.payload,NEW.requested_by,NEW.inference_requested_epoch,NEW.idempotency_key,NEW.request_fingerprint,NEW.created_at)
    IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.node_id,OLD.operation,OLD.payload,OLD.requested_by,OLD.inference_requested_epoch,OLD.idempotency_key,OLD.request_fingerprint,OLD.created_at)
  THEN RAISE EXCEPTION 'DERIVED_INFERENCE_COMMAND_IMMUTABLE';END IF;
 END IF;
 RETURN NEW;
END;$$;
CREATE TRIGGER node_derived_inference_immutable BEFORE UPDATE ON node_tasks FOR EACH ROW EXECUTE FUNCTION protect_derived_inference_task();

-- Existing model, deployment and alias IDs, snapshots and data remain unchanged.
ALTER TABLE deployments DROP CONSTRAINT deployments_model_kind_check;
ALTER TABLE deployments DROP CONSTRAINT deployments_serving_recipe_sha256_check;
ALTER TABLE deployments ADD CONSTRAINT deployment_model_variant CHECK(
 (model_kind='approved_base' AND catalog_digest='4d32aa5cd4b2d86f12beff791f0428f0813c2d3479041080d13b0be6bc6842bc' AND serving_recipe_sha256 IS NULL)
 OR (model_kind='merged_lora' AND catalog_digest~'^[a-f0-9]{64}$' AND catalog_digest<>'4d32aa5cd4b2d86f12beff791f0428f0813c2d3479041080d13b0be6bc6842bc' AND serving_recipe_sha256~'^[a-f0-9]{64}$' AND serving_recipe_sha256 IS NOT NULL)
);
CREATE FUNCTION protect_deployment_model_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.node_id,NEW.model_id,NEW.gpu_uuid,NEW.model_kind,NEW.catalog_digest,NEW.serving_recipe_sha256)
 IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.node_id,OLD.model_id,OLD.gpu_uuid,OLD.model_kind,OLD.catalog_digest,OLD.serving_recipe_sha256)
 THEN RAISE EXCEPTION 'DEPLOYMENT_MODEL_IDENTITY_IMMUTABLE';END IF;
 RETURN NEW;
END;$$;
CREATE TRIGGER deployment_model_identity_immutable BEFORE UPDATE ON deployments FOR EACH ROW EXECUTE FUNCTION protect_deployment_model_identity();
-- Recoverable alias pause/cutover retains exact historical model identities and existing scoped keys.
ALTER TABLE model_alias_history DROP CONSTRAINT model_alias_history_action_check;
ALTER TABLE model_alias_history ADD CONSTRAINT model_alias_history_action_check CHECK(action IN ('published','disabled','paused','resumed','retargeted','rolled_back'));
ALTER TABLE model_alias_target_versions DROP CONSTRAINT model_alias_target_versions_source_revision_check;
ALTER TABLE model_alias_target_versions DROP CONSTRAINT model_alias_target_versions_action_check;
ALTER TABLE model_alias_target_versions DROP CONSTRAINT model_alias_target_versions_model_kind_check;
ALTER TABLE model_alias_target_versions ADD CONSTRAINT model_alias_target_versions_action_check CHECK(action IN ('published','disabled','paused','resumed','retargeted','rolled_back'));
ALTER TABLE model_alias_target_versions ADD CONSTRAINT model_alias_target_versions_model_kind_check CHECK(model_kind IN ('approved_base','merged_lora'));
CREATE TABLE model_alias_cutover_intents (
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 actor_user_id uuid NOT NULL REFERENCES users(id),
 idempotency_key uuid NOT NULL,
 alias_id uuid NOT NULL,
 result_revision bigint NOT NULL,
 fingerprint bytea NOT NULL CHECK(octet_length(fingerprint)=32),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(tenant_id,workspace_id,actor_user_id,idempotency_key),
 FOREIGN KEY(tenant_id,workspace_id,alias_id,result_revision) REFERENCES model_alias_target_versions(tenant_id,workspace_id,alias_id,revision)
);
CREATE TRIGGER model_alias_intent_immutable BEFORE UPDATE OR DELETE ON model_alias_cutover_intents FOR EACH ROW EXECUTE FUNCTION protect_alias_cutover_record();
-- Preserve immutable review provenance. Existing invalid approvals refuse upgrade;
-- migration never invents a model identity or human review decision.
ALTER TABLE model_preparation_reviews
 ADD CONSTRAINT model_review_approval_identity_required
 CHECK((decision='approve' AND model_id IS NOT NULL AND model_id=artifact_id AND length(model_name)>0)
    OR (decision='reject' AND model_id IS NULL AND model_name=''));
ALTER TABLE managed_nodes DROP CONSTRAINT managed_nodes_capabilities_check;
ALTER TABLE managed_nodes ADD CONSTRAINT managed_nodes_capabilities_check CHECK(
 (execution_profile='read_only' AND capabilities='["collect_facts.v1"]'::jsonb) OR
 (execution_profile='systemd_components' AND capabilities='["collect_facts.v1","node_exporter.install.v1","node_exporter.start.v1","node_exporter.stop.v1","node_exporter.remove.v1","node_exporter.observe.v1"]'::jsonb) OR
 (execution_profile='systemd_maintenance' AND capabilities='["collect_facts.v1","node_exporter.install.v1","node_exporter.start.v1","node_exporter.stop.v1","node_exporter.remove.v1","node_exporter.observe.v1","node_exporter.configure.v1","node_exporter.restart.v1","node_exporter.upgrade.v1","node_exporter.rollback.v1","node_exporter.logs.v1"]'::jsonb) OR
 (execution_profile='docker_inference' AND capabilities='["collect_facts.v1","inference.deploy.v1","inference.stop.v1","inference.remove.v1","inference.observe.v1","inference.verify.v1"]'::jsonb) OR
 (execution_profile='docker_ai' AND capabilities='["collect_facts.v1","inference.deploy.v1","inference.stop.v1","inference.remove.v1","inference.observe.v1","inference.verify.v1","training.run.v1"]'::jsonb) OR
 (execution_profile='docker_model_lifecycle' AND capabilities='["collect_facts.v1","inference.deploy.v1","inference.stop.v1","inference.remove.v1","inference.observe.v1","inference.verify.v1","training.run.v1","model.prepare.v1","inference.derived.v1"]'::jsonb));

INSERT INTO schema_migrations(version,checksum) VALUES(16,'bbee01cd26cc6c5e32260ca0c787e8ded1c1601e3e650ab07b611997d13bc1ff');
-- Exact source: services/control/migrations/postgresql/000017_sso.up.sql
-- M11 cumulative forward migration after the immutable M10 sixteen-entry catalog.
-- Existing source columns are retained; only the local-only restriction is replaced.
-- M11 explicit existing-account binding; provider tokens and authorization codes are never stored.
CREATE TABLE sso_connectors (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL REFERENCES tenants(id),
 name varchar(100) NOT NULL CHECK(length(btrim(name))>0),
 provider varchar(16) NOT NULL CHECK(provider='gitea'),
 origin varchar(2048) NOT NULL,
 client_id uuid NOT NULL,
 secret_key_id varchar(64) NOT NULL,
 secret_nonce bytea NOT NULL CHECK(octet_length(secret_nonce)=12),
 secret_ciphertext bytea NOT NULL CHECK(octet_length(secret_ciphertext) BETWEEN 17 AND 4112),
 enabled boolean NOT NULL,
 revision bigint NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9007199254740991),
 auth_version bigint NOT NULL DEFAULT 1 CHECK(auth_version BETWEEN 1 AND 9007199254740991),
 created_by uuid NOT NULL REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,id)
);
CREATE INDEX sso_connectors_page_idx ON sso_connectors(tenant_id,created_at DESC,id DESC);
CREATE TABLE sso_bindings (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 connector_id uuid NOT NULL,
 subject varchar(19) COLLATE "C" NOT NULL CHECK(subject~'^[1-9][0-9]{0,18}$' AND subject::numeric<=9223372036854775807),
 user_id uuid NOT NULL REFERENCES users(id),
 active boolean NOT NULL DEFAULT true,
 revision bigint NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9007199254740991),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,connector_id,subject),
 UNIQUE(tenant_id,connector_id,id,user_id),
 FOREIGN KEY(tenant_id,connector_id) REFERENCES sso_connectors(tenant_id,id)
);
CREATE INDEX sso_bindings_user_idx ON sso_bindings(tenant_id,user_id,created_at,id);
CREATE TABLE sso_flows (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 connector_id uuid NOT NULL,
 connector_revision bigint NOT NULL CHECK(connector_revision BETWEEN 1 AND 9007199254740991),
 auth_version bigint NOT NULL CHECK(auth_version BETWEEN 1 AND 9007199254740991),
 audience varchar(16) NOT NULL CHECK(audience IN ('portal','admin')),
 mode varchar(16) NOT NULL CHECK(mode IN ('login','link')),
 state varchar(16) NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','claimed','succeeded','failed','expired')),
 state_hash bytea NOT NULL UNIQUE CHECK(octet_length(state_hash)=32),
 browser_hash bytea NOT NULL CHECK(octet_length(browser_hash)=32),
 previous_cookie_hash bytea NOT NULL CHECK(octet_length(previous_cookie_hash)=32),
 previous_session_id uuid,
 initiating_user_id uuid REFERENCES users(id),
 initiating_session_id uuid,
 initiating_epoch bigint,
 credential_version bigint,
 reason varchar(500),
 verifier_key_id varchar(64),
 verifier_nonce bytea,
 verifier_ciphertext bytea,
 claim_id uuid,
 authenticated_user_id uuid REFERENCES users(id),
 issued_session_id uuid,
 error_code varchar(80),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 expires_at timestamptz NOT NULL,
 finished_at timestamptz,
 UNIQUE(tenant_id,connector_id,id),
 UNIQUE(tenant_id,connector_id,id,authenticated_user_id),
 FOREIGN KEY(tenant_id,connector_id) REFERENCES sso_connectors(tenant_id,id),
 CHECK(expires_at>created_at),
 CHECK((mode='login' AND initiating_user_id IS NULL AND initiating_session_id IS NULL AND initiating_epoch IS NULL AND credential_version IS NULL AND reason IS NULL)
    OR (mode='link' AND previous_session_id IS NOT NULL AND previous_session_id=initiating_session_id AND initiating_user_id IS NOT NULL AND initiating_session_id IS NOT NULL AND initiating_epoch IS NOT NULL AND credential_version IS NOT NULL AND reason IS NOT NULL AND initiating_epoch BETWEEN 1 AND 9007199254740991 AND credential_version BETWEEN 1 AND 9007199254740991 AND length(btrim(reason)) BETWEEN 1 AND 500)),
 CHECK((state='pending' AND verifier_key_id IS NOT NULL AND verifier_nonce IS NOT NULL AND octet_length(verifier_nonce)=12 AND verifier_ciphertext IS NOT NULL AND octet_length(verifier_ciphertext)=59 AND claim_id IS NULL)
    OR (state<>'pending' AND verifier_key_id IS NULL AND verifier_nonce IS NULL AND verifier_ciphertext IS NULL)),
 CHECK(state NOT IN ('claimed','succeeded') OR claim_id IS NOT NULL),
 CHECK((state='succeeded')=(authenticated_user_id IS NOT NULL)),
 CHECK(state='succeeded' OR issued_session_id IS NULL),
 CHECK((state IN ('succeeded','failed','expired'))=(finished_at IS NOT NULL)),
 CHECK((state IN ('failed','expired') AND error_code IS NOT NULL AND error_code~'^[A-Z][A-Z0-9_]{1,79}$') OR (state NOT IN ('failed','expired') AND error_code IS NULL))
);
CREATE INDEX sso_flows_expiry_idx ON sso_flows(expires_at,id) WHERE state IN ('pending','claimed');
CREATE INDEX sso_flows_connector_idx ON sso_flows(tenant_id,connector_id,state);
ALTER TABLE sessions DROP CONSTRAINT account_local_session_only;
ALTER TABLE sessions ADD CONSTRAINT sessions_sso_source_complete CHECK(
  (sso_tenant_id IS NULL AND sso_connector_id IS NULL AND sso_binding_id IS NULL AND sso_auth_version IS NULL AND sso_flow_id IS NULL)
  OR (sso_tenant_id IS NOT NULL AND sso_connector_id IS NOT NULL AND sso_binding_id IS NOT NULL AND sso_auth_version IS NOT NULL AND sso_auth_version BETWEEN 1 AND 9007199254740991 AND sso_flow_id IS NOT NULL)),
 ADD CONSTRAINT sessions_sso_binding_fk FOREIGN KEY(sso_tenant_id,sso_connector_id,sso_binding_id,user_id) REFERENCES sso_bindings(tenant_id,connector_id,id,user_id),
 ADD CONSTRAINT sessions_sso_flow_fk FOREIGN KEY(sso_tenant_id,sso_connector_id,sso_flow_id,user_id) REFERENCES sso_flows(tenant_id,connector_id,id,authenticated_user_id),
 ADD CONSTRAINT sessions_sso_flow_unique UNIQUE(sso_flow_id);
CREATE INDEX sessions_sso_connector_live_idx ON sessions(sso_tenant_id,sso_connector_id) WHERE revoked_at IS NULL AND sso_connector_id IS NOT NULL;
CREATE INDEX sessions_sso_binding_live_idx ON sessions(sso_binding_id) WHERE revoked_at IS NULL AND sso_binding_id IS NOT NULL;
CREATE TABLE sso_mutation_intents (
 tenant_id uuid NOT NULL REFERENCES tenants(id),
 actor_user_id uuid NOT NULL REFERENCES users(id),
 idempotency_key uuid NOT NULL,
 operation varchar(24) NOT NULL CHECK(operation IN ('create_connector','update_connector','unlink_binding')),
 target_id uuid NOT NULL,
 fingerprint bytea NOT NULL CHECK(octet_length(fingerprint)=32),
 result jsonb NOT NULL CHECK(jsonb_typeof(result)='object'),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(tenant_id,actor_user_id,idempotency_key)
);
CREATE FUNCTION protect_sso_connector_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.id,NEW.tenant_id,NEW.provider,NEW.origin,NEW.created_by,NEW.created_at) IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.provider,OLD.origin,OLD.created_by,OLD.created_at)
 THEN RAISE EXCEPTION 'SSO_CONNECTOR_IDENTITY_IMMUTABLE'; END IF;
 IF NEW.revision<>OLD.revision+1 OR NEW.auth_version NOT IN (OLD.auth_version,OLD.auth_version+1)
 OR ((NEW.enabled,NEW.client_id,NEW.secret_key_id,NEW.secret_nonce,NEW.secret_ciphertext) IS DISTINCT FROM (OLD.enabled,OLD.client_id,OLD.secret_key_id,OLD.secret_nonce,OLD.secret_ciphertext) AND NEW.auth_version<>OLD.auth_version+1)
 THEN RAISE EXCEPTION 'SSO_CONNECTOR_REVISION_INVALID'; END IF;
 RETURN NEW;
END;$$;
CREATE TRIGGER sso_connector_identity BEFORE UPDATE ON sso_connectors FOR EACH ROW EXECUTE FUNCTION protect_sso_connector_identity();
CREATE FUNCTION protect_sso_binding_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.id,NEW.tenant_id,NEW.connector_id,NEW.subject,NEW.user_id,NEW.created_at) IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.connector_id,OLD.subject,OLD.user_id,OLD.created_at)
 OR NEW.revision<>OLD.revision+1 THEN RAISE EXCEPTION 'SSO_BINDING_IDENTITY_IMMUTABLE'; END IF;
 RETURN NEW;
END;$$;
CREATE TRIGGER sso_binding_identity BEFORE UPDATE ON sso_bindings FOR EACH ROW EXECUTE FUNCTION protect_sso_binding_identity();
CREATE FUNCTION protect_sso_flow_transition() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.state<>'pending' THEN RAISE EXCEPTION 'SSO_FLOW_MUST_BEGIN_PENDING'; END IF;
  RETURN NEW;
 END IF;
 IF OLD.state='succeeded' AND NEW.state='succeeded' AND OLD.issued_session_id IS NULL AND NEW.issued_session_id IS NOT NULL
 AND (to_jsonb(NEW)-'issued_session_id')=(to_jsonb(OLD)-'issued_session_id') THEN RETURN NEW; END IF;
 IF (NEW.id,NEW.tenant_id,NEW.connector_id,NEW.connector_revision,NEW.auth_version,NEW.audience,NEW.mode,NEW.state_hash,NEW.browser_hash,NEW.previous_cookie_hash,NEW.previous_session_id,NEW.initiating_user_id,NEW.initiating_session_id,NEW.initiating_epoch,NEW.credential_version,NEW.reason,NEW.created_at,NEW.expires_at)
 IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.connector_id,OLD.connector_revision,OLD.auth_version,OLD.audience,OLD.mode,OLD.state_hash,OLD.browser_hash,OLD.previous_cookie_hash,OLD.previous_session_id,OLD.initiating_user_id,OLD.initiating_session_id,OLD.initiating_epoch,OLD.credential_version,OLD.reason,OLD.created_at,OLD.expires_at)
 OR NEW.issued_session_id IS DISTINCT FROM OLD.issued_session_id
 OR NOT ((OLD.state='pending' AND NEW.state IN ('claimed','failed','expired')) OR (OLD.state='claimed' AND NEW.state IN ('succeeded','failed','expired')))
 OR (OLD.claim_id IS NOT NULL AND NEW.claim_id IS DISTINCT FROM OLD.claim_id)
 THEN RAISE EXCEPTION 'SSO_FLOW_TRANSITION_INVALID'; END IF;
 RETURN NEW;
END;$$;
CREATE TRIGGER sso_flow_transition BEFORE INSERT OR UPDATE ON sso_flows FOR EACH ROW EXECUTE FUNCTION protect_sso_flow_transition();
CREATE FUNCTION claim_sso_session_issuance() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.sso_flow_id IS NULL THEN RETURN NEW; END IF;
 UPDATE sso_flows SET issued_session_id=NEW.id
 WHERE tenant_id=NEW.sso_tenant_id AND connector_id=NEW.sso_connector_id AND id=NEW.sso_flow_id
 AND authenticated_user_id=NEW.user_id AND auth_version=NEW.sso_auth_version AND state='succeeded' AND issued_session_id IS NULL;
 IF NOT FOUND THEN RAISE EXCEPTION 'SSO_FLOW_ALREADY_ISSUED'; END IF;
 RETURN NEW;
END;$$;
CREATE TRIGGER sessions_sso_single_issuance BEFORE INSERT ON sessions FOR EACH ROW EXECUTE FUNCTION claim_sso_session_issuance();
CREATE FUNCTION protect_sso_intent() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'SSO_INTENT_IMMUTABLE'; END;$$;
CREATE TRIGGER sso_intent_immutable BEFORE UPDATE OR DELETE ON sso_mutation_intents FOR EACH ROW EXECUTE FUNCTION protect_sso_intent();
CREATE TRIGGER sso_connector_retained BEFORE DELETE ON sso_connectors FOR EACH ROW EXECUTE FUNCTION protect_sso_intent();
CREATE TRIGGER sso_binding_retained BEFORE DELETE ON sso_bindings FOR EACH ROW EXECUTE FUNCTION protect_sso_intent();

INSERT INTO schema_migrations(version,checksum) VALUES(17,'7b4b1a5126f0aea4a36bf58d0e2a978588e5875fbf03ed962d8e79e523c3ec42');
-- Exact source: services/control/migrations/postgresql/000018_knowledge.up.sql
-- Cumulative M12: append historical knowledge semantics after the immutable M11 prefix.
-- Every prior account/platform migration remains byte-identical.
-- The operator installs the approved database-wide extension before product migration.
DO $$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace WHERE e.extname='vector' AND e.extversion='0.8.7' AND n.nspname='public') THEN
  RAISE EXCEPTION 'KNOWLEDGE_APPROVED_VECTOR_EXTENSION_REQUIRED';
 END IF;
 IF EXISTS (SELECT 1 FROM pg_namespace n CROSS JOIN LATERAL aclexplode(COALESCE(n.nspacl,acldefault('n',n.nspowner))) a WHERE n.nspname='public' AND a.grantee=0 AND a.privilege_type='CREATE') THEN
  RAISE EXCEPTION 'KNOWLEDGE_TRUSTED_VECTOR_SCHEMA_REQUIRED';
 END IF;
END $$;

CREATE TABLE knowledge_bases (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 name varchar(100) NOT NULL CHECK(length(name)>0),
 name_key varchar(200) NOT NULL CHECK(length(name_key)>0),
 enabled boolean NOT NULL DEFAULT true,
 revision bigint NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9007199254740991),
 active_generation_id uuid NOT NULL,
 building_generation_id uuid,
 created_by uuid NOT NULL REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 creation_key uuid NOT NULL,
 creation_fingerprint bytea NOT NULL CHECK(octet_length(creation_fingerprint)=32),
 UNIQUE(tenant_id,workspace_id,id),
 UNIQUE(tenant_id,workspace_id,created_by,creation_key),
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id),
 CHECK(building_generation_id IS NULL OR building_generation_id<>active_generation_id)
);
CREATE UNIQUE INDEX knowledge_base_name_idx ON knowledge_bases(tenant_id,workspace_id,name_key);
CREATE INDEX knowledge_base_page_idx ON knowledge_bases(tenant_id,workspace_id,created_at DESC,id DESC);

CREATE TABLE knowledge_generations (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 base_id uuid NOT NULL,
 catalog_sha256 varchar(64) NOT NULL CHECK(catalog_sha256 ~ '^[a-f0-9]{64}$'),
 dimension integer NOT NULL CHECK(dimension=384),
 chunk_tokens integer NOT NULL CHECK(chunk_tokens BETWEEN 8 AND 254),
 overlap_tokens integer NOT NULL CHECK(overlap_tokens>=0 AND overlap_tokens<chunk_tokens),
 state varchar(12) NOT NULL CHECK(state IN ('building','active','failed','retired')),
 requested_by uuid NOT NULL REFERENCES users(id),
 requested_audience varchar(8) NOT NULL CHECK(requested_audience IN ('portal','admin')),
 requested_epoch bigint NOT NULL CHECK(requested_epoch>=0),
 membership_authority uuid,
 expected_documents integer NOT NULL CHECK(expected_documents BETWEEN 0 AND 1000),
 error_code varchar(80) CHECK(error_code IS NULL OR error_code ~ '^[A-Z][A-Z0-9_]{1,79}$'),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 activated_at timestamptz,
 finished_at timestamptz,
 UNIQUE(tenant_id,workspace_id,base_id,id),
 FOREIGN KEY(tenant_id,workspace_id,base_id) REFERENCES knowledge_bases(tenant_id,workspace_id,id),
 CHECK(requested_audience='admin' OR membership_authority IS NOT NULL),
 CHECK(state<>'active' OR activated_at IS NOT NULL)
);
CREATE UNIQUE INDEX knowledge_one_active_generation_idx ON knowledge_generations(tenant_id,workspace_id,base_id) WHERE state='active';
CREATE UNIQUE INDEX knowledge_one_building_generation_idx ON knowledge_generations(tenant_id,workspace_id,base_id) WHERE state='building';
ALTER TABLE knowledge_bases ADD CONSTRAINT knowledge_active_generation_scope FOREIGN KEY(tenant_id,workspace_id,id,active_generation_id) REFERENCES knowledge_generations(tenant_id,workspace_id,base_id,id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE knowledge_bases ADD CONSTRAINT knowledge_building_generation_scope FOREIGN KEY(tenant_id,workspace_id,id,building_generation_id) REFERENCES knowledge_generations(tenant_id,workspace_id,base_id,id) DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE knowledge_documents (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 base_id uuid NOT NULL,
 name varchar(255) NOT NULL CHECK(length(name)>0),
 name_key varchar(600) NOT NULL CHECK(length(name_key)>0),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9007199254740991),
 state varchar(12) NOT NULL CHECK(state IN ('pending','running','ready','failed','deleted')),
 visible_source_id uuid,
 pending_source_id uuid,
 current_attempt_id uuid,
 created_by uuid NOT NULL REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 deleted_at timestamptz,
 delete_reason varchar(500),
 UNIQUE(tenant_id,workspace_id,base_id,id),
 FOREIGN KEY(tenant_id,workspace_id,base_id) REFERENCES knowledge_bases(tenant_id,workspace_id,id),
 CHECK((state='deleted' AND deleted_at IS NOT NULL AND length(delete_reason)>0) OR(state<>'deleted' AND deleted_at IS NULL AND delete_reason IS NULL)),
 CHECK(state<>'ready' OR visible_source_id IS NOT NULL)
);
CREATE UNIQUE INDEX knowledge_document_name_idx ON knowledge_documents(tenant_id,workspace_id,base_id,name_key) WHERE deleted_at IS NULL;
CREATE INDEX knowledge_document_page_idx ON knowledge_documents(tenant_id,workspace_id,base_id,created_at DESC,id DESC);

CREATE TABLE knowledge_source_versions (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 base_id uuid NOT NULL,
 document_id uuid NOT NULL,
 source_version bigint NOT NULL CHECK(source_version BETWEEN 1 AND 9007199254740991),
 content bytea NOT NULL CHECK(octet_length(content) BETWEEN 1 AND 2097152 AND length(pg_catalog.convert_from(content,'UTF8'))>0),
 size_bytes integer GENERATED ALWAYS AS (octet_length(content)) STORED,
 sha256 varchar(64) NOT NULL CHECK(sha256 ~ '^[a-f0-9]{64}$' AND sha256=pg_catalog.encode(pg_catalog.sha256(content),'hex')),
 created_by uuid NOT NULL REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 superseded_at timestamptz CHECK(superseded_at IS NULL OR superseded_at>=created_at),
 UNIQUE(tenant_id,workspace_id,base_id,document_id,id),
 UNIQUE(document_id,source_version),
 FOREIGN KEY(tenant_id,workspace_id,base_id,document_id) REFERENCES knowledge_documents(tenant_id,workspace_id,base_id,id)
);
ALTER TABLE knowledge_documents ADD CONSTRAINT knowledge_visible_source_scope FOREIGN KEY(tenant_id,workspace_id,base_id,id,visible_source_id) REFERENCES knowledge_source_versions(tenant_id,workspace_id,base_id,document_id,id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE knowledge_documents ADD CONSTRAINT knowledge_pending_source_scope FOREIGN KEY(tenant_id,workspace_id,base_id,id,pending_source_id) REFERENCES knowledge_source_versions(tenant_id,workspace_id,base_id,document_id,id) DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE knowledge_attempts (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 base_id uuid NOT NULL,
 document_id uuid NOT NULL,
 source_id uuid NOT NULL,
 generation_id uuid NOT NULL,
 kind varchar(8) NOT NULL CHECK(kind IN ('ingest','rebuild')),
 attempt_number integer NOT NULL CHECK(attempt_number BETWEEN 1 AND 10),
 expected_document_revision bigint NOT NULL CHECK(expected_document_revision BETWEEN 1 AND 9007199254740991),
 requested_by uuid NOT NULL REFERENCES users(id),
 requested_audience varchar(8) NOT NULL CHECK(requested_audience IN ('portal','admin')),
 requested_epoch bigint NOT NULL CHECK(requested_epoch>=0),
 membership_authority uuid,
 state varchar(12) NOT NULL CHECK(state IN ('pending','running','ready','failed','cancelled')),
 lease_hash bytea CHECK(lease_hash IS NULL OR octet_length(lease_hash)=32),
 lease_expires_at timestamptz,
 chunk_count integer NOT NULL DEFAULT 0 CHECK(chunk_count BETWEEN 0 AND 4096),
 error_code varchar(80) CHECK(error_code IS NULL OR error_code ~ '^[A-Z][A-Z0-9_]{1,79}$'),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 started_at timestamptz,
 finished_at timestamptz,
 UNIQUE(tenant_id,workspace_id,base_id,document_id,generation_id,source_id,id),
 UNIQUE(tenant_id,workspace_id,base_id,document_id,id),
 UNIQUE(generation_id,document_id,source_id,attempt_number),
 FOREIGN KEY(tenant_id,workspace_id,base_id,document_id,source_id) REFERENCES knowledge_source_versions(tenant_id,workspace_id,base_id,document_id,id),
 FOREIGN KEY(tenant_id,workspace_id,base_id,generation_id) REFERENCES knowledge_generations(tenant_id,workspace_id,base_id,id),
 CHECK(requested_audience='admin' OR membership_authority IS NOT NULL),
 CHECK((state='running' AND lease_hash IS NOT NULL AND lease_expires_at IS NOT NULL) OR(state<>'running' AND lease_hash IS NULL AND lease_expires_at IS NULL)),
 CHECK((state IN ('failed','cancelled') AND error_code IS NOT NULL) OR(state NOT IN ('failed','cancelled') AND error_code IS NULL)),
 CHECK((state IN ('ready','failed','cancelled') AND finished_at IS NOT NULL) OR(state IN ('pending','running') AND finished_at IS NULL))
);
CREATE INDEX knowledge_pending_attempt_idx ON knowledge_attempts(state,created_at,id) WHERE state IN ('pending','running');
CREATE UNIQUE INDEX knowledge_one_ready_attempt_idx ON knowledge_attempts(generation_id,document_id,source_id) WHERE state='ready';
CREATE UNIQUE INDEX knowledge_one_live_attempt_idx ON knowledge_attempts(generation_id,document_id,source_id) WHERE state IN ('pending','running');
ALTER TABLE knowledge_documents ADD CONSTRAINT knowledge_current_attempt_scope FOREIGN KEY(tenant_id,workspace_id,base_id,id,current_attempt_id) REFERENCES knowledge_attempts(tenant_id,workspace_id,base_id,document_id,id) DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE knowledge_chunks (
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 base_id uuid NOT NULL,
 document_id uuid NOT NULL,
 generation_id uuid NOT NULL,
 source_id uuid NOT NULL,
 attempt_id uuid NOT NULL,
 ordinal integer NOT NULL CHECK(ordinal BETWEEN 0 AND 4095),
 start_byte integer NOT NULL CHECK(start_byte BETWEEN 0 AND 2097151),
 end_byte integer NOT NULL CHECK(end_byte>start_byte AND end_byte<=2097152 AND end_byte-start_byte<=65536),
 token_count integer NOT NULL CHECK(token_count BETWEEN 1 AND 254),
 embedding public.vector(384) NOT NULL CHECK(abs(public.vector_norm(embedding)-1)<0.001),
 PRIMARY KEY(attempt_id,ordinal),
 FOREIGN KEY(tenant_id,workspace_id,base_id,document_id,generation_id,source_id,attempt_id) REFERENCES knowledge_attempts(tenant_id,workspace_id,base_id,document_id,generation_id,source_id,id)
);
CREATE INDEX knowledge_scoped_exact_search_idx ON knowledge_chunks(tenant_id,workspace_id,base_id,generation_id,document_id,source_id);

CREATE TABLE knowledge_mutation_intents (
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 actor_id uuid NOT NULL REFERENCES users(id),
 idempotency_key uuid NOT NULL,
 purpose varchar(24) NOT NULL CHECK(purpose IN ('create_base','upload_source','replace_source','retry_document','rebuild')),
 request_hash bytea NOT NULL CHECK(octet_length(request_hash)=32),
 entity_id uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(tenant_id,workspace_id,actor_id,idempotency_key),
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id)
);

CREATE FUNCTION knowledge_immutable_source() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'KNOWLEDGE_SOURCE_IMMUTABLE' USING ERRCODE='23514'; END $$;
CREATE FUNCTION knowledge_source_retirement() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.superseded_at IS NOT NULL OR NEW.superseded_at IS NULL OR ROW(NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.base_id,NEW.document_id,NEW.source_version,NEW.content,NEW.sha256,NEW.created_by,NEW.created_at) IS DISTINCT FROM ROW(OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.base_id,OLD.document_id,OLD.source_version,OLD.content,OLD.sha256,OLD.created_by,OLD.created_at) THEN
  RAISE EXCEPTION 'KNOWLEDGE_SOURCE_IMMUTABLE' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER knowledge_source_immutable BEFORE UPDATE ON knowledge_source_versions FOR EACH ROW EXECUTE FUNCTION knowledge_source_retirement();
CREATE TRIGGER knowledge_chunk_immutable BEFORE UPDATE ON knowledge_chunks FOR EACH ROW EXECUTE FUNCTION knowledge_immutable_source();
CREATE FUNCTION knowledge_generation_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.base_id,NEW.catalog_sha256,NEW.dimension,NEW.chunk_tokens,NEW.overlap_tokens,NEW.requested_by,NEW.requested_audience,NEW.requested_epoch,NEW.membership_authority,NEW.expected_documents,NEW.created_at) IS DISTINCT FROM ROW(OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.base_id,OLD.catalog_sha256,OLD.dimension,OLD.chunk_tokens,OLD.overlap_tokens,OLD.requested_by,OLD.requested_audience,OLD.requested_epoch,OLD.membership_authority,OLD.expected_documents,OLD.created_at) THEN
  RAISE EXCEPTION 'KNOWLEDGE_GENERATION_IMMUTABLE' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER knowledge_generation_immutable BEFORE UPDATE ON knowledge_generations FOR EACH ROW EXECUTE FUNCTION knowledge_generation_identity();

CREATE FUNCTION knowledge_attempt_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.base_id,NEW.document_id,NEW.source_id,NEW.generation_id,NEW.kind,NEW.attempt_number,NEW.expected_document_revision,NEW.requested_by,NEW.requested_audience,NEW.requested_epoch,NEW.membership_authority,NEW.created_at) IS DISTINCT FROM ROW(OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.base_id,OLD.document_id,OLD.source_id,OLD.generation_id,OLD.kind,OLD.attempt_number,OLD.expected_document_revision,OLD.requested_by,OLD.requested_audience,OLD.requested_epoch,OLD.membership_authority,OLD.created_at) THEN
  RAISE EXCEPTION 'KNOWLEDGE_ATTEMPT_IMMUTABLE' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER knowledge_attempt_immutable BEFORE UPDATE ON knowledge_attempts FOR EACH ROW EXECUTE FUNCTION knowledge_attempt_identity();

INSERT INTO schema_migrations(version,checksum) VALUES(18,'07ec9a499bff9bb4e12137e49cd7243b0439edaa3d0dcd87472f40dcad31a85a');
-- Exact source: services/control/migrations/postgresql/000019_workflows.up.sql
-- Cumulative M13: six schema1 workflow owners after the immutable M12 prefix.
-- M13 bounded workflow drafts, immutable publications and private durable runs.
CREATE TABLE workflows (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 name varchar(120) NOT NULL CHECK(length(btrim(name))>0),
 name_key varchar(240) NOT NULL CHECK(length(btrim(name_key))>0),
 enabled boolean NOT NULL DEFAULT true,
 execution_authority uuid NOT NULL,
 revision bigint NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9007199254740991),
 draft_graph jsonb NOT NULL CHECK(jsonb_typeof(draft_graph)='object' AND octet_length(draft_graph::text)<=1048576),
 draft_sha256 varchar(64) NOT NULL CHECK(draft_sha256~'^[a-f0-9]{64}$'),
 current_version_id uuid,
 created_by uuid NOT NULL REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id)
);
CREATE UNIQUE INDEX workflow_name_idx ON workflows(tenant_id,workspace_id,name_key);
CREATE INDEX workflow_page_idx ON workflows(tenant_id,workspace_id,created_at DESC,id DESC);
CREATE TABLE workflow_versions (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 workflow_id uuid NOT NULL,
 version_number integer NOT NULL CHECK(version_number>0),
 draft_revision bigint NOT NULL CHECK(draft_revision BETWEEN 1 AND 9007199254740991),
 graph jsonb NOT NULL CHECK(jsonb_typeof(graph)='object' AND octet_length(graph::text)<=1048576),
 graph_sha256 varchar(64) NOT NULL CHECK(graph_sha256~'^[a-f0-9]{64}$'),
 published_by uuid NOT NULL REFERENCES users(id),
 published_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,workspace_id,workflow_id,id),
 UNIQUE(tenant_id,workspace_id,workflow_id,version_number),
 FOREIGN KEY(tenant_id,workspace_id,workflow_id) REFERENCES workflows(tenant_id,workspace_id,id)
);
ALTER TABLE workflows ADD CONSTRAINT workflow_current_version_scope FOREIGN KEY(tenant_id,workspace_id,id,current_version_id) REFERENCES workflow_versions(tenant_id,workspace_id,workflow_id,id);
CREATE FUNCTION workflow_immutable_publication() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'WORKFLOW_VERSION_IMMUTABLE'; END $$;
CREATE TRIGGER workflow_publication_immutable BEFORE UPDATE OR DELETE ON workflow_versions FOR EACH ROW EXECUTE FUNCTION workflow_immutable_publication();
CREATE TABLE workflow_conversations (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 workflow_id uuid NOT NULL,
 version_id uuid NOT NULL,
 owner_id uuid NOT NULL REFERENCES users(id),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9007199254740991),
 turn_count integer NOT NULL DEFAULT 0 CHECK(turn_count BETWEEN 0 AND 10000),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 deleted_at timestamptz,
 UNIQUE(tenant_id,workspace_id,workflow_id,id),
 UNIQUE(tenant_id,workspace_id,workflow_id,id,owner_id,version_id),
 FOREIGN KEY(tenant_id,workspace_id,workflow_id,version_id) REFERENCES workflow_versions(tenant_id,workspace_id,workflow_id,id)
);
CREATE INDEX workflow_conversation_page_idx ON workflow_conversations(tenant_id,workspace_id,owner_id,created_at DESC,id DESC) WHERE deleted_at IS NULL;
CREATE TABLE workflow_runs (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 workflow_id uuid NOT NULL,
 owner_id uuid NOT NULL REFERENCES users(id),
 conversation_id uuid,
 turn_number integer CHECK(turn_number BETWEEN 1 AND 10000),
 version_id uuid,
 draft_revision bigint CHECK(draft_revision BETWEEN 1 AND 9007199254740991),
 execution_authority uuid NOT NULL,
 membership_authority uuid NOT NULL,
 requested_audience varchar(6) NOT NULL CHECK(requested_audience IN ('portal','admin')),
 graph_snapshot jsonb NOT NULL CHECK(jsonb_typeof(graph_snapshot)='object' AND octet_length(graph_snapshot::text)<=1048576),
 graph_sha256 varchar(64) NOT NULL CHECK(graph_sha256~'^[a-f0-9]{64}$'),
 state varchar(16) NOT NULL CHECK(state IN ('pending','running','succeeded','failed','cancelled','interrupted')),
 question text NOT NULL CHECK(octet_length(question) BETWEEN 1 AND 32768),
 answer text CHECK(octet_length(answer)<=1048576),
 evidence jsonb CHECK(evidence IS NULL OR jsonb_typeof(evidence)='object'),
 alias_snapshot jsonb CHECK(alias_snapshot IS NULL OR jsonb_typeof(alias_snapshot)='object'),
 usage jsonb CHECK(usage IS NULL OR jsonb_typeof(usage)='object'),
 error_code varchar(100),
 process_owner uuid,
 lease_id uuid,
 lease_expires_at timestamptz,
 deadline_at timestamptz NOT NULL,
 cancellation_requested_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 started_at timestamptz,
 finished_at timestamptz,
 UNIQUE(tenant_id,workspace_id,workflow_id,id),
 UNIQUE(conversation_id,turn_number),
 FOREIGN KEY(tenant_id,workspace_id,workflow_id) REFERENCES workflows(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,workflow_id,version_id) REFERENCES workflow_versions(tenant_id,workspace_id,workflow_id,id),
 FOREIGN KEY(tenant_id,workspace_id,workflow_id,conversation_id,owner_id,version_id) REFERENCES workflow_conversations(tenant_id,workspace_id,workflow_id,id,owner_id,version_id),
 CHECK((version_id IS NOT NULL AND draft_revision IS NULL AND conversation_id IS NOT NULL AND turn_number IS NOT NULL) OR (version_id IS NULL AND draft_revision IS NOT NULL AND conversation_id IS NULL AND turn_number IS NULL)),
 CHECK((state IN ('pending','running') AND finished_at IS NULL) OR (state NOT IN ('pending','running') AND finished_at IS NOT NULL)),
 CHECK(state='succeeded' OR answer IS NULL),
 CHECK(state<>'succeeded' OR (answer IS NOT NULL AND evidence IS NOT NULL AND alias_snapshot IS NOT NULL AND usage IS NOT NULL AND error_code IS NULL)),
 CHECK((state='running' AND process_owner IS NOT NULL AND lease_id IS NOT NULL AND lease_expires_at IS NOT NULL AND started_at IS NOT NULL) OR (state<>'running' AND process_owner IS NULL AND lease_id IS NULL AND lease_expires_at IS NULL))
);
CREATE UNIQUE INDEX workflow_one_active_turn_idx ON workflow_runs(conversation_id) WHERE conversation_id IS NOT NULL AND state IN ('pending','running');
CREATE INDEX workflow_active_workspace_idx ON workflow_runs(tenant_id,workspace_id,state,lease_expires_at) WHERE state IN ('pending','running');
CREATE INDEX workflow_run_page_idx ON workflow_runs(tenant_id,workspace_id,owner_id,created_at DESC,id DESC);
CREATE TABLE workflow_node_traces (
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 workflow_id uuid NOT NULL,
 run_id uuid NOT NULL,
 node_id uuid NOT NULL,
 kind varchar(16) NOT NULL CHECK(kind IN ('start','retrieval','answer','end')),
 state varchar(16) NOT NULL CHECK(state IN ('not_started','running','succeeded','failed','cancelled','interrupted')),
 result_metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK(jsonb_typeof(result_metadata)='object' AND octet_length(result_metadata::text)<=65536),
 error_code varchar(100),
 started_at timestamptz,
 finished_at timestamptz,
 PRIMARY KEY(run_id,node_id),
 UNIQUE(run_id,kind),
 FOREIGN KEY(tenant_id,workspace_id,workflow_id,run_id) REFERENCES workflow_runs(tenant_id,workspace_id,workflow_id,id) ON DELETE CASCADE,
 CHECK((state='not_started' AND started_at IS NULL AND finished_at IS NULL) OR (state='running' AND started_at IS NOT NULL AND finished_at IS NULL) OR (state IN ('succeeded','failed','cancelled','interrupted') AND finished_at IS NOT NULL))
);
CREATE TABLE workflow_mutation_intents (
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 actor_id uuid NOT NULL REFERENCES users(id),
 idempotency_key uuid NOT NULL,
 purpose varchar(64) NOT NULL,
 request_hash bytea NOT NULL CHECK(octet_length(request_hash)=32),
 entity_id uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(tenant_id,workspace_id,actor_id,idempotency_key),
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id)
);

CREATE FUNCTION workflow_run_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.workflow_id,NEW.owner_id,NEW.conversation_id,NEW.turn_number,NEW.version_id,NEW.draft_revision,NEW.execution_authority,NEW.membership_authority,NEW.requested_audience,NEW.graph_snapshot,NEW.graph_sha256,NEW.question,NEW.deadline_at,NEW.created_at)
 IS DISTINCT FROM ROW(OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.workflow_id,OLD.owner_id,OLD.conversation_id,OLD.turn_number,OLD.version_id,OLD.draft_revision,OLD.execution_authority,OLD.membership_authority,OLD.requested_audience,OLD.graph_snapshot,OLD.graph_sha256,OLD.question,OLD.deadline_at,OLD.created_at) THEN
  RAISE EXCEPTION 'WORKFLOW_RUN_IDENTITY_IMMUTABLE';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER workflow_run_identity_guard BEFORE UPDATE ON workflow_runs FOR EACH ROW EXECUTE FUNCTION workflow_run_identity();

-- This composed capability supports only the original knowledge graph family.
ALTER TABLE workflows ADD CONSTRAINT workflow_schema1_draft CHECK(draft_graph->>'schema_version'='1');
ALTER TABLE workflow_versions ADD CONSTRAINT workflow_schema1_version CHECK(graph->>'schema_version'='1');
ALTER TABLE workflow_runs ADD CONSTRAINT workflow_schema1_run CHECK(graph_snapshot->>'schema_version'='1');

INSERT INTO schema_migrations(version,checksum) VALUES(19,'72d708808e284a7da92332f3d4c8303cedaf17cdcd7142387dcbfabb25ba47ec');
-- Exact source: services/control/migrations/postgresql/000020_plugins.up.sql
-- Cumulative M14 after the immutable19-entry account prefix.
ALTER TABLE workflows DROP CONSTRAINT workflow_schema1_draft;
ALTER TABLE workflow_versions DROP CONSTRAINT workflow_schema1_version;
ALTER TABLE workflow_runs DROP CONSTRAINT workflow_schema1_run;
-- M14 immutable signed builtin tool provenance and current Workspace grants.
CREATE TABLE plugin_versions (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL REFERENCES tenants(id),
 plugin_id varchar(100) NOT NULL,
 release_version varchar(32) NOT NULL,
 version_major integer NOT NULL CHECK(version_major=1),
 version_minor integer NOT NULL CHECK(version_minor BETWEEN 0 AND 9999),
 version_patch integer NOT NULL CHECK(version_patch BETWEEN 0 AND 9999),
 abi varchar(100) NOT NULL,
 executor varchar(100) NOT NULL,
 capsule bytea NOT NULL CHECK(octet_length(capsule) BETWEEN 1 AND 1048576),
 capsule_sha256 varchar(64) NOT NULL CHECK(capsule_sha256~'^[a-f0-9]{64}$'),
 manifest jsonb NOT NULL CHECK(jsonb_typeof(manifest)='object' AND octet_length(manifest::text)<=16384),
 manifest_sha256 varchar(64) NOT NULL CHECK(manifest_sha256~'^[a-f0-9]{64}$'),
 key_id varchar(64) NOT NULL CHECK(key_id~'^[a-f0-9]{64}$'),
 state varchar(16) NOT NULL CHECK(state IN ('imported','installed','uninstalled')),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9007199254740991),
 imported_by uuid NOT NULL REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 installed_at timestamptz,
 uninstalled_at timestamptz,
 UNIQUE(tenant_id,id),
 UNIQUE(tenant_id,plugin_id,release_version),
 UNIQUE(tenant_id,capsule_sha256),
 CHECK(state<>'installed' OR installed_at IS NOT NULL),
 CHECK(release_version=version_major::text||'.'||version_minor::text||'.'||version_patch::text)
);
CREATE INDEX plugin_version_page_idx ON plugin_versions(tenant_id,created_at DESC,id DESC);
CREATE FUNCTION plugin_immutable_provenance() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'PLUGIN_PROVENANCE_IMMUTABLE'; END IF;
 IF ROW(NEW.id,NEW.tenant_id,NEW.plugin_id,NEW.release_version,NEW.version_major,NEW.version_minor,NEW.version_patch,NEW.abi,NEW.executor,NEW.capsule,NEW.capsule_sha256,NEW.manifest,NEW.manifest_sha256,NEW.key_id,NEW.imported_by,NEW.created_at)
 IS DISTINCT FROM ROW(OLD.id,OLD.tenant_id,OLD.plugin_id,OLD.release_version,OLD.version_major,OLD.version_minor,OLD.version_patch,OLD.abi,OLD.executor,OLD.capsule,OLD.capsule_sha256,OLD.manifest,OLD.manifest_sha256,OLD.key_id,OLD.imported_by,OLD.created_at) THEN
  RAISE EXCEPTION 'PLUGIN_PROVENANCE_IMMUTABLE';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER plugin_provenance_immutable BEFORE UPDATE OR DELETE ON plugin_versions FOR EACH ROW EXECUTE FUNCTION plugin_immutable_provenance();
CREATE TABLE plugin_grants (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 plugin_version_id uuid NOT NULL,
 enabled boolean NOT NULL DEFAULT true,
 authority_id uuid NOT NULL,
 revision bigint NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9007199254740991),
 granted_by uuid NOT NULL REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,workspace_id,plugin_version_id),
 UNIQUE(tenant_id,workspace_id,id,plugin_version_id),
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id),
 FOREIGN KEY(tenant_id,plugin_version_id) REFERENCES plugin_versions(tenant_id,id)
);
CREATE INDEX plugin_grant_page_idx ON plugin_grants(tenant_id,workspace_id,created_at DESC,id DESC);
CREATE TABLE plugin_mutation_intents (
 tenant_id uuid NOT NULL REFERENCES tenants(id),
 actor_id uuid NOT NULL REFERENCES users(id),
 idempotency_key uuid NOT NULL,
 purpose varchar(100) NOT NULL,
 request_hash bytea NOT NULL CHECK(octet_length(request_hash)=32),
 entity_id uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(tenant_id,actor_id,idempotency_key)
);
CREATE INDEX plugin_intent_retention_idx ON plugin_mutation_intents(tenant_id,created_at,idempotency_key);
CREATE TABLE workflow_plugin_references (
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 workflow_id uuid NOT NULL,
 workflow_version_id uuid NOT NULL,
 plugin_version_id uuid NOT NULL,
 grant_id uuid NOT NULL,
 publication_grant_authority uuid NOT NULL,
 PRIMARY KEY(workflow_version_id,plugin_version_id),
 FOREIGN KEY(tenant_id,workspace_id,workflow_id,workflow_version_id) REFERENCES workflow_versions(tenant_id,workspace_id,workflow_id,id),
 FOREIGN KEY(tenant_id,workspace_id,grant_id,plugin_version_id) REFERENCES plugin_grants(tenant_id,workspace_id,id,plugin_version_id)
);
CREATE TRIGGER workflow_plugin_reference_immutable BEFORE UPDATE OR DELETE ON workflow_plugin_references FOR EACH ROW EXECUTE FUNCTION workflow_immutable_publication();
ALTER TABLE workflow_runs ADD COLUMN result_kind varchar(16) NOT NULL DEFAULT 'answer' CHECK(result_kind IN ('answer','model_list'));
ALTER TABLE workflow_runs ADD COLUMN tool_result jsonb CHECK(tool_result IS NULL OR jsonb_typeof(tool_result)='object' AND octet_length(tool_result::text)<=1048576);
ALTER TABLE workflow_runs ADD COLUMN tool_proof jsonb CHECK(tool_proof IS NULL OR jsonb_typeof(tool_proof)='object' AND octet_length(tool_proof::text)<=65536);
-- Replace exactly the three M13 answer-specific checks, independent of generated
-- constraint names. An unexpected input schema aborts rather than widening it.
DO $$ DECLARE item record; found integer:=0;
BEGIN
 FOR item IN SELECT conname FROM pg_constraint WHERE conrelid='workflow_runs'::regclass AND contype='c' AND (SELECT attnum FROM pg_attribute WHERE attrelid='workflow_runs'::regclass AND attname='answer')=ANY(conkey) LOOP
  EXECUTE format('ALTER TABLE workflow_runs DROP CONSTRAINT %I',item.conname); found:=found+1;
 END LOOP;
 IF found<>3 THEN RAISE EXCEPTION 'PLUGIN_WORKFLOW_INPUT_SCHEMA_UNEXPECTED'; END IF;
END $$;
ALTER TABLE workflow_runs ADD CONSTRAINT workflow_answer_bytes CHECK(answer IS NULL OR octet_length(answer)<=1048576);
ALTER TABLE workflow_runs ADD CONSTRAINT workflow_non_success_result_absent CHECK(state='succeeded' OR (answer IS NULL AND tool_result IS NULL AND tool_proof IS NULL));
ALTER TABLE workflow_runs ADD CONSTRAINT workflow_success_result_required CHECK(state<>'succeeded' OR (error_code IS NULL AND ((result_kind='answer' AND answer IS NOT NULL AND evidence IS NOT NULL AND alias_snapshot IS NOT NULL AND usage IS NOT NULL AND tool_result IS NULL AND tool_proof IS NULL) OR (result_kind='model_list' AND answer IS NULL AND evidence IS NULL AND alias_snapshot IS NULL AND usage IS NULL AND tool_result IS NOT NULL AND tool_proof IS NOT NULL))));
ALTER TABLE workflow_runs ADD CONSTRAINT workflow_result_graph_kind CHECK((result_kind='answer' AND graph_snapshot->>'schema_version'='1') OR (result_kind='model_list' AND graph_snapshot->>'schema_version'='2'));
ALTER TABLE workflow_node_traces DROP CONSTRAINT workflow_node_traces_kind_check;
ALTER TABLE workflow_node_traces ADD CONSTRAINT workflow_node_traces_kind_check CHECK(kind IN ('start','retrieval','answer','end','tool'));
CREATE OR REPLACE FUNCTION workflow_run_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.workflow_id,NEW.owner_id,NEW.conversation_id,NEW.turn_number,NEW.version_id,NEW.draft_revision,NEW.execution_authority,NEW.membership_authority,NEW.requested_audience,NEW.graph_snapshot,NEW.graph_sha256,NEW.question,NEW.deadline_at,NEW.created_at,NEW.result_kind)
 IS DISTINCT FROM ROW(OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.workflow_id,OLD.owner_id,OLD.conversation_id,OLD.turn_number,OLD.version_id,OLD.draft_revision,OLD.execution_authority,OLD.membership_authority,OLD.requested_audience,OLD.graph_snapshot,OLD.graph_sha256,OLD.question,OLD.deadline_at,OLD.created_at,OLD.result_kind) THEN
  RAISE EXCEPTION 'WORKFLOW_RUN_IDENTITY_IMMUTABLE';
 END IF;
 RETURN NEW;
END $$;

ALTER TABLE workflows ADD CONSTRAINT workflow_supported_draft CHECK(draft_graph->>'schema_version' IN ('1','2'));
ALTER TABLE workflow_versions ADD CONSTRAINT workflow_supported_version CHECK(graph->>'schema_version' IN ('1','2'));

INSERT INTO schema_migrations(version,checksum) VALUES(20,'1599c8237e70b649c792452858f0b80dcd8cf123dc1f33a3bcc52309be8d4318');
-- Exact source: services/control/migrations/postgresql/000021_portal.up.sql
-- Forward cumulative M15: preserve the exact twenty-entry M14 prefix.
-- M15 private preferences and atomic terminal notices; no historical run backfill.
CREATE TABLE user_preferences (
 user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 theme varchar(8) NOT NULL DEFAULT 'light' CHECK(theme IN ('light','dark')),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision>=1 AND revision<=9007199254740991),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
INSERT INTO user_preferences(user_id) SELECT id FROM users;
CREATE FUNCTION initialize_user_preferences() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO user_preferences(user_id) VALUES(NEW.id);
 RETURN NEW;
END;$$;
CREATE TRIGGER user_preferences_initialize AFTER INSERT ON users FOR EACH ROW EXECUTE FUNCTION initialize_user_preferences();
CREATE FUNCTION protect_user_preferences() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.user_id IS DISTINCT FROM OLD.user_id THEN RAISE EXCEPTION 'PREFERENCE_OWNER_IMMUTABLE';END IF;
 IF NEW.revision<>OLD.revision+1 THEN RAISE EXCEPTION 'PREFERENCE_REVISION_MUST_ADVANCE';END IF;
 RETURN NEW;
END;$$;
CREATE TRIGGER user_preferences_revision BEFORE UPDATE ON user_preferences FOR EACH ROW EXECUTE FUNCTION protect_user_preferences();

ALTER TABLE workflow_runs ADD CONSTRAINT workflow_run_recipient_identity UNIQUE(tenant_id,workspace_id,workflow_id,id,owner_id);
CREATE TABLE terminal_notifications (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 workflow_id uuid NOT NULL,
 run_id uuid NOT NULL UNIQUE,
 recipient_id uuid NOT NULL,
 outcome varchar(16) NOT NULL CHECK(outcome IN ('succeeded','failed','cancelled','interrupted')),
 error_code varchar(100) CHECK(error_code IS NULL OR error_code ~ '^[A-Z][A-Z0-9_]{0,99}$'),
 occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 read_at timestamptz CHECK(read_at IS NULL OR read_at>=occurred_at),
 CHECK((outcome='succeeded')=(error_code IS NULL)),
 FOREIGN KEY(tenant_id,workspace_id,workflow_id,run_id,recipient_id) REFERENCES workflow_runs(tenant_id,workspace_id,workflow_id,id,owner_id) ON DELETE CASCADE
);
CREATE INDEX terminal_notification_recipient_page_idx ON terminal_notifications(recipient_id,occurred_at DESC,id DESC);
CREATE INDEX terminal_notification_unread_idx ON terminal_notifications(recipient_id,occurred_at DESC,id DESC) WHERE read_at IS NULL;
CREATE INDEX terminal_notification_retention_idx ON terminal_notifications(tenant_id,occurred_at,id);
CREATE INDEX application_conversation_updated_idx ON workflow_conversations(tenant_id,workspace_id,owner_id,updated_at DESC,id DESC) WHERE deleted_at IS NULL;
CREATE FUNCTION create_terminal_notification() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.state IN ('pending','running') AND NEW.state IN ('succeeded','failed','cancelled','interrupted') THEN
  INSERT INTO terminal_notifications(tenant_id,workspace_id,workflow_id,run_id,recipient_id,outcome,error_code)
  VALUES(NEW.tenant_id,NEW.workspace_id,NEW.workflow_id,NEW.id,NEW.owner_id,NEW.state,NEW.error_code)
  ON CONFLICT(run_id) DO NOTHING;
 END IF;
 RETURN NEW;
END;$$;
CREATE TRIGGER workflow_terminal_notification AFTER UPDATE OF state ON workflow_runs FOR EACH ROW EXECUTE FUNCTION create_terminal_notification();
CREATE FUNCTION protect_terminal_notification() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.workflow_id,NEW.run_id,NEW.recipient_id,NEW.outcome,NEW.error_code,NEW.occurred_at)
 IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.workflow_id,OLD.run_id,OLD.recipient_id,OLD.outcome,OLD.error_code,OLD.occurred_at)
 THEN RAISE EXCEPTION 'NOTIFICATION_PROVENANCE_IMMUTABLE';END IF;
 IF OLD.read_at IS NOT NULL AND NEW.read_at IS DISTINCT FROM OLD.read_at THEN RAISE EXCEPTION 'NOTIFICATION_READ_TIME_IMMUTABLE';END IF;
 RETURN NEW;
END;$$;
CREATE TRIGGER terminal_notification_immutable BEFORE UPDATE ON terminal_notifications FOR EACH ROW EXECUTE FUNCTION protect_terminal_notification();

CREATE FUNCTION require_terminal_notification_provenance() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM terminal_notifications WHERE id=NEW.id) THEN RETURN NEW;END IF;
 IF NOT EXISTS(SELECT 1 FROM workflow_runs r WHERE r.tenant_id=NEW.tenant_id AND r.workspace_id=NEW.workspace_id AND r.workflow_id=NEW.workflow_id AND r.id=NEW.run_id AND r.owner_id=NEW.recipient_id AND r.state=NEW.outcome AND r.error_code IS NOT DISTINCT FROM NEW.error_code AND r.finished_at IS NOT NULL)
 THEN RAISE EXCEPTION 'NOTIFICATION_RUN_PROVENANCE_REQUIRED';END IF;
 RETURN NEW;
END;$$;
CREATE CONSTRAINT TRIGGER terminal_notification_provenance AFTER INSERT OR UPDATE ON terminal_notifications DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION require_terminal_notification_provenance();

INSERT INTO schema_migrations(version,checksum) VALUES(21,'84f937a94229d4adbee4669031878c27421364e2d821938c4a2e5346c09917f8');
-- Exact source: services/control/migrations/postgresql/000022_localization.up.sql
-- Forward-only account22: immutable account1-21 remain unchanged.
ALTER TABLE users DROP CONSTRAINT users_locale_check;
ALTER TABLE users ALTER COLUMN locale TYPE varchar(35);
ALTER TABLE users ADD CONSTRAINT users_locale_check CHECK (length(locale) BETWEEN 2 AND 35 AND locale COLLATE "C" ~ '^[a-z]{2,3}(-[A-Z][a-z]{3})?(-([A-Z]{2}|[0-9]{3}))?(-([a-z0-9]{5,8}|[0-9][a-z0-9]{3})){0,3}$');
ALTER TABLE invitations DROP CONSTRAINT invitations_locale_check;
ALTER TABLE invitations ALTER COLUMN locale TYPE varchar(35);
ALTER TABLE invitations ADD CONSTRAINT invitations_locale_check CHECK (length(locale) BETWEEN 2 AND 35 AND locale COLLATE "C" ~ '^[a-z]{2,3}(-[A-Z][a-z]{3})?(-([A-Z]{2}|[0-9]{3}))?(-([a-z0-9]{5,8}|[0-9][a-z0-9]{3})){0,3}$');
ALTER TABLE mail_deliveries DROP CONSTRAINT mail_deliveries_locale_check;
ALTER TABLE mail_deliveries ALTER COLUMN locale TYPE varchar(35);
ALTER TABLE mail_deliveries ADD CONSTRAINT mail_deliveries_locale_check CHECK (length(locale) BETWEEN 2 AND 35 AND locale COLLATE "C" ~ '^[a-z]{2,3}(-[A-Z][a-z]{3})?(-([A-Z]{2}|[0-9]{3}))?(-([a-z0-9]{5,8}|[0-9][a-z0-9]{3})){0,3}$');

INSERT INTO schema_migrations(version,checksum) VALUES(22,'016fb8991d3f25a346bc24a1baa529dcc8c00259fdd177eda8956516dc2095c6');
-- Exact source: services/control/migrations/postgresql/000023_cumulative_catalog.up.sql
-- Forward-only account22 evolution. Never replay the independent platform ledger.
-- Preserve accepted membership/session/component/review/GPU identity constraints.
-- Digest values are already checked as exactly 64 hex bytes: this conversion
-- preserves every value and gives the full repository its canonical SQL types.
ALTER TABLE deployments DROP CONSTRAINT deployment_model_variant;
ALTER TABLE deployments ALTER COLUMN catalog_digest TYPE char(64);
ALTER TABLE deployments ALTER COLUMN catalog_digest SET DEFAULT '4d32aa5cd4b2d86f12beff791f0428f0813c2d3479041080d13b0be6bc6842bc';
ALTER TABLE deployments ALTER COLUMN serving_recipe_sha256 TYPE char(64);
ALTER TABLE deployments ADD CONSTRAINT deployment_model_variant CHECK(
 (model_kind='approved_base' AND catalog_digest='4d32aa5cd4b2d86f12beff791f0428f0813c2d3479041080d13b0be6bc6842bc' AND serving_recipe_sha256 IS NULL)
 OR (model_kind='merged_lora' AND catalog_digest~'^[a-f0-9]{64}$' AND catalog_digest<>'4d32aa5cd4b2d86f12beff791f0428f0813c2d3479041080d13b0be6bc6842bc' AND serving_recipe_sha256~'^[a-f0-9]{64}$' AND serving_recipe_sha256 IS NOT NULL)
);
-- Keep the scoped reference; its historical generated name becomes canonical.
ALTER TABLE managed_components RENAME CONSTRAINT managed_components_tenant_id_workspace_id_node_id_last_tas_fkey TO components_task_scope_fk;
ALTER TABLE managed_components ADD CONSTRAINT managed_components_last_task_id_fkey FOREIGN KEY(last_task_id) REFERENCES node_tasks(id);
-- The old chain already has immutable snapshots. Fill only a genuinely missing
-- current snapshot using the original scoped model/deployment/history identities;
-- the existing BEFORE INSERT trigger independently checks the current target.
ALTER TABLE model_alias_target_versions DROP CONSTRAINT model_alias_target_versions_action_check;
ALTER TABLE model_alias_target_versions ADD CONSTRAINT model_alias_target_versions_action_check CHECK(action IN ('migration_current','published','disabled','paused','resumed','retargeted','rolled_back'));
INSERT INTO model_alias_target_versions(id,tenant_id,workspace_id,alias_id,revision,action,deployment_id,deployment_revision,model_id,model_kind,catalog_digest,serving_recipe_sha256)
 SELECT gen_random_uuid(),a.tenant_id,a.workspace_id,a.id,a.revision,'migration_current',d.id,a.deployment_revision,d.model_id,d.model_kind,d.catalog_digest,d.serving_recipe_sha256
 FROM model_aliases a JOIN deployments d ON d.tenant_id=a.tenant_id AND d.workspace_id=a.workspace_id AND d.id=a.deployment_id
 WHERE NOT EXISTS(SELECT 1 FROM model_alias_target_versions v WHERE v.tenant_id=a.tenant_id AND v.workspace_id=a.workspace_id AND v.alias_id=a.id AND v.revision=a.revision);
-- Reject corrupt old provenance; never manufacture an approval or replace IDs.
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM model_preparation_reviews WHERE NOT
  ((decision='approve' AND model_id IS NOT NULL AND model_id=artifact_id AND length(model_name)>0)
   OR (decision='reject' AND model_id IS NULL AND model_name='')))
 THEN RAISE EXCEPTION 'MODEL_REVIEW_IDENTITY_INCOMPATIBLE'; END IF;
END $$;

INSERT INTO schema_migrations(version,checksum) VALUES(23,'67b7498071723346f9a002e895d4aa80744452381716dc2237c7e05227e307c9');
-- Exact source: services/control/migrations/postgresql/000024_external_imports.up.sql
-- New account22-derived MVP lineage; immutable legacy assets are unchanged.
-- MongoDB is an external read-only source, never the platform metadata database.
CREATE TABLE mongodb_dataset_imports (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 dataset_id uuid NOT NULL,
 requested_by uuid NOT NULL REFERENCES users(id),
 creation_key uuid NOT NULL,
 intent bytea NOT NULL CHECK(octet_length(intent)=32),
 request jsonb NOT NULL CHECK(jsonb_typeof(request)='object'),
 source_grant jsonb NOT NULL CHECK(jsonb_typeof(source_grant)='object'),
 source_id text NOT NULL,
 state text NOT NULL CHECK(state IN ('reading','exported','imported','failed')),
 attempt_id uuid NOT NULL,
 attempt_count integer NOT NULL CHECK(attempt_count BETWEEN 1 AND 20),
 lease_expires_at timestamptz,
 object_id uuid NOT NULL,
 retained_objects jsonb NOT NULL DEFAULT '[]'::jsonb CHECK(jsonb_typeof(retained_objects)='array'),
 source_sha256 text,
 source_bytes bigint,
 source_rows bigint,
 first_id text,
 last_id text,
 draft_id uuid,
 error_code text,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(tenant_id,workspace_id,dataset_id,requested_by,creation_key),
 FOREIGN KEY(tenant_id,workspace_id,dataset_id) REFERENCES datasets(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,dataset_id,draft_id) REFERENCES dataset_drafts(tenant_id,workspace_id,dataset_id,id),
 CHECK ((source_sha256 IS NULL AND source_bytes IS NULL AND source_rows IS NULL AND first_id IS NULL AND last_id IS NULL) OR
        (source_sha256 IS NOT NULL AND source_bytes IS NOT NULL AND source_rows IS NOT NULL AND first_id IS NOT NULL AND last_id IS NOT NULL AND source_sha256 ~ '^[0-9a-f]{64}$' AND source_bytes>0 AND source_rows>0 AND first_id ~ '^[0-9a-f]{24}$' AND last_id ~ '^[0-9a-f]{24}$' AND first_id<=last_id)),
 CHECK(state NOT IN ('exported','imported') OR source_sha256 IS NOT NULL),
 CHECK(state<>'imported' OR draft_id IS NOT NULL),
 CHECK((state IN ('reading','exported') AND lease_expires_at IS NOT NULL) OR (state IN ('failed','imported') AND lease_expires_at IS NULL))
);
CREATE INDEX mongodb_dataset_import_lease_idx ON mongodb_dataset_imports(tenant_id,lease_expires_at) WHERE lease_expires_at IS NOT NULL;

CREATE TABLE aws_discovered_resources (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 account_id varchar(12) NOT NULL CHECK (account_id ~ '^[0-9]{12}$'),
 region varchar(64) NOT NULL,
 instance_id varchar(32) NOT NULL,
 observation jsonb NOT NULL CHECK (jsonb_typeof(observation)='object'),
 observed_at timestamptz NOT NULL,
 missing boolean NOT NULL DEFAULT false,
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id),
 UNIQUE(tenant_id,workspace_id,account_id,region,instance_id)
);
CREATE INDEX aws_resource_scope ON aws_discovered_resources(tenant_id,workspace_id,id);
CREATE TABLE aws_import_receipts (
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 idempotency_key uuid NOT NULL,
 actor_id uuid NOT NULL REFERENCES users(id),
 connection_name varchar(48) NOT NULL,
 account_id varchar(12) NOT NULL CHECK (account_id ~ '^[0-9]{12}$'),
 region varchar(64) NOT NULL,
 started_at timestamptz NOT NULL,
 completed_at timestamptz NOT NULL CHECK (completed_at>=started_at),
 imported_count integer NOT NULL CHECK (imported_count>=0),
 PRIMARY KEY(tenant_id,workspace_id,idempotency_key),
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id)
);
CREATE INDEX aws_receipt_snapshot ON aws_import_receipts(tenant_id,workspace_id,account_id,region,started_at);
CREATE FUNCTION aws_receipt_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'AWS_RECEIPT_IMMUTABLE'; END;
$$;
CREATE TRIGGER aws_receipt_immutable BEFORE UPDATE OR DELETE ON aws_import_receipts FOR EACH ROW EXECUTE FUNCTION aws_receipt_immutable();

INSERT INTO schema_migrations(version,checksum) VALUES(24,'0b0cda98b40b8c9f55d797c146f6a793020438a755423a58949cc7b713742216');
-- Exact source: services/control/migrations/postgresql/000025_artifact_objects.up.sql
-- New account22-derived MVP lineage; immutable legacy assets are unchanged.
-- Private artifact ingress; formal schema follows AWS000022.
CREATE TABLE artifact_objects (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, workspace_id uuid NOT NULL,
 created_by uuid NOT NULL REFERENCES users(id), requested_audience varchar(8) NOT NULL CHECK(requested_audience IN ('admin','portal')),
 requested_epoch bigint NOT NULL CHECK(requested_epoch>0), membership_authority uuid,
 execution_epoch bigint NOT NULL CHECK(execution_epoch>0),execution_membership uuid,
 creation_key uuid NOT NULL, fingerprint bytea NOT NULL CHECK(octet_length(fingerprint)=32),
 store_id uuid NOT NULL, name varchar(200) NOT NULL, purpose varchar(24) NOT NULL CHECK(purpose IN ('model_bundle','dataset_source')),
 expected_bytes bigint NOT NULL CHECK(expected_bytes BETWEEN 1 AND 5497558138880), sha256 char(64) NOT NULL CHECK(sha256~'^[a-f0-9]{64}$'),
 part_bytes bigint NOT NULL CHECK(part_bytes BETWEEN 5242880 AND 5368709120),
 state varchar(16) NOT NULL CHECK(state IN ('creating','uploading','completing','ready','deleting','deleted','aborting','aborted')),
 remote_upload_id varchar(4096), remote_etag varchar(1024),
 operation_id uuid, operation_kind varchar(16), operation_phase varchar(16), operation_expires_at timestamptz,operation_authority jsonb,
 error_code varchar(64), revision bigint NOT NULL DEFAULT 1 CHECK(revision>0),
 upload_expires_at timestamptz NOT NULL, expires_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,workspace_id,id), UNIQUE(tenant_id,workspace_id,created_by,creation_key),
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id),
 CHECK((operation_id IS NULL)=(operation_kind IS NULL)),CHECK((operation_id IS NULL)=(operation_phase IS NULL)),
 CHECK(operation_phase IS NULL OR operation_phase IN ('prepared','sent')),
 CHECK(operation_kind IS NULL OR operation_kind IN ('create','part','complete','delete','abort','ingest')),
 CHECK(state<>'ready' OR remote_etag IS NOT NULL),CHECK((operation_id IS NULL)=(operation_expires_at IS NULL)),
 CHECK(expires_at>=upload_expires_at),CHECK((expected_bytes+part_bytes-1)/part_bytes<=10000),
 CHECK((operation_id IS NULL)=(operation_authority IS NULL))
);
CREATE TABLE artifact_object_parts (
 tenant_id uuid NOT NULL,workspace_id uuid NOT NULL,object_id uuid NOT NULL,number integer NOT NULL CHECK(number BETWEEN 1 AND 10000),
 bytes bigint NOT NULL CHECK(bytes BETWEEN 1 AND 5368709120),sha256 char(64) NOT NULL CHECK(sha256~'^[a-f0-9]{64}$'),
 etag varchar(1024),state varchar(16) NOT NULL CHECK(state IN ('intent','uploaded')),stage_id uuid NOT NULL,
 PRIMARY KEY(object_id,number),FOREIGN KEY(tenant_id,workspace_id,object_id) REFERENCES artifact_objects(tenant_id,workspace_id,id),
 CHECK((state='uploaded')=(etag IS NOT NULL))
);
CREATE TABLE artifact_object_grants (
 id uuid PRIMARY KEY,tenant_id uuid NOT NULL,workspace_id uuid NOT NULL,object_id uuid NOT NULL,
 recipient_user_id uuid NOT NULL REFERENCES users(id),created_by uuid NOT NULL REFERENCES users(id),
 creator_epoch bigint NOT NULL,creator_membership uuid,recipient_epoch bigint NOT NULL,recipient_membership uuid NOT NULL,
 action varchar(24) NOT NULL CHECK(action IN ('read','ingest_model','ingest_dataset')),
 creation_key uuid NOT NULL,fingerprint bytea NOT NULL CHECK(octet_length(fingerprint)=32),
 not_before timestamptz NOT NULL,expires_at timestamptz NOT NULL,revoked_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,workspace_id,object_id,id),UNIQUE(tenant_id,workspace_id,object_id,created_by,creation_key),FOREIGN KEY(tenant_id,workspace_id,object_id) REFERENCES artifact_objects(tenant_id,workspace_id,id),
 CHECK(expires_at>not_before)
);
CREATE TABLE artifact_object_ingestions (
 id uuid PRIMARY KEY,tenant_id uuid NOT NULL,workspace_id uuid NOT NULL,object_id uuid NOT NULL,
 created_by uuid NOT NULL REFERENCES users(id),creation_key uuid NOT NULL,fingerprint bytea NOT NULL CHECK(octet_length(fingerprint)=32),
 kind varchar(16) NOT NULL CHECK(kind IN ('model','dataset')),model_import_id uuid,dataset_draft_id uuid,target_dataset_id uuid,
 state varchar(16) NOT NULL CHECK(state IN ('intent','running','submitted','failed')),
 source_verified_sha256 char(64),source_verified_at timestamptz,
 input jsonb NOT NULL CHECK(jsonb_typeof(input)='object' AND octet_length(input::text)<=16384),
 receipt jsonb CHECK(receipt IS NULL OR jsonb_typeof(receipt)='object' AND octet_length(receipt::text)<=16384),
 error_code varchar(64),created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,workspace_id,object_id,created_by,creation_key),
 FOREIGN KEY(tenant_id,workspace_id,object_id) REFERENCES artifact_objects(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,target_dataset_id) REFERENCES datasets(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,model_import_id) REFERENCES model_imports(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,target_dataset_id,dataset_draft_id) REFERENCES dataset_drafts(tenant_id,workspace_id,dataset_id,id),
 CHECK((kind='model' AND dataset_draft_id IS NULL AND target_dataset_id IS NULL) OR (kind='dataset' AND model_import_id IS NULL AND target_dataset_id IS NOT NULL)),
 CHECK(state<>'submitted' OR (kind='model' AND model_import_id IS NOT NULL) OR (kind='dataset' AND dataset_draft_id IS NOT NULL))
);
CREATE INDEX artifact_objects_scope_page ON artifact_objects(tenant_id,workspace_id,created_at DESC,id DESC);
CREATE INDEX artifact_grant_recipient ON artifact_object_grants(tenant_id,workspace_id,recipient_user_id,object_id);

-- Inactive candidate; one complete PostgreSQL statement.
CREATE FUNCTION m22_artifact_objects_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.state<>'creating' OR NEW.remote_upload_id IS NOT NULL OR NEW.remote_etag IS NOT NULL OR NEW.operation_id IS NULL OR NEW.operation_kind<>'create' OR NEW.operation_phase<>'prepared' OR NEW.operation_authority IS NULL OR NEW.execution_epoch<>NEW.requested_epoch OR NEW.execution_membership IS DISTINCT FROM NEW.membership_authority THEN RAISE EXCEPTION 'OBJECT_STORED_PROVENANCE_INVALID' USING ERRCODE='23514'; END IF;
 ELSE
  IF (OLD.remote_upload_id IS NOT NULL AND (NEW.remote_upload_id IS DISTINCT FROM OLD.remote_upload_id)) OR (OLD.remote_etag IS NOT NULL AND (NEW.remote_etag IS DISTINCT FROM OLD.remote_etag)) OR (OLD.state IN ('deleted','aborted') AND NEW.operation_id IS NOT NULL) OR ROW(NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.created_by,NEW.requested_audience,NEW.requested_epoch,NEW.membership_authority,NEW.creation_key,NEW.fingerprint,NEW.store_id,NEW.name,NEW.purpose,NEW.expected_bytes,NEW.sha256,NEW.part_bytes,NEW.created_at) IS DISTINCT FROM ROW(OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.created_by,OLD.requested_audience,OLD.requested_epoch,OLD.membership_authority,OLD.creation_key,OLD.fingerprint,OLD.store_id,OLD.name,OLD.purpose,OLD.expected_bytes,OLD.sha256,OLD.part_bytes,OLD.created_at) OR NEW.upload_expires_at>OLD.upload_expires_at OR NEW.expires_at>OLD.expires_at OR NEW.revision<OLD.revision OR NOT (NEW.state=OLD.state OR (OLD.state='creating' AND NEW.state IN ('uploading','aborting')) OR (OLD.state='uploading' AND NEW.state IN ('completing','aborting')) OR (OLD.state='completing' AND NEW.state IN ('ready','aborting')) OR (OLD.state='ready' AND NEW.state='deleting') OR (OLD.state='deleting' AND NEW.state IN ('deleted','aborting')) OR (OLD.state='aborting' AND NEW.state='aborted')) THEN RAISE EXCEPTION 'OBJECT_STORED_PROVENANCE_INVALID' USING ERRCODE='23514'; END IF;
 END IF;
 RETURN NEW;
END;
$$;

-- Install only after the matching candidate function/table.
CREATE TRIGGER m22_artifact_objects_guard BEFORE INSERT OR UPDATE ON artifact_objects FOR EACH ROW EXECUTE FUNCTION m22_artifact_objects_guard();

-- Inactive candidate; one complete PostgreSQL statement.
CREATE FUNCTION m22_artifact_object_parts_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.state<>'intent' OR NEW.etag IS NOT NULL OR NOT EXISTS(SELECT 1 FROM artifact_objects o WHERE o.tenant_id=NEW.tenant_id AND o.workspace_id=NEW.workspace_id AND o.id=NEW.object_id AND o.state='uploading' AND o.remote_upload_id IS NOT NULL AND o.operation_id IS NULL AND (NEW.number-1)*o.part_bytes<o.expected_bytes AND NEW.bytes=LEAST(o.part_bytes,o.expected_bytes-(NEW.number-1)*o.part_bytes)) THEN RAISE EXCEPTION 'OBJECT_STORED_PROVENANCE_INVALID' USING ERRCODE='23514'; END IF;
 ELSE
  IF ROW(NEW.tenant_id,NEW.workspace_id,NEW.object_id,NEW.number,NEW.bytes,NEW.sha256,NEW.stage_id) IS DISTINCT FROM ROW(OLD.tenant_id,OLD.workspace_id,OLD.object_id,OLD.number,OLD.bytes,OLD.sha256,OLD.stage_id) OR OLD.state='uploaded' AND (NEW.state<>'uploaded' OR NEW.etag IS DISTINCT FROM OLD.etag) THEN RAISE EXCEPTION 'OBJECT_STORED_PROVENANCE_INVALID' USING ERRCODE='23514'; END IF;
 END IF;
 RETURN NEW;
END;
$$;

-- Install only after the matching candidate function/table.
CREATE TRIGGER m22_artifact_object_parts_guard BEFORE INSERT OR UPDATE ON artifact_object_parts FOR EACH ROW EXECUTE FUNCTION m22_artifact_object_parts_guard();

-- Inactive candidate; one complete PostgreSQL statement.
CREATE FUNCTION m22_artifact_object_grants_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.revoked_at IS NOT NULL OR NOT EXISTS(SELECT 1 FROM artifact_objects o WHERE o.tenant_id=NEW.tenant_id AND o.workspace_id=NEW.workspace_id AND o.id=NEW.object_id AND o.state='ready' AND NEW.expires_at<=o.expires_at) THEN RAISE EXCEPTION 'OBJECT_STORED_PROVENANCE_INVALID' USING ERRCODE='23514'; END IF;
 ELSE
  IF ROW(NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.object_id,NEW.recipient_user_id,NEW.created_by,NEW.creator_epoch,NEW.creator_membership,NEW.recipient_epoch,NEW.recipient_membership,NEW.action,NEW.creation_key,NEW.fingerprint,NEW.not_before,NEW.expires_at,NEW.created_at) IS DISTINCT FROM ROW(OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.object_id,OLD.recipient_user_id,OLD.created_by,OLD.creator_epoch,OLD.creator_membership,OLD.recipient_epoch,OLD.recipient_membership,OLD.action,OLD.creation_key,OLD.fingerprint,OLD.not_before,OLD.expires_at,OLD.created_at) OR OLD.revoked_at IS NOT NULL AND NEW.revoked_at IS DISTINCT FROM OLD.revoked_at THEN RAISE EXCEPTION 'OBJECT_STORED_PROVENANCE_INVALID' USING ERRCODE='23514'; END IF;
 END IF;
 RETURN NEW;
END;
$$;

-- Install only after the matching candidate function/table.
CREATE TRIGGER m22_artifact_object_grants_guard BEFORE INSERT OR UPDATE ON artifact_object_grants FOR EACH ROW EXECUTE FUNCTION m22_artifact_object_grants_guard();

-- Inactive candidate; one complete PostgreSQL statement.
CREATE FUNCTION m22_artifact_object_ingestions_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.state<>'intent' OR NEW.model_import_id IS NOT NULL OR NEW.dataset_draft_id IS NOT NULL OR NEW.source_verified_sha256 IS NOT NULL OR NEW.source_verified_at IS NOT NULL OR NEW.receipt IS NOT NULL OR NEW.error_code IS NOT NULL THEN RAISE EXCEPTION 'OBJECT_STORED_PROVENANCE_INVALID' USING ERRCODE='23514'; END IF;
 ELSE
  IF ROW(NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.object_id,NEW.created_by,NEW.creation_key,NEW.fingerprint,NEW.kind,NEW.target_dataset_id,NEW.input,NEW.created_at) IS DISTINCT FROM ROW(OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.object_id,OLD.created_by,OLD.creation_key,OLD.fingerprint,OLD.kind,OLD.target_dataset_id,OLD.input,OLD.created_at) OR (OLD.model_import_id IS NOT NULL AND NEW.model_import_id IS DISTINCT FROM OLD.model_import_id) OR (OLD.dataset_draft_id IS NOT NULL AND NEW.dataset_draft_id IS DISTINCT FROM OLD.dataset_draft_id) OR (OLD.source_verified_sha256 IS NOT NULL AND (NEW.source_verified_sha256 IS DISTINCT FROM OLD.source_verified_sha256 OR NEW.source_verified_at IS DISTINCT FROM OLD.source_verified_at)) OR ((OLD.state IN ('submitted','failed')) AND NEW IS DISTINCT FROM OLD) OR NOT (NEW.state=OLD.state OR (OLD.state='intent' AND NEW.state IN ('running','failed')) OR (OLD.state='running' AND NEW.state IN ('submitted','failed'))) THEN RAISE EXCEPTION 'OBJECT_STORED_PROVENANCE_INVALID' USING ERRCODE='23514'; END IF;
 END IF;

  IF NEW.model_import_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM model_imports m JOIN artifact_objects o ON o.tenant_id=m.tenant_id AND o.workspace_id=m.workspace_id WHERE o.id=NEW.object_id AND m.id=NEW.model_import_id AND m.tenant_id=NEW.tenant_id AND m.workspace_id=NEW.workspace_id AND m.requested_by=NEW.created_by AND m.expected_bytes=o.expected_bytes) THEN RAISE EXCEPTION 'OBJECT_STORED_PROVENANCE_INVALID' USING ERRCODE='23514'; END IF;
  IF NEW.dataset_draft_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM dataset_drafts d JOIN artifact_objects o ON o.tenant_id=d.tenant_id AND o.workspace_id=d.workspace_id WHERE o.id=NEW.object_id AND d.id=NEW.dataset_draft_id AND d.tenant_id=NEW.tenant_id AND d.workspace_id=NEW.workspace_id AND d.dataset_id=NEW.target_dataset_id AND d.created_by=NEW.created_by AND d.expected_bytes=o.expected_bytes AND d.source_sha256=o.sha256) THEN RAISE EXCEPTION 'OBJECT_STORED_PROVENANCE_INVALID' USING ERRCODE='23514'; END IF;
  IF ((NEW.source_verified_sha256 IS NULL)<>(NEW.source_verified_at IS NULL)) OR (NEW.source_verified_sha256 IS NOT NULL AND NOT EXISTS(SELECT 1 FROM artifact_objects o WHERE o.tenant_id=NEW.tenant_id AND o.workspace_id=NEW.workspace_id AND o.id=NEW.object_id AND o.sha256=NEW.source_verified_sha256)) OR (NEW.state='submitted' AND (NEW.receipt IS NULL OR NEW.source_verified_sha256 IS NULL OR NEW.source_verified_at IS NULL)) THEN RAISE EXCEPTION 'OBJECT_STORED_PROVENANCE_INVALID' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END;
$$;

-- Install only after the matching candidate function/table.
CREATE TRIGGER m22_artifact_object_ingestions_guard BEFORE INSERT OR UPDATE ON artifact_object_ingestions FOR EACH ROW EXECUTE FUNCTION m22_artifact_object_ingestions_guard();

INSERT INTO schema_migrations(version,checksum) VALUES(25,'300d17abc81eb2a93254bb1bc2a5e89a4742ef33a53bb5ee7c62210ae477fd43');
-- Exact source: services/control/migrations/postgresql/000026_image_registry.up.sql
-- New account22-derived MVP lineage; immutable legacy assets are unchanged.
-- Active M24 PostgreSQL image registry schema.
CREATE TABLE image_registry_pins (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, workspace_id uuid NOT NULL,
 asset_id varchar(160) NOT NULL CHECK(length(asset_id)>0),
 source_archive_sha256 char(64) NOT NULL CHECK(source_archive_sha256~'^[a-f0-9]{64}$'),
 source_manifest_sha256 char(64) NOT NULL CHECK(source_manifest_sha256~'^[a-f0-9]{64}$'),
 backend_identity varchar(71) NOT NULL CHECK(backend_identity~'^sha256:[a-f0-9]{64}$'),
 state varchar(16) NOT NULL CHECK(state IN('importing','ready','unknown','deleting','deleted','failed')),
 manifest_digest varchar(71), config_digest varchar(71), archive_sha256 char(64), archive_bytes bigint,
 inventory jsonb, inventory_sha256 char(64),
 created_by uuid NOT NULL REFERENCES users(id), actor_epoch bigint NOT NULL CHECK(actor_epoch>=1),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(), updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9007199254740991),
 UNIQUE(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id),
 CHECK((inventory IS NULL)=(manifest_digest IS NULL)), CHECK((inventory IS NULL)=(config_digest IS NULL)),
 CHECK((inventory IS NULL)=(archive_sha256 IS NULL)), CHECK((inventory IS NULL)=(archive_bytes IS NULL)),
 CHECK((inventory IS NULL)=(inventory_sha256 IS NULL)),
 CHECK(manifest_digest IS NULL OR manifest_digest~'^sha256:[a-f0-9]{64}$'),
 CHECK(config_digest IS NULL OR config_digest~'^sha256:[a-f0-9]{64}$'),
 CHECK(archive_sha256 IS NULL OR archive_sha256~'^[a-f0-9]{64}$'),
 CHECK(inventory_sha256 IS NULL OR inventory_sha256~'^[a-f0-9]{64}$'),
 CHECK(archive_bytes IS NULL OR archive_bytes>0),
 CHECK(inventory IS NULL OR jsonb_typeof(inventory)='object'),
 CHECK(state<>'ready' OR inventory IS NOT NULL)
);
CREATE INDEX image_registry_scope ON image_registry_pins(tenant_id,workspace_id,created_at DESC,id DESC);
CREATE TABLE image_registry_blobs (
 tenant_id uuid NOT NULL, workspace_id uuid NOT NULL, pin_id uuid NOT NULL,
 ordinal integer NOT NULL CHECK(ordinal>=0), digest varchar(71) NOT NULL CHECK(digest~'^sha256:[a-f0-9]{64}$'),
 size bigint NOT NULL CHECK(size>0), media_type varchar(160) NOT NULL CHECK(length(media_type)>0), kind varchar(16) NOT NULL CHECK(kind IN('manifest','config','layer')),
 PRIMARY KEY(tenant_id,workspace_id,pin_id,ordinal),
 FOREIGN KEY(tenant_id,workspace_id,pin_id) REFERENCES image_registry_pins(tenant_id,workspace_id,id)
);
CREATE TABLE image_registry_imports (
 tenant_id uuid NOT NULL, workspace_id uuid NOT NULL, pin_id uuid NOT NULL,
 owner_token uuid NOT NULL, phase varchar(16) NOT NULL CHECK(phase IN('prepared','remote','completed','unknown')),
 PRIMARY KEY(tenant_id,workspace_id,pin_id),
 FOREIGN KEY(tenant_id,workspace_id,pin_id) REFERENCES image_registry_pins(tenant_id,workspace_id,id)
);
CREATE TABLE image_registry_deployment_refs (
 tenant_id uuid NOT NULL, workspace_id uuid NOT NULL, pin_id uuid NOT NULL,
 node_id uuid NOT NULL, deployment_id uuid NOT NULL, created_revision bigint NOT NULL CHECK(created_revision>=1),
 released_at timestamptz, created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,workspace_id,deployment_id),
 UNIQUE(tenant_id,workspace_id,pin_id,node_id,deployment_id),
 FOREIGN KEY(tenant_id,workspace_id,pin_id) REFERENCES image_registry_pins(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,node_id,deployment_id) REFERENCES deployments(tenant_id,workspace_id,node_id,id)
);
CREATE TABLE image_registry_grants (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, workspace_id uuid NOT NULL, pin_id uuid NOT NULL,
 node_id uuid NOT NULL, deployment_id uuid NOT NULL, task_id uuid NOT NULL, revision bigint NOT NULL CHECK(revision>=1),
 created_by uuid NOT NULL REFERENCES users(id), actor_epoch bigint NOT NULL CHECK(actor_epoch>=1),
 -- Immutable historical binding; no session FK may block existing retention.
 created_session_id uuid NOT NULL, created_token_hash bytea NOT NULL CHECK(octet_length(created_token_hash)=32),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(), expires_at timestamptz NOT NULL, revoked_at timestamptz,
 FOREIGN KEY(tenant_id,workspace_id,pin_id,node_id,deployment_id) REFERENCES image_registry_deployment_refs(tenant_id,workspace_id,pin_id,node_id,deployment_id),
 FOREIGN KEY(tenant_id,workspace_id,node_id,task_id) REFERENCES node_tasks(tenant_id,workspace_id,node_id,id),
 CHECK(expires_at>created_at)
);
CREATE UNIQUE INDEX image_registry_current_grant ON image_registry_grants(tenant_id,workspace_id,node_id,task_id) WHERE revoked_at IS NULL;
CREATE TABLE image_registry_offline_refs (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, workspace_id uuid NOT NULL, pin_id uuid NOT NULL,
 created_by uuid NOT NULL REFERENCES users(id), created_at timestamptz NOT NULL DEFAULT clock_timestamp(), released_at timestamptz,
 FOREIGN KEY(tenant_id,workspace_id,pin_id) REFERENCES image_registry_pins(tenant_id,workspace_id,id)
);
CREATE TABLE image_registry_mutations (
 tenant_id uuid NOT NULL, workspace_id uuid NOT NULL, actor_id uuid NOT NULL REFERENCES users(id),
 operation varchar(32) NOT NULL CHECK(operation IN('import','reconcile','remove','revoke','offline.hold','offline.release','reference.release','abandon','discard','grant')),
 idempotency_key uuid NOT NULL, fingerprint bytea NOT NULL CHECK(octet_length(fingerprint)=32),
 target_id uuid NOT NULL, created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,workspace_id,actor_id,operation,idempotency_key),
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id)
);
CREATE FUNCTION image_registry_pin_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.asset_id,OLD.source_archive_sha256,OLD.source_manifest_sha256,OLD.backend_identity,OLD.created_by,OLD.actor_epoch,OLD.created_at)
 IS DISTINCT FROM ROW(NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.asset_id,NEW.source_archive_sha256,NEW.source_manifest_sha256,NEW.backend_identity,NEW.created_by,NEW.actor_epoch,NEW.created_at)
 OR OLD.inventory IS NOT NULL AND ROW(OLD.manifest_digest,OLD.config_digest,OLD.archive_sha256,OLD.archive_bytes,OLD.inventory,OLD.inventory_sha256) IS DISTINCT FROM ROW(NEW.manifest_digest,NEW.config_digest,NEW.archive_sha256,NEW.archive_bytes,NEW.inventory,NEW.inventory_sha256)
 OR NEW.revision<>OLD.revision+1 OR OLD.state='deleted' THEN
 RAISE EXCEPTION 'REGISTRY_IMMUTABLE_IDENTITY'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER image_registry_pin_identity BEFORE UPDATE ON image_registry_pins FOR EACH ROW EXECUTE FUNCTION image_registry_pin_guard();
CREATE FUNCTION image_registry_grant_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.pin_id,OLD.node_id,OLD.deployment_id,OLD.task_id,OLD.revision,OLD.created_by,OLD.actor_epoch,OLD.created_session_id,OLD.created_token_hash,OLD.created_at,OLD.expires_at)
 IS DISTINCT FROM ROW(NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.pin_id,NEW.node_id,NEW.deployment_id,NEW.task_id,NEW.revision,NEW.created_by,NEW.actor_epoch,NEW.created_session_id,NEW.created_token_hash,NEW.created_at,NEW.expires_at)
 OR OLD.revoked_at IS NOT NULL AND OLD.revoked_at IS DISTINCT FROM NEW.revoked_at THEN RAISE EXCEPTION 'REGISTRY_IMMUTABLE_GRANT'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER image_registry_grant_identity BEFORE UPDATE ON image_registry_grants FOR EACH ROW EXECUTE FUNCTION image_registry_grant_guard();
CREATE FUNCTION image_registry_deployment_ref_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(OLD.tenant_id,OLD.workspace_id,OLD.pin_id,OLD.node_id,OLD.deployment_id,OLD.created_revision,OLD.created_at)
 IS DISTINCT FROM ROW(NEW.tenant_id,NEW.workspace_id,NEW.pin_id,NEW.node_id,NEW.deployment_id,NEW.created_revision,NEW.created_at)
 OR OLD.released_at IS NOT NULL AND OLD.released_at IS DISTINCT FROM NEW.released_at THEN RAISE EXCEPTION 'REGISTRY_IMMUTABLE_REFERENCE'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER image_registry_deployment_ref_identity BEFORE UPDATE ON image_registry_deployment_refs FOR EACH ROW EXECUTE FUNCTION image_registry_deployment_ref_guard();
CREATE FUNCTION image_registry_offline_ref_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.pin_id,OLD.created_by,OLD.created_at)
 IS DISTINCT FROM ROW(NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.pin_id,NEW.created_by,NEW.created_at)
 OR OLD.released_at IS NOT NULL AND OLD.released_at IS DISTINCT FROM NEW.released_at THEN RAISE EXCEPTION 'REGISTRY_IMMUTABLE_OFFLINE_REFERENCE'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER image_registry_offline_ref_identity BEFORE UPDATE ON image_registry_offline_refs FOR EACH ROW EXECUTE FUNCTION image_registry_offline_ref_guard();

INSERT INTO schema_migrations(version,checksum) VALUES(26,'05e3f965f43c22591f6ecafa425611e0ea8c7b4c65baa230cd21cdcf7804c364');
-- Exact source: services/control/migrations/postgresql/000027_task_events.up.sql
-- New account22-derived MVP lineage; immutable legacy assets are unchanged.
-- M25 transactional task event state and exact runtime guards.
-- Inactive source candidate. Parent assigns migration numbers and activation.
-- Requires the real node_tasks full scope key introduced by PostgreSQL M05.
CREATE TABLE task_event_outbox (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL,workspace_id uuid NOT NULL,node_id uuid NOT NULL,task_id uuid NOT NULL,
 namespace_id uuid NOT NULL,event_kind varchar(32) NOT NULL CHECK(event_kind='node.task.queued.v1'),operation varchar(64) NOT NULL,
 event_bytes bytea NOT NULL CHECK(octet_length(event_bytes) BETWEEN 1 AND 4096),logical_sha256 char(64) NOT NULL CHECK(logical_sha256~'^[a-f0-9]{64}$'),
 delivery_id uuid NOT NULL,generation integer NOT NULL CHECK(generation>=1),
 state varchar(16) NOT NULL CHECK(state IN('prepared','sent','published','received','obsolete','dead')),
 outcome_unknown boolean NOT NULL DEFAULT false,publish_attempts integer NOT NULL DEFAULT 0 CHECK(publish_attempts>=0),consume_attempts bigint NOT NULL DEFAULT 0 CHECK(consume_attempts>=0),
 claim_id uuid,claim_expires_at timestamptz,broker_identity char(64) NOT NULL CHECK(broker_identity~'^[a-f0-9]{64}$'),broker_sequence numeric(20,0),
 consumer_name varchar(64) NOT NULL,error_code varchar(64),not_before timestamptz NOT NULL,expires_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL,updated_at timestamptz NOT NULL,
 UNIQUE(tenant_id,workspace_id,node_id,task_id,id),UNIQUE(tenant_id,workspace_id,node_id,task_id,event_kind),UNIQUE(id,logical_sha256),UNIQUE(delivery_id),
 FOREIGN KEY(tenant_id,workspace_id,node_id,task_id) REFERENCES node_tasks(tenant_id,workspace_id,node_id,id),
 CONSTRAINT task_event_outbox_claim CHECK((claim_id IS NULL)=(claim_expires_at IS NULL)),
 CONSTRAINT task_event_outbox_unknown CHECK(NOT outcome_unknown OR state IN('sent','dead')),
 CONSTRAINT task_event_outbox_sequence CHECK(broker_sequence IS NULL OR (broker_sequence>=1 AND broker_sequence<=18446744073709551615 AND broker_identity IS NOT NULL)),
 CONSTRAINT task_event_outbox_ack CHECK(state NOT IN('published','received','obsolete') OR broker_sequence IS NOT NULL),
 CONSTRAINT task_event_outbox_time CHECK(expires_at>created_at AND updated_at>=created_at)
);
CREATE TABLE task_event_receipts (
 id uuid PRIMARY KEY,event_id uuid NOT NULL UNIQUE,tenant_id uuid NOT NULL,workspace_id uuid NOT NULL,node_id uuid NOT NULL,task_id uuid NOT NULL,
 logical_sha256 char(64) NOT NULL CHECK(logical_sha256~'^[a-f0-9]{64}$'),delivery_id uuid NOT NULL,generation integer NOT NULL CHECK(generation>=1),
 consumer_name varchar(64) NOT NULL,broker_identity char(64) NOT NULL CHECK(broker_identity~'^[a-f0-9]{64}$'),
 broker_sequence numeric(20,0) NOT NULL CHECK(broker_sequence BETWEEN 1 AND 18446744073709551615),consumer_sequence numeric(20,0) NOT NULL CHECK(consumer_sequence BETWEEN 1 AND 18446744073709551615),
 outcome varchar(16) NOT NULL CHECK(outcome IN('received','obsolete')),task_state varchar(16) NOT NULL CHECK(task_state IN('pending','running','succeeded','failed','cancelled')),
 created_at timestamptz NOT NULL,
 FOREIGN KEY(tenant_id,workspace_id,node_id,task_id,event_id) REFERENCES task_event_outbox(tenant_id,workspace_id,node_id,task_id,id),
 FOREIGN KEY(event_id,logical_sha256) REFERENCES task_event_outbox(id,logical_sha256)
);
CREATE TABLE task_event_dead_letters (
 id uuid PRIMARY KEY,event_id uuid,tenant_id uuid NOT NULL,workspace_id uuid,node_id uuid,task_id uuid,generation integer,delivery_id uuid,
 kind varchar(16) NOT NULL CHECK(kind IN('publish','consume','expiry','poison')),outcome_unknown boolean NOT NULL,
 broker_identity char(64) NOT NULL CHECK(broker_identity~'^[a-f0-9]{64}$'),broker_sequence numeric(20,0),consumer_name varchar(64) NOT NULL,
 envelope_sha256 char(64) NOT NULL CHECK(envelope_sha256~'^[a-f0-9]{64}$'),error_code varchar(64) NOT NULL,
 retry_actor uuid REFERENCES users(id),retry_key uuid,retry_reason varchar(500),retry_fingerprint bytea CHECK(retry_fingerprint IS NULL OR octet_length(retry_fingerprint)=32),retry_delivery_id uuid,retry_generation integer,retried_at timestamptz,
 created_at timestamptz NOT NULL,
 UNIQUE(event_id,generation),UNIQUE(delivery_id),UNIQUE(broker_identity,broker_sequence,consumer_name),UNIQUE(retry_actor,retry_key),
 FOREIGN KEY(tenant_id,workspace_id,node_id,task_id,event_id) REFERENCES task_event_outbox(tenant_id,workspace_id,node_id,task_id,id),
 CONSTRAINT task_event_dead_letters_scope CHECK((event_id IS NOT NULL AND workspace_id IS NOT NULL AND node_id IS NOT NULL AND task_id IS NOT NULL AND generation IS NOT NULL AND generation>=1 AND delivery_id IS NOT NULL) OR (event_id IS NULL AND workspace_id IS NULL AND node_id IS NULL AND task_id IS NULL AND generation IS NULL AND delivery_id IS NULL AND kind='poison')),
 CONSTRAINT task_event_dead_letters_sequence CHECK(broker_sequence IS NULL OR broker_sequence BETWEEN 1 AND 18446744073709551615),
 CONSTRAINT task_event_dead_letters_retry CHECK((retry_actor IS NULL AND retry_key IS NULL AND retry_reason IS NULL AND retry_fingerprint IS NULL AND retry_delivery_id IS NULL AND retry_generation IS NULL AND retried_at IS NULL) OR (event_id IS NOT NULL AND retry_actor IS NOT NULL AND retry_key IS NOT NULL AND retry_reason IS NOT NULL AND retry_fingerprint IS NOT NULL AND retry_delivery_id IS NOT NULL AND retry_generation IS NOT NULL AND retry_generation>generation AND retried_at IS NOT NULL AND retried_at>=created_at))
);
CREATE INDEX task_event_outbox_due ON task_event_outbox(state,not_before,created_at,id);
CREATE INDEX task_event_outbox_scope ON task_event_outbox(tenant_id,workspace_id,node_id,created_at DESC,id DESC);
CREATE INDEX task_event_dead_letters_scope ON task_event_dead_letters(tenant_id,workspace_id,node_id,created_at DESC,id DESC);

ALTER TABLE task_event_outbox ADD CONSTRAINT task_event_outbox_error CHECK(error_code IS NULL OR error_code IN('TASK_BUS_READBACK_CONFLICT','TASK_BUS_IDENTITY_CHANGED','TASK_BUS_PROFILE_REJECTED','TASK_BUS_PUBLISH_UNKNOWN','TASK_EVENT_SOURCE_OBSOLETE','TASK_EVENT_SOURCE_CHANGED','TASK_BUS_PUBLISH_EXHAUSTED','TASK_BUS_CONSUME_EXHAUSTED','TRANSPORT_RETENTION_EXPIRED','TASK_EVENT_ENVELOPE_REJECTED','TASK_EVENT_UNCOMMITTED_SOURCE','TASK_EVENT_STALE_DELIVERY','TASK_BUS_RESTORE_FENCED'));
ALTER TABLE task_event_dead_letters ADD CONSTRAINT task_event_dead_letters_error CHECK(error_code IN('TASK_BUS_READBACK_CONFLICT','TASK_BUS_IDENTITY_CHANGED','TASK_BUS_PROFILE_REJECTED','TASK_BUS_PUBLISH_UNKNOWN','TASK_EVENT_SOURCE_OBSOLETE','TASK_EVENT_SOURCE_CHANGED','TASK_BUS_PUBLISH_EXHAUSTED','TASK_BUS_CONSUME_EXHAUSTED','TRANSPORT_RETENTION_EXPIRED','TASK_EVENT_ENVELOPE_REJECTED','TASK_EVENT_UNCOMMITTED_SOURCE','TASK_EVENT_STALE_DELIVERY','TASK_BUS_RESTORE_FENCED'));
ALTER TABLE task_event_outbox ADD CONSTRAINT task_event_outbox_sent CHECK(state<>'sent' OR outcome_unknown);
ALTER TABLE task_event_outbox ADD CONSTRAINT task_event_outbox_prepared CHECK(state<>'prepared' OR (NOT outcome_unknown AND publish_attempts=0 AND consume_attempts=0 AND broker_sequence IS NULL AND claim_id IS NULL AND error_code IS NULL));
-- Inactive PostgreSQL source guards; apply after the three real candidate tables.
CREATE FUNCTION task_event_outbox_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE source_time timestamptz; source_operation text; j jsonb;
BEGIN
 IF TG_OP='DELETE' THEN
  IF OLD.outcome_unknown OR OLD.claim_id IS NOT NULL OR OLD.state NOT IN('received','obsolete','dead') OR OLD.expires_at>clock_timestamp() OR NOT EXISTS(SELECT 1 FROM node_tasks t WHERE (t.tenant_id,t.workspace_id,t.node_id,t.id)=(OLD.tenant_id,OLD.workspace_id,OLD.node_id,OLD.task_id) AND t.state IN('succeeded','failed','cancelled')) THEN
   RAISE EXCEPTION 'TASK_EVENT_RETENTION_REFUSED';
  END IF;
  RETURN OLD;
 END IF;
 IF TG_OP='INSERT' THEN
  SELECT t.created_at,t.operation INTO source_time,source_operation FROM node_tasks t JOIN managed_nodes n ON (n.tenant_id,n.workspace_id,n.id)=(t.tenant_id,t.workspace_id,t.node_id) JOIN workspaces w ON (w.tenant_id,w.id)=(n.tenant_id,n.workspace_id) WHERE (t.tenant_id,t.workspace_id,t.node_id,t.id)=(NEW.tenant_id,NEW.workspace_id,NEW.node_id,NEW.task_id) AND t.state='pending' AND t.attempt=0 AND n.status='active' AND w.status='active';
  IF NOT FOUND OR source_operation<>NEW.operation OR NEW.generation<>1 OR NEW.state<>'prepared' OR NEW.outcome_unknown OR NEW.publish_attempts<>0 OR NEW.consume_attempts<>0 OR NEW.claim_id IS NOT NULL OR NEW.broker_sequence IS NOT NULL OR NEW.error_code IS NOT NULL OR NEW.not_before<>NEW.created_at OR NEW.updated_at<>NEW.created_at OR NEW.created_at<source_time OR encode(sha256(NEW.event_bytes),'hex')<>NEW.logical_sha256 THEN RAISE EXCEPTION 'TASK_EVENT_SOURCE_INVALID'; END IF;
  j:=convert_from(NEW.event_bytes,'UTF8')::jsonb;
  IF jsonb_typeof(j)<>'object' OR (SELECT count(*) FROM jsonb_object_keys(j))<>11 OR jsonb_typeof(j->'schema') IS DISTINCT FROM 'number' OR j->>'schema' IS DISTINCT FROM '1' OR j->>'event_id' IS DISTINCT FROM NEW.id::text OR j->>'namespace_id' IS DISTINCT FROM NEW.namespace_id::text OR j->>'tenant_id' IS DISTINCT FROM NEW.tenant_id::text OR j->>'workspace_id' IS DISTINCT FROM NEW.workspace_id::text OR j->>'node_id' IS DISTINCT FROM NEW.node_id::text OR j->>'task_id' IS DISTINCT FROM NEW.task_id::text OR j->>'kind' IS DISTINCT FROM NEW.event_kind OR j->>'operation' IS DISTINCT FROM NEW.operation OR (j->>'created_at')::timestamptz IS DISTINCT FROM NEW.created_at OR (j->>'task_created_at')::timestamptz IS DISTINCT FROM source_time THEN RAISE EXCEPTION 'TASK_EVENT_SOURCE_INVALID'; END IF;
  RETURN NEW;
 END IF;
 IF (NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.node_id,NEW.task_id,NEW.namespace_id,NEW.event_kind,NEW.operation,NEW.event_bytes,NEW.logical_sha256,NEW.created_at,NEW.expires_at,NEW.broker_identity,NEW.consumer_name) IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.node_id,OLD.task_id,OLD.namespace_id,OLD.event_kind,OLD.operation,OLD.event_bytes,OLD.logical_sha256,OLD.created_at,OLD.expires_at,OLD.broker_identity,OLD.consumer_name) OR NEW.updated_at<OLD.updated_at OR OLD.state IN('received','obsolete') THEN RAISE EXCEPTION 'TASK_EVENT_IMMUTABLE'; END IF;
 IF NEW.generation<>OLD.generation THEN
  IF NEW.generation<>OLD.generation+1 OR NEW.delivery_id=OLD.delivery_id OR OLD.state<>'dead' OR NEW.state<>'prepared' OR NEW.outcome_unknown OR NEW.publish_attempts<>0 OR NEW.consume_attempts<>0 OR NEW.broker_sequence IS NOT NULL OR NEW.claim_id IS NOT NULL OR NEW.error_code IS NOT NULL OR NEW.not_before<>NEW.updated_at OR NOT EXISTS(SELECT 1 FROM task_event_dead_letters d WHERE d.event_id=OLD.id AND d.generation=OLD.generation AND d.retry_generation=NEW.generation AND d.retry_delivery_id=NEW.delivery_id AND d.retried_at=NEW.updated_at) THEN RAISE EXCEPTION 'TASK_EVENT_RETRY_INVALID'; END IF;
 ELSE
  IF NEW.delivery_id<>OLD.delivery_id OR NEW.publish_attempts<OLD.publish_attempts OR NEW.consume_attempts<OLD.consume_attempts OR NOT ((OLD.state='prepared' AND NEW.state IN('prepared','sent','dead')) OR (OLD.state='sent' AND NEW.state IN('sent','published','received','obsolete','dead')) OR (OLD.state='published' AND NEW.state IN('published','received','obsolete','dead')) OR (OLD.state='dead' AND NEW.state IN('dead','received','obsolete'))) THEN RAISE EXCEPTION 'TASK_EVENT_TRANSITION_INVALID'; END IF;
 END IF;
 IF NEW.state IN('received','obsolete') AND NOT EXISTS(SELECT 1 FROM task_event_receipts r WHERE r.event_id=NEW.id AND r.logical_sha256=NEW.logical_sha256 AND r.delivery_id=NEW.delivery_id AND r.generation=NEW.generation AND r.broker_identity=NEW.broker_identity AND r.consumer_name=NEW.consumer_name AND r.broker_sequence=NEW.broker_sequence AND r.outcome=NEW.state) THEN RAISE EXCEPTION 'TASK_EVENT_RECEIPT_INVALID'; END IF;
 IF NEW.state='dead' AND NOT EXISTS(SELECT 1 FROM task_event_dead_letters d WHERE d.event_id=NEW.id AND d.generation=NEW.generation AND d.error_code=NEW.error_code) THEN RAISE EXCEPTION 'TASK_EVENT_DLQ_INVALID'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER task_event_outbox_guard BEFORE INSERT OR UPDATE OR DELETE ON task_event_outbox FOR EACH ROW EXECUTE FUNCTION task_event_outbox_guard();

CREATE FUNCTION task_event_receipt_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE source task_event_outbox;
BEGIN
 IF TG_OP='UPDATE' THEN RAISE EXCEPTION 'TASK_EVENT_RECEIPT_IMMUTABLE'; END IF;
 IF TG_OP='DELETE' THEN
  IF NOT EXISTS(SELECT 1 FROM task_event_outbox o JOIN node_tasks t ON (t.tenant_id,t.workspace_id,t.node_id,t.id)=(o.tenant_id,o.workspace_id,o.node_id,o.task_id) WHERE o.id=OLD.event_id AND NOT o.outcome_unknown AND o.claim_id IS NULL AND o.state IN('received','obsolete','dead') AND o.expires_at<=clock_timestamp() AND t.state IN('succeeded','failed','cancelled')) THEN RAISE EXCEPTION 'TASK_EVENT_RETENTION_REFUSED'; END IF;
  RETURN OLD;
 END IF;
 SELECT * INTO source FROM task_event_outbox WHERE id=NEW.event_id FOR SHARE;
 IF NOT FOUND OR (source.tenant_id,source.workspace_id,source.node_id,source.task_id,source.logical_sha256,source.delivery_id,source.generation,source.broker_identity,source.consumer_name) IS DISTINCT FROM (NEW.tenant_id,NEW.workspace_id,NEW.node_id,NEW.task_id,NEW.logical_sha256,NEW.delivery_id,NEW.generation,NEW.broker_identity,NEW.consumer_name) OR source.state NOT IN('sent','published','dead') OR NEW.created_at<source.created_at OR NOT EXISTS(SELECT 1 FROM node_tasks t WHERE (t.tenant_id,t.workspace_id,t.node_id,t.id)=(NEW.tenant_id,NEW.workspace_id,NEW.node_id,NEW.task_id) AND t.state=NEW.task_state) THEN RAISE EXCEPTION 'TASK_EVENT_RECEIPT_INVALID'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER task_event_receipt_guard BEFORE INSERT OR UPDATE OR DELETE ON task_event_receipts FOR EACH ROW EXECUTE FUNCTION task_event_receipt_guard();

CREATE FUNCTION task_event_dead_letter_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE source task_event_outbox;
BEGIN
 IF TG_OP='DELETE' THEN
  IF OLD.event_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM task_event_outbox o JOIN node_tasks t ON (t.tenant_id,t.workspace_id,t.node_id,t.id)=(o.tenant_id,o.workspace_id,o.node_id,o.task_id) WHERE o.id=OLD.event_id AND NOT o.outcome_unknown AND o.claim_id IS NULL AND o.state IN('received','obsolete','dead') AND o.expires_at<=clock_timestamp() AND t.state IN('succeeded','failed','cancelled')) THEN RAISE EXCEPTION 'TASK_EVENT_RETENTION_REFUSED'; END IF;
  RETURN OLD;
 END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.retry_key IS NOT NULL THEN RAISE EXCEPTION 'TASK_EVENT_RETRY_INVALID'; END IF;
  IF NEW.event_id IS NOT NULL THEN
   SELECT * INTO source FROM task_event_outbox WHERE id=NEW.event_id FOR SHARE;
   IF NOT FOUND OR (source.tenant_id,source.workspace_id,source.node_id,source.task_id,source.generation,source.delivery_id,source.broker_identity,source.consumer_name) IS DISTINCT FROM (NEW.tenant_id,NEW.workspace_id,NEW.node_id,NEW.task_id,NEW.generation,NEW.delivery_id,NEW.broker_identity,NEW.consumer_name) OR source.state IN('received','obsolete') OR NEW.created_at<source.created_at OR NEW.envelope_sha256<>encode(sha256(convert_to('{"delivery_id":"'||source.delivery_id::text||'","event":','UTF8')||source.event_bytes||convert_to('}','UTF8')),'hex') THEN RAISE EXCEPTION 'TASK_EVENT_DLQ_INVALID'; END IF;
  ELSIF NEW.kind<>'poison' OR NEW.broker_sequence IS NULL OR NEW.outcome_unknown THEN RAISE EXCEPTION 'TASK_EVENT_DLQ_INVALID'; END IF;
  RETURN NEW;
 END IF;
 IF (NEW.id,NEW.event_id,NEW.tenant_id,NEW.workspace_id,NEW.node_id,NEW.task_id,NEW.generation,NEW.delivery_id,NEW.kind,NEW.outcome_unknown,NEW.broker_identity,NEW.broker_sequence,NEW.consumer_name,NEW.envelope_sha256,NEW.error_code,NEW.created_at) IS DISTINCT FROM (OLD.id,OLD.event_id,OLD.tenant_id,OLD.workspace_id,OLD.node_id,OLD.task_id,OLD.generation,OLD.delivery_id,OLD.kind,OLD.outcome_unknown,OLD.broker_identity,OLD.broker_sequence,OLD.consumer_name,OLD.envelope_sha256,OLD.error_code,OLD.created_at) OR OLD.retry_key IS NOT NULL OR NEW.retry_key IS NULL OR NEW.retry_generation<>OLD.generation+1 OR NOT EXISTS(SELECT 1 FROM users u WHERE u.id=NEW.retry_actor AND u.status='active' AND u.is_platform_admin) OR NOT EXISTS(SELECT 1 FROM task_event_outbox o WHERE o.id=OLD.event_id AND o.generation=OLD.generation AND o.state='dead' AND o.expires_at>NEW.retried_at) THEN RAISE EXCEPTION 'TASK_EVENT_RETRY_INVALID'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER task_event_dead_letter_guard BEFORE INSERT OR UPDATE OR DELETE ON task_event_dead_letters FOR EACH ROW EXECUTE FUNCTION task_event_dead_letter_guard();

INSERT INTO schema_migrations(version,checksum) VALUES(27,'5884a82776a568457ec1508e2a23bf9c6ef7f81477dcde3f6c2a2fcf3f54b3a3');
-- Exact source: services/control/migrations/postgresql/000028_middleware.up.sql
-- New account22-derived MVP lineage; immutable legacy assets are unchanged.
-- M26 fixed PostgreSQL maintenance instances, immutable intents and history.
CREATE TABLE middleware_instances (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 profile_key varchar(63) NOT NULL CHECK(profile_key ~ '^[a-z][a-z0-9_]{0,62}$'),
 kind varchar(24) NOT NULL CHECK(kind IN('platform_owned','customer_external')),
 name varchar(100) NOT NULL CHECK(length(name)>0),
 version bigint NOT NULL DEFAULT 1 CHECK(version BETWEEN 1 AND 9007199254740991),
 state varchar(16) NOT NULL DEFAULT 'active' CHECK(state IN('active','archived')),
 profile_fingerprint char(64) NOT NULL CHECK(profile_fingerprint ~ '^[a-f0-9]{64}$'),
 created_by uuid NOT NULL REFERENCES users(id),
 creation_key uuid NOT NULL,
 creation_fingerprint char(64) NOT NULL CHECK(creation_fingerprint ~ '^[a-f0-9]{64}$'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(tenant_id,workspace_id,id),
 UNIQUE(tenant_id,workspace_id,profile_key),
 UNIQUE(tenant_id,workspace_id,created_by,creation_key),
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id)
);
CREATE TABLE middleware_operations (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 instance_id uuid NOT NULL,
 kind varchar(24) NOT NULL CHECK(kind IN('health','backup','isolated_restore','reconcile')),
 state varchar(16) NOT NULL DEFAULT 'queued' CHECK(state IN('queued','running','succeeded','failed','unknown')),
 requested_by uuid NOT NULL REFERENCES users(id),
 intent_key uuid NOT NULL,
 intent_fingerprint char(64) NOT NULL CHECK(intent_fingerprint ~ '^[a-f0-9]{64}$'),
 input_json text NOT NULL CHECK(octet_length(input_json)<=65536 AND jsonb_typeof(input_json::jsonb)='object'),
 actor_json text NOT NULL CHECK(octet_length(actor_json)<=65536 AND jsonb_typeof(actor_json::jsonb)='object'),
 instance_version bigint NOT NULL CHECK(instance_version BETWEEN 1 AND 9007199254740991),
 profile_fingerprint char(64) NOT NULL CHECK(profile_fingerprint ~ '^[a-f0-9]{64}$'),
 request_id uuid NOT NULL,
 attempt_id uuid,
 lease_expires_at timestamptz,
 result_json text CHECK(result_json IS NULL OR (octet_length(result_json)<=16777216 AND jsonb_typeof(result_json::jsonb)='object')),
 result_sha256 char(64) CHECK(result_sha256 IS NULL OR result_sha256 ~ '^[a-f0-9]{64}$'),
 error_code varchar(96),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 completed_at timestamptz,
 UNIQUE(tenant_id,workspace_id,id),
 UNIQUE(tenant_id,workspace_id,requested_by,intent_key),
 FOREIGN KEY(tenant_id,workspace_id,instance_id) REFERENCES middleware_instances(tenant_id,workspace_id,id),
 CHECK((state='running' AND attempt_id IS NOT NULL AND lease_expires_at IS NOT NULL) OR (state<>'running' AND lease_expires_at IS NULL)),
 CHECK((state='succeeded' AND result_json IS NOT NULL AND result_sha256 IS NOT NULL AND error_code IS NULL AND completed_at IS NOT NULL) OR (state<>'succeeded' AND result_json IS NULL AND result_sha256 IS NULL)),
 CHECK((state IN('failed','unknown') AND error_code IS NOT NULL AND completed_at IS NOT NULL) OR (state NOT IN('failed','unknown') AND error_code IS NULL)),
 CHECK(state<>'queued' OR (attempt_id IS NULL AND completed_at IS NULL))
);
CREATE INDEX middleware_operations_claim ON middleware_operations(state,created_at,id);
CREATE INDEX middleware_operations_expiry ON middleware_operations(state,lease_expires_at,id);
CREATE INDEX middleware_operations_history ON middleware_operations(tenant_id,workspace_id,instance_id,id);
CREATE FUNCTION middleware_operation_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'MIDDLEWARE_HISTORY_IMMUTABLE'; END IF;
 IF ROW(NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.instance_id,NEW.kind,NEW.requested_by,NEW.intent_key,NEW.intent_fingerprint,NEW.input_json,NEW.actor_json,NEW.instance_version,NEW.profile_fingerprint,NEW.request_id,NEW.created_at) IS DISTINCT FROM ROW(OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.instance_id,OLD.kind,OLD.requested_by,OLD.intent_key,OLD.intent_fingerprint,OLD.input_json,OLD.actor_json,OLD.instance_version,OLD.profile_fingerprint,OLD.request_id,OLD.created_at) THEN RAISE EXCEPTION 'MIDDLEWARE_INTENT_IMMUTABLE'; END IF;
 IF OLD.state IN('succeeded','failed','unknown') AND NEW IS DISTINCT FROM OLD THEN RAISE EXCEPTION 'MIDDLEWARE_TERMINAL_IMMUTABLE'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER middleware_operation_update BEFORE UPDATE OR DELETE ON middleware_operations FOR EACH ROW EXECUTE FUNCTION middleware_operation_immutable();

INSERT INTO schema_migrations(version,checksum) VALUES(28,'c19a744462bfdbdf4ee0d54237a77d51efda10d9ed3a22d8e05a95fc5d68ddc1');
-- Exact source: services/control/migrations/postgresql/000029_private_cluster.up.sql
-- New account22-derived MVP lineage; immutable legacy assets are unchanged.
-- M28 initial private-cluster ledger; remote work is never released by age.
CREATE TABLE private_cluster_connections (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, workspace_id uuid NOT NULL,
 profile_id varchar(63) NOT NULL, cluster_uid uuid NOT NULL, namespace_uid uuid NOT NULL,
 namespace_name varchar(63) NOT NULL, installation_id uuid NOT NULL, profile varchar(64) NOT NULL,
 config_sha256 char(64) NOT NULL, credential_sha256 char(64) NOT NULL, manifest_sha256 char(64) NOT NULL,
 image_digest varchar(71) NOT NULL, config_digest varchar(71) NOT NULL,
 state varchar(16) NOT NULL CHECK(state IN('active','disabled','restore_fenced')),
 revision bigint NOT NULL CHECK(revision BETWEEN 1 AND 9007199254740991), active_run_id uuid,
 observation jsonb, created_by uuid NOT NULL REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(), updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(tenant_id,workspace_id,id), UNIQUE(cluster_uid,namespace_uid), UNIQUE(tenant_id,workspace_id,profile_id),
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id),
 CHECK(cluster_uid<>namespace_uid), CHECK(namespace_name LIKE 'projectone-test-%'),
 CHECK(profile IN('kubernetes1.36.4-linux-amd64','local-kind1.36.4-linux-arm64')),
 CHECK(config_sha256~'^[a-f0-9]{64}$' AND credential_sha256~'^[a-f0-9]{64}$' AND manifest_sha256~'^[a-f0-9]{64}$'),
 CHECK(image_digest~'^sha256:[a-f0-9]{64}$' AND config_digest~'^sha256:[a-f0-9]{64}$'),
 CHECK(observation IS NULL OR jsonb_typeof(observation)='object')
);
CREATE TABLE private_cluster_runs (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, workspace_id uuid NOT NULL, connection_id uuid NOT NULL,
 connection_revision bigint NOT NULL CHECK(connection_revision BETWEEN 1 AND 9007199254740991),
 created_by uuid NOT NULL REFERENCES users(id), actor_epoch bigint NOT NULL CHECK(actor_epoch>=1),
 membership_authority uuid, created_session_id uuid NOT NULL, created_token_hash bytea NOT NULL CHECK(octet_length(created_token_hash)=32),
 config_sha256 char(64) NOT NULL CHECK(config_sha256~'^[a-f0-9]{64}$'),
 request jsonb NOT NULL CHECK(jsonb_typeof(request)='object'), spec_sha256 char(64) NOT NULL CHECK(spec_sha256~'^[a-f0-9]{64}$'),
 phase varchar(24) NOT NULL CHECK(phase IN('prepared','lease_pending','lease_acquired','job_pending','observing','job_delete_pending','job_absent','lease_delete_pending','cleaned')),
 result varchar(16) CHECK(result IN('succeeded','failed','cancelled')),
 lease_uid uuid, job_uid uuid, pod_uid uuid, remote_state jsonb,
 revision bigint NOT NULL CHECK(revision BETWEEN 1 AND 9007199254740991),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(), updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(tenant_id,workspace_id,connection_id,id), UNIQUE(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,connection_id) REFERENCES private_cluster_connections(tenant_id,workspace_id,id),
 CHECK(job_uid IS NULL OR lease_uid IS NOT NULL), CHECK(pod_uid IS NULL OR job_uid IS NOT NULL),
 CHECK(phase NOT IN('lease_acquired','job_pending','observing','job_delete_pending','job_absent','lease_delete_pending') OR lease_uid IS NOT NULL),
 CHECK(phase NOT IN('observing','job_delete_pending') OR job_uid IS NOT NULL),
 CHECK(phase<>'cleaned' OR result IS NOT NULL),
 CHECK(remote_state IS NULL OR jsonb_typeof(remote_state)='object')
);
ALTER TABLE private_cluster_connections ADD CONSTRAINT private_cluster_active_run FOREIGN KEY(tenant_id,workspace_id,id,active_run_id) REFERENCES private_cluster_runs(tenant_id,workspace_id,connection_id,id);
CREATE INDEX private_cluster_history ON private_cluster_runs(tenant_id,workspace_id,created_at DESC,id DESC);
CREATE TABLE private_cluster_intents (
 tenant_id uuid NOT NULL, workspace_id uuid NOT NULL, actor_id uuid NOT NULL REFERENCES users(id),
 idempotency_key uuid NOT NULL, operation varchar(24) NOT NULL CHECK(operation IN('bind','rebind','disable','observe','start','continue','reconcile','cancel','cleanup')),
 fingerprint char(64) NOT NULL CHECK(fingerprint~'^[a-f0-9]{64}$'), target_id uuid NOT NULL,
 result_revision bigint NOT NULL CHECK(result_revision BETWEEN 1 AND 9007199254740991),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,workspace_id,actor_id,idempotency_key),
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id)
);
CREATE FUNCTION private_cluster_connection_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.profile_id,OLD.cluster_uid,OLD.namespace_uid,OLD.namespace_name,OLD.installation_id,OLD.profile,OLD.created_by,OLD.created_at)
 IS DISTINCT FROM ROW(NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.profile_id,NEW.cluster_uid,NEW.namespace_uid,NEW.namespace_name,NEW.installation_id,NEW.profile,NEW.created_by,NEW.created_at)
 OR NEW.revision<>OLD.revision+1 OR OLD.active_run_id IS NOT NULL AND ROW(OLD.config_sha256,OLD.manifest_sha256,OLD.image_digest,OLD.config_digest) IS DISTINCT FROM ROW(NEW.config_sha256,NEW.manifest_sha256,NEW.image_digest,NEW.config_digest)
 THEN RAISE EXCEPTION 'PRIVATE_CLUSTER_IMMUTABLE_CONNECTION'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER private_cluster_connection_identity BEFORE UPDATE ON private_cluster_connections FOR EACH ROW EXECUTE FUNCTION private_cluster_connection_guard();
CREATE FUNCTION private_cluster_run_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.connection_id,OLD.connection_revision,OLD.created_by,OLD.actor_epoch,OLD.membership_authority,OLD.created_session_id,OLD.created_token_hash,OLD.config_sha256,OLD.request,OLD.spec_sha256,OLD.created_at)
 IS DISTINCT FROM ROW(NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.connection_id,NEW.connection_revision,NEW.created_by,NEW.actor_epoch,NEW.membership_authority,NEW.created_session_id,NEW.created_token_hash,NEW.config_sha256,NEW.request,NEW.spec_sha256,NEW.created_at)
 OR NEW.revision<>OLD.revision+1 OR OLD.phase='cleaned' OR NOT (OLD.phase=NEW.phase OR OLD.phase='prepared' AND NEW.phase IN('lease_pending','cleaned') OR OLD.phase='lease_pending' AND NEW.phase IN('prepared','lease_acquired') OR OLD.phase='lease_acquired' AND NEW.phase IN('job_pending','job_absent') OR OLD.phase='job_pending' AND NEW.phase IN('lease_acquired','observing') OR OLD.phase='observing' AND NEW.phase='job_delete_pending' OR OLD.phase='job_delete_pending' AND NEW.phase IN('observing','job_absent') OR OLD.phase='job_absent' AND NEW.phase='lease_delete_pending' OR OLD.phase='lease_delete_pending' AND NEW.phase IN('job_absent','cleaned'))
 OR OLD.result IS NOT NULL AND OLD.result IS DISTINCT FROM NEW.result
 OR OLD.lease_uid IS NOT NULL AND OLD.lease_uid IS DISTINCT FROM NEW.lease_uid
 OR OLD.job_uid IS NOT NULL AND OLD.job_uid IS DISTINCT FROM NEW.job_uid
 OR OLD.pod_uid IS NOT NULL AND OLD.pod_uid IS DISTINCT FROM NEW.pod_uid
 THEN RAISE EXCEPTION 'PRIVATE_CLUSTER_IMMUTABLE_RUN'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER private_cluster_run_identity BEFORE UPDATE ON private_cluster_runs FOR EACH ROW EXECUTE FUNCTION private_cluster_run_guard();
CREATE FUNCTION private_cluster_intent_guard() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'PRIVATE_CLUSTER_IMMUTABLE_INTENT'; END $$;
CREATE TRIGGER private_cluster_intent_identity BEFORE UPDATE OR DELETE ON private_cluster_intents FOR EACH ROW EXECUTE FUNCTION private_cluster_intent_guard();
CREATE FUNCTION private_cluster_history_guard() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'PRIVATE_CLUSTER_IMMUTABLE_HISTORY'; END $$;
CREATE TRIGGER private_cluster_connection_delete BEFORE DELETE ON private_cluster_connections FOR EACH ROW EXECUTE FUNCTION private_cluster_history_guard();
CREATE TRIGGER private_cluster_run_delete BEFORE DELETE ON private_cluster_runs FOR EACH ROW EXECUTE FUNCTION private_cluster_history_guard();

INSERT INTO schema_migrations(version,checksum) VALUES(29,'27313006716fbffba85f5baa4ea964ffcf6fa1b30d796fed93ba78a097ed363b');
-- Exact source: services/control/migrations/postgresql/000030_cell_scope.up.sql
-- Every installation has a site and a cell (ADR 0006). Tables that belong to the global
-- layer (identity, tenancy, licensing) carry neither column; the list is enforced by
-- TestEveryCellOwnedTableCarriesScope. Rows default to the cell this database serves, so
-- the catalog is the same in every cell database and a cell's identity is one row of data.
CREATE TABLE sites (
 id varchar(32) PRIMARY KEY CHECK(id ~ '^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$'),
 display_name varchar(128) NOT NULL CHECK(length(display_name) BETWEEN 1 AND 128),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE cells (
 id varchar(32) PRIMARY KEY CHECK(id ~ '^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$'),
 site_id varchar(32) NOT NULL REFERENCES sites(id),
 display_name varchar(128) NOT NULL CHECK(length(display_name) BETWEEN 1 AND 128),
 is_local boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX cells_site_fk ON cells(site_id);
CREATE UNIQUE INDEX cells_one_local ON cells(is_local) WHERE is_local;
INSERT INTO sites(id,display_name) VALUES('default','Default site');
INSERT INTO cells(id,site_id,display_name,is_local) VALUES('default','default','Default cell',true);
CREATE FUNCTION local_site_id() RETURNS varchar LANGUAGE sql STABLE AS $$ SELECT site_id FROM cells WHERE is_local $$;
CREATE FUNCTION local_cell_id() RETURNS varchar LANGUAGE sql STABLE AS $$ SELECT id FROM cells WHERE is_local $$;
ALTER TABLE api_keys ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE artifact_object_grants ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE artifact_object_ingestions ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE artifact_object_parts ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE artifact_objects ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE audit_events ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE aws_discovered_resources ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE aws_import_receipts ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE component_log_snapshots ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE dataset_drafts ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE dataset_job_attempts ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE dataset_jobs ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE dataset_row_history ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE dataset_rows ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE dataset_versions ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE datasets ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE deployments ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE gateway_instances ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE gateway_recoveries ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE gateway_requests ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE gpu_leases ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE idempotency_records ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE image_registry_blobs ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE image_registry_deployment_refs ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE image_registry_grants ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE image_registry_imports ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE image_registry_mutations ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE image_registry_offline_refs ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE image_registry_pins ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE inference_nodes ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE knowledge_attempts ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE knowledge_bases ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE knowledge_chunks ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE knowledge_documents ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE knowledge_generations ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE knowledge_mutation_intents ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE knowledge_source_versions ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE managed_components ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE managed_nodes ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE middleware_instances ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE middleware_operations ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE model_alias_cutover_intents ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE model_alias_history ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE model_alias_target_versions ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE model_aliases ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE model_derivations ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE model_holdout_prompts ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE model_holdout_sources ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE model_holdouts ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE model_import_chunks ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE model_imports ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE model_preparation_artifacts ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE model_preparation_attempts ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE model_preparation_events ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE model_preparation_recoveries ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE model_preparation_reviews ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE model_preparation_upload_chunks ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE model_preparation_uploads ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE model_preparations ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE model_versions ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE mongodb_dataset_imports ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE node_join_tokens ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE node_tasks ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE plugin_grants ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE plugin_mutation_intents ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE plugin_versions ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE private_cluster_connections ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE private_cluster_intents ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE private_cluster_runs ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE rate_limit_buckets ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE task_event_dead_letters ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE task_event_outbox ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE task_event_receipts ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE training_artifacts ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE training_attempts ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE training_checkpoints ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE training_events ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE training_jobs ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE training_recoveries ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE training_upload_chunks ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE training_uploads ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE workflow_conversations ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE workflow_mutation_intents ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE workflow_node_traces ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE workflow_plugin_references ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE workflow_runs ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE workflow_versions ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE workflows ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
-- Task events now carry their scope; shared brokers route on it.
CREATE OR REPLACE FUNCTION task_event_outbox_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE source_time timestamptz; source_operation text; j jsonb;
BEGIN
 IF TG_OP='DELETE' THEN
  IF OLD.outcome_unknown OR OLD.claim_id IS NOT NULL OR OLD.state NOT IN('received','obsolete','dead') OR OLD.expires_at>clock_timestamp() OR NOT EXISTS(SELECT 1 FROM node_tasks t WHERE (t.tenant_id,t.workspace_id,t.node_id,t.id)=(OLD.tenant_id,OLD.workspace_id,OLD.node_id,OLD.task_id) AND t.state IN('succeeded','failed','cancelled')) THEN
   RAISE EXCEPTION 'TASK_EVENT_RETENTION_REFUSED';
  END IF;
  RETURN OLD;
 END IF;
 IF TG_OP='INSERT' THEN
  SELECT t.created_at,t.operation INTO source_time,source_operation FROM node_tasks t JOIN managed_nodes n ON (n.tenant_id,n.workspace_id,n.id)=(t.tenant_id,t.workspace_id,t.node_id) JOIN workspaces w ON (w.tenant_id,w.id)=(n.tenant_id,n.workspace_id) WHERE (t.tenant_id,t.workspace_id,t.node_id,t.id)=(NEW.tenant_id,NEW.workspace_id,NEW.node_id,NEW.task_id) AND t.state='pending' AND t.attempt=0 AND n.status='active' AND w.status='active';
  IF NOT FOUND OR source_operation<>NEW.operation OR NEW.generation<>1 OR NEW.state<>'prepared' OR NEW.outcome_unknown OR NEW.publish_attempts<>0 OR NEW.consume_attempts<>0 OR NEW.claim_id IS NOT NULL OR NEW.broker_sequence IS NOT NULL OR NEW.error_code IS NOT NULL OR NEW.not_before<>NEW.created_at OR NEW.updated_at<>NEW.created_at OR NEW.created_at<source_time OR encode(sha256(NEW.event_bytes),'hex')<>NEW.logical_sha256 THEN RAISE EXCEPTION 'TASK_EVENT_SOURCE_INVALID'; END IF;
  j:=convert_from(NEW.event_bytes,'UTF8')::jsonb;
  IF jsonb_typeof(j)<>'object' OR (SELECT count(*) FROM jsonb_object_keys(j))<>13 OR j->>'site_id' IS DISTINCT FROM NEW.site_id OR j->>'cell_id' IS DISTINCT FROM NEW.cell_id OR jsonb_typeof(j->'schema') IS DISTINCT FROM 'number' OR j->>'schema' IS DISTINCT FROM '1' OR j->>'event_id' IS DISTINCT FROM NEW.id::text OR j->>'namespace_id' IS DISTINCT FROM NEW.namespace_id::text OR j->>'tenant_id' IS DISTINCT FROM NEW.tenant_id::text OR j->>'workspace_id' IS DISTINCT FROM NEW.workspace_id::text OR j->>'node_id' IS DISTINCT FROM NEW.node_id::text OR j->>'task_id' IS DISTINCT FROM NEW.task_id::text OR j->>'kind' IS DISTINCT FROM NEW.event_kind OR j->>'operation' IS DISTINCT FROM NEW.operation OR (j->>'created_at')::timestamptz IS DISTINCT FROM NEW.created_at OR (j->>'task_created_at')::timestamptz IS DISTINCT FROM source_time THEN RAISE EXCEPTION 'TASK_EVENT_SOURCE_INVALID'; END IF;
  RETURN NEW;
 END IF;
 IF (NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.node_id,NEW.task_id,NEW.namespace_id,NEW.event_kind,NEW.operation,NEW.event_bytes,NEW.logical_sha256,NEW.created_at,NEW.expires_at,NEW.broker_identity,NEW.consumer_name) IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.node_id,OLD.task_id,OLD.namespace_id,OLD.event_kind,OLD.operation,OLD.event_bytes,OLD.logical_sha256,OLD.created_at,OLD.expires_at,OLD.broker_identity,OLD.consumer_name) OR NEW.updated_at<OLD.updated_at OR OLD.state IN('received','obsolete') THEN RAISE EXCEPTION 'TASK_EVENT_IMMUTABLE'; END IF;
 IF NEW.generation<>OLD.generation THEN
  IF NEW.generation<>OLD.generation+1 OR NEW.delivery_id=OLD.delivery_id OR OLD.state<>'dead' OR NEW.state<>'prepared' OR NEW.outcome_unknown OR NEW.publish_attempts<>0 OR NEW.consume_attempts<>0 OR NEW.broker_sequence IS NOT NULL OR NEW.claim_id IS NOT NULL OR NEW.error_code IS NOT NULL OR NEW.not_before<>NEW.updated_at OR NOT EXISTS(SELECT 1 FROM task_event_dead_letters d WHERE d.event_id=OLD.id AND d.generation=OLD.generation AND d.retry_generation=NEW.generation AND d.retry_delivery_id=NEW.delivery_id AND d.retried_at=NEW.updated_at) THEN RAISE EXCEPTION 'TASK_EVENT_RETRY_INVALID'; END IF;
 ELSE
  IF NEW.delivery_id<>OLD.delivery_id OR NEW.publish_attempts<OLD.publish_attempts OR NEW.consume_attempts<OLD.consume_attempts OR NOT ((OLD.state='prepared' AND NEW.state IN('prepared','sent','dead')) OR (OLD.state='sent' AND NEW.state IN('sent','published','received','obsolete','dead')) OR (OLD.state='published' AND NEW.state IN('published','received','obsolete','dead')) OR (OLD.state='dead' AND NEW.state IN('dead','received','obsolete'))) THEN RAISE EXCEPTION 'TASK_EVENT_TRANSITION_INVALID'; END IF;
 END IF;
 IF NEW.state IN('received','obsolete') AND NOT EXISTS(SELECT 1 FROM task_event_receipts r WHERE r.event_id=NEW.id AND r.logical_sha256=NEW.logical_sha256 AND r.delivery_id=NEW.delivery_id AND r.generation=NEW.generation AND r.broker_identity=NEW.broker_identity AND r.consumer_name=NEW.consumer_name AND r.broker_sequence=NEW.broker_sequence AND r.outcome=NEW.state) THEN RAISE EXCEPTION 'TASK_EVENT_RECEIPT_INVALID'; END IF;
 IF NEW.state='dead' AND NOT EXISTS(SELECT 1 FROM task_event_dead_letters d WHERE d.event_id=NEW.id AND d.generation=NEW.generation AND d.error_code=NEW.error_code) THEN RAISE EXCEPTION 'TASK_EVENT_DLQ_INVALID'; END IF;
 RETURN NEW;
END $$;

INSERT INTO schema_migrations(version,checksum) VALUES(30,'3ecfe24a652878a09a1be067261a099dae2bf1eccc5f44b2cfc2c1339881b76d');
-- Exact source: services/control/migrations/postgresql/000031_task_delivery.up.sql
-- Task delivery semantics (ARC-006, ADR 0011): every task of a node gets the next per-node
-- sequence, the agent acknowledges a lease before executing it, and a lease that was never
-- acknowledged returns to pending without spending an attempt.
ALTER TABLE managed_nodes ADD COLUMN task_sequence bigint NOT NULL DEFAULT 0 CHECK(task_sequence BETWEEN 0 AND 9007199254740991);
ALTER TABLE managed_nodes ADD COLUMN task_acknowledged_sequence bigint NOT NULL DEFAULT 0;
ALTER TABLE managed_nodes ADD COLUMN resync_granted_at timestamptz;
ALTER TABLE node_tasks ADD COLUMN sequence bigint;
ALTER TABLE node_tasks ADD COLUMN acknowledged_attempt integer NOT NULL DEFAULT 0;
ALTER TABLE node_tasks ADD COLUMN deliveries integer NOT NULL DEFAULT 0 CHECK(deliveries BETWEEN 0 AND 1000);

-- Existing tasks keep their order and count as delivered: they ran under the old rules.
UPDATE node_tasks t SET sequence=s.sequence,acknowledged_attempt=t.attempt,deliveries=t.attempt
 FROM (SELECT id,row_number() OVER (PARTITION BY tenant_id,workspace_id,node_id ORDER BY created_at,id) AS sequence FROM node_tasks) s
 WHERE s.id=t.id;
UPDATE managed_nodes n SET task_sequence=m.sequence,task_acknowledged_sequence=m.sequence
 FROM (SELECT tenant_id,workspace_id,node_id,max(sequence) AS sequence FROM node_tasks GROUP BY tenant_id,workspace_id,node_id) m
 WHERE (n.tenant_id,n.workspace_id,n.id)=(m.tenant_id,m.workspace_id,m.node_id);

ALTER TABLE managed_nodes ADD CONSTRAINT managed_nodes_task_acknowledged_sequence_check CHECK(task_acknowledged_sequence BETWEEN 0 AND task_sequence);
ALTER TABLE node_tasks ALTER COLUMN sequence SET NOT NULL;
ALTER TABLE node_tasks ADD CONSTRAINT node_tasks_sequence_check CHECK(sequence BETWEEN 1 AND 9007199254740991);
ALTER TABLE node_tasks ADD CONSTRAINT node_tasks_acknowledged_attempt_check CHECK(acknowledged_attempt BETWEEN 0 AND attempt);
ALTER TABLE node_tasks ADD CONSTRAINT node_tasks_node_sequence_key UNIQUE(tenant_id,workspace_id,node_id,sequence);

-- The sequence comes from the node row, so every insert path gets it and concurrent
-- inserts for one node serialize on that row. A caller cannot choose or change it.
CREATE FUNCTION node_task_sequence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' THEN
  IF NEW.sequence IS DISTINCT FROM OLD.sequence THEN RAISE EXCEPTION 'NODE_TASK_SEQUENCE_IMMUTABLE'; END IF;
  RETURN NEW;
 END IF;
 UPDATE managed_nodes SET task_sequence=task_sequence+1 WHERE tenant_id=NEW.tenant_id AND workspace_id=NEW.workspace_id AND id=NEW.node_id RETURNING task_sequence INTO NEW.sequence;
 IF NEW.sequence IS NULL THEN RAISE EXCEPTION 'NODE_TASK_SEQUENCE_UNAVAILABLE'; END IF;
 NEW.acknowledged_attempt:=0;
 NEW.deliveries:=0;
 RETURN NEW;
END $$;
CREATE TRIGGER node_task_sequence BEFORE INSERT OR UPDATE ON node_tasks FOR EACH ROW EXECUTE FUNCTION node_task_sequence();

-- A training or model preparation lease that was never acknowledged never reached an
-- executing agent, so its stop authority may be bound again when the task is delivered
-- again. Once the task is acknowledged the stop authority is immutable as before.
CREATE OR REPLACE FUNCTION protect_training_attempt_inputs() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.job_id,NEW.node_id,NEW.task_id,NEW.attempt_number,NEW.requested_by,NEW.requested_audience,NEW.requested_epoch,NEW.membership_authority,NEW.input_inventory,NEW.resume_checkpoint_id,NEW.created_at)
 IS DISTINCT FROM ROW(OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.job_id,OLD.node_id,OLD.task_id,OLD.attempt_number,OLD.requested_by,OLD.requested_audience,OLD.requested_epoch,OLD.membership_authority,OLD.input_inventory,OLD.resume_checkpoint_id,OLD.created_at)
 OR OLD.outcome IS NOT NULL AND ROW(NEW.outcome,NEW.outcome_sha256) IS DISTINCT FROM ROW(OLD.outcome,OLD.outcome_sha256)
 OR OLD.stop_token_hash IS NOT NULL AND NEW.stop_token_hash IS DISTINCT FROM OLD.stop_token_hash
  AND NOT (OLD.state='preparing' AND NEW.state='preparing' AND NEW.stop_token_hash IS NOT NULL AND EXISTS(SELECT 1 FROM node_tasks t WHERE t.id=OLD.task_id AND t.acknowledged_attempt=0))
 OR OLD.physical_stop_receipt IS NOT NULL AND ROW(NEW.physical_stop_receipt,NEW.physical_stop_sha256,NEW.physical_stopped_at) IS DISTINCT FROM ROW(OLD.physical_stop_receipt,OLD.physical_stop_sha256,OLD.physical_stopped_at)
 THEN RAISE EXCEPTION 'TRAINING_ATTEMPT_INPUTS_IMMUTABLE' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END$$;

CREATE OR REPLACE FUNCTION protect_model_preparation_attempt_inputs() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'MODEL_PREPARATION_ATTEMPT_IMMUTABLE'; END IF;
 IF (NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.preparation_id,NEW.node_id,NEW.task_id,NEW.attempt_number,NEW.requested_by,NEW.requested_audience,NEW.requested_epoch,NEW.membership_authority,NEW.command_sha256,NEW.created_at)
 IS DISTINCT FROM
 (OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.preparation_id,OLD.node_id,OLD.task_id,OLD.attempt_number,OLD.requested_by,OLD.requested_audience,OLD.requested_epoch,OLD.membership_authority,OLD.command_sha256,OLD.created_at)
 THEN RAISE EXCEPTION 'MODEL_PREPARATION_ATTEMPT_INPUTS_IMMUTABLE'; END IF;
 IF OLD.stop_token_hash IS NOT NULL AND NEW.stop_token_hash IS DISTINCT FROM OLD.stop_token_hash
  AND NOT (OLD.state='preparing' AND NEW.state='preparing' AND NEW.stop_token_hash IS NOT NULL AND EXISTS(SELECT 1 FROM node_tasks t WHERE t.id=OLD.task_id AND t.acknowledged_attempt=0))
 THEN RAISE EXCEPTION 'MODEL_PREPARATION_STOP_AUTHORITY_IMMUTABLE'; END IF;
 IF OLD.physical_stopped_at IS NOT NULL AND (NEW.physical_stopped_at,NEW.physical_stop_receipt,NEW.physical_stop_sha256) IS DISTINCT FROM (OLD.physical_stopped_at,OLD.physical_stop_receipt,OLD.physical_stop_sha256) THEN RAISE EXCEPTION 'MODEL_PREPARATION_STOP_PROOF_IMMUTABLE'; END IF;
 RETURN NEW;
END;
$$;

INSERT INTO schema_migrations(version,checksum) VALUES(31,'b4d8ba81b657181424cb4abbbb8b0f5e83ab7049491586cae244d4708330cce9');
-- Exact source: services/control/migrations/postgresql/000032_drop_component_log_snapshots.up.sql
-- Component log snapshots live in the log store (OBS-002, ADR 0012). Nothing reads or
-- writes this table any more. It held only the last snapshot per node, which the
-- administrator can fetch again with the logs action, so its rows are not carried over.
DROP TABLE component_log_snapshots;

INSERT INTO schema_migrations(version,checksum) VALUES(32,'341db7795c1737d1e2caa2951605f513633c5adb74821b31c7cd751a5b9c3add');
-- Exact source: services/control/migrations/postgresql/000033_agent_pacing.up.sql
-- How control paces the agents of this cell at runtime (ARC-007, ADR 0013). There is one
-- row per cell; the seeded row matches wire.DefaultPacing, and the bounds match
-- wire.Pacing.Validate, which agents apply again on their side.
CREATE TABLE agent_pacing (
 site_id varchar(32) NOT NULL DEFAULT local_site_id(),
 cell_id varchar(32) NOT NULL DEFAULT local_cell_id(),
 heartbeat_seconds integer NOT NULL CHECK(heartbeat_seconds BETWEEN 5 AND 120),
 jitter_percent integer NOT NULL CHECK(jitter_percent BETWEEN 0 AND 50),
 reconnect_base_seconds integer NOT NULL CHECK(reconnect_base_seconds BETWEEN 1 AND 60),
 reconnect_max_seconds integer NOT NULL CHECK(reconnect_max_seconds BETWEEN reconnect_base_seconds AND 900),
 version bigint NOT NULL CHECK(version>=1),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(site_id,cell_id)
);
INSERT INTO agent_pacing(heartbeat_seconds,jitter_percent,reconnect_base_seconds,reconnect_max_seconds,version) VALUES(10,10,1,60,1);

INSERT INTO schema_migrations(version,checksum) VALUES(33,'36335436e6a9939f93ff0d1c31d8b324ff427ee4157325708faea6a5077a8a1b');
-- Exact source: services/control/migrations/postgresql/000034_user_appearance.up.sql
-- The theme and density a user chooses follow the account across browsers (UX-118).
-- `system` means the browser follows the operating system. Rows still at revision 1 were
-- seeded, never chosen, so they move to `system`; the revision trigger requires the step.
ALTER TABLE user_preferences DROP CONSTRAINT user_preferences_theme_check;
ALTER TABLE user_preferences ADD CONSTRAINT user_preferences_theme_check CHECK(theme IN ('system','light','dark'));
ALTER TABLE user_preferences ALTER COLUMN theme SET DEFAULT 'system';
ALTER TABLE user_preferences ADD COLUMN density varchar(12) NOT NULL DEFAULT 'comfortable' CONSTRAINT user_preferences_density_check CHECK(density IN ('comfortable','compact'));
UPDATE user_preferences SET theme='system',revision=2,updated_at=clock_timestamp() WHERE revision=1;

INSERT INTO schema_migrations(version,checksum) VALUES(34,'c071c67561d53226602fcc21e6aae23f999d410669e5be264a5ef70b4991ab50');
-- Exact source: services/control/migrations/postgresql/000035_tenant_node_counts.up.sql
-- Live node counts (ARC-009, ADR 0020): enrollment reads the limit from one row instead of
-- counting managed_nodes, so its cost does not grow with the fleet. The trigger keeps the
-- count for every write path, and enrollment locks the row to serialize against the limit.
CREATE TABLE tenant_node_counts (
 site_id varchar(32) NOT NULL DEFAULT local_site_id(),
 cell_id varchar(32) NOT NULL DEFAULT local_cell_id(),
 tenant_id uuid NOT NULL REFERENCES tenants(id),
 live_nodes bigint NOT NULL CHECK(live_nodes>=0),
 PRIMARY KEY(site_id,cell_id,tenant_id)
);

INSERT INTO tenant_node_counts(site_id,cell_id,tenant_id,live_nodes)
 SELECT site_id,cell_id,tenant_id,count(*) FROM managed_nodes WHERE status<>'removed' GROUP BY site_id,cell_id,tenant_id;

CREATE FUNCTION tenant_node_count() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
 row_change managed_nodes := CASE WHEN TG_OP='DELETE' THEN OLD ELSE NEW END;
 change bigint := CASE WHEN TG_OP<>'DELETE' AND NEW.status<>'removed' THEN 1 ELSE 0 END
  - CASE WHEN TG_OP<>'INSERT' AND OLD.status<>'removed' THEN 1 ELSE 0 END;
BEGIN
 -- A decrease must not go through INSERT: its CHECK sees the proposed row first. The row
 -- exists, because the node being counted out was counted in.
 IF change>0 THEN
  INSERT INTO tenant_node_counts(site_id,cell_id,tenant_id,live_nodes) VALUES(row_change.site_id,row_change.cell_id,row_change.tenant_id,change)
   ON CONFLICT(site_id,cell_id,tenant_id) DO UPDATE SET live_nodes=tenant_node_counts.live_nodes+EXCLUDED.live_nodes;
 ELSIF change<0 THEN
  UPDATE tenant_node_counts SET live_nodes=live_nodes+change
   WHERE site_id=row_change.site_id AND cell_id=row_change.cell_id AND tenant_id=row_change.tenant_id;
 END IF;
 RETURN NULL;
END $$;

CREATE TRIGGER managed_nodes_live_count AFTER INSERT OR DELETE OR UPDATE OF status ON managed_nodes
 FOR EACH ROW EXECUTE FUNCTION tenant_node_count();

INSERT INTO schema_migrations(version,checksum) VALUES(35,'83326c9bbc2d8afc278510161de1f98694401c09fa0d35f64bbdb195bd85b805');
-- Exact source: services/control/migrations/postgresql/000036_mail_resend.up.sql
-- Administrators send a failed email again (UX-120). The write is idempotent, like the
-- invitation writes that share this table.
ALTER TABLE idempotency_records DROP CONSTRAINT idempotency_records_operation_id_check;
ALTER TABLE idempotency_records ADD CONSTRAINT idempotency_records_operation_id_check CHECK (operation_id IN ('createInvitation','resendInvitation','resendMailDelivery'));

INSERT INTO schema_migrations(version,checksum) VALUES(36,'70c3d6b8ffda14f88d1ecbdc4bb881d7c035d01ff4c6bc5b64ee1525547a6215');
-- Exact source: services/control/migrations/postgresql/000037_language_preference.up.sql
-- The language a person chooses follows the account, and an installation can set the language
-- for those who have not chosen (UX-109). `auto` means no choice: the tenant's default, then
-- the browser's languages. Existing accounts start there, so nobody's language changes.
ALTER TABLE user_preferences ADD COLUMN locale varchar(16) NOT NULL DEFAULT 'auto' CONSTRAINT user_preferences_locale_check CHECK (locale IN ('auto', 'en', 'zh-CN'));
ALTER TABLE tenants ADD COLUMN default_locale varchar(16) NOT NULL DEFAULT 'auto' CONSTRAINT tenants_default_locale_check CHECK (default_locale IN ('auto', 'en', 'zh-CN'));
ALTER TABLE tenants ADD COLUMN settings_revision bigint NOT NULL DEFAULT 1 CONSTRAINT tenants_settings_revision_check CHECK (settings_revision BETWEEN 1 AND 9007199254740991);

INSERT INTO schema_migrations(version,checksum) VALUES(37,'1323ec8f1dfe5dfb1f1243c696e18916ef2aad24af6c2b2f94112bdbc1341479');
-- Exact source: services/control/migrations/postgresql/000038_node_connectivity.up.sql
-- Connectivity changes of a node (ADR 0022). The last-seen time alone cannot say when a node
-- came back or how often it flaps. Readers that decide anything keep deriving connectivity
-- from last_heartbeat_at, so a stopped worker cannot leave a node looking online.
ALTER TABLE managed_nodes ADD COLUMN connectivity varchar(8) NOT NULL DEFAULT 'online' CHECK (connectivity IN ('online','unknown','offline'));
ALTER TABLE managed_nodes ADD COLUMN connectivity_changed_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP;

INSERT INTO schema_migrations(version,checksum) VALUES(38,'126c4e704d6aaaaf8d90857e575acb89ec2b88557825504ace20f7099f173147');
-- Exact source: services/control/migrations/postgresql/000039_saved_views.up.sql
-- Saved views of a console list: its filters, sort and columns under a name (UX-103). They
-- belong to one person and follow the account across browsers. The page owns the meaning of
-- `state`; the database only bounds its size and shape.
CREATE TABLE saved_views (
 id uuid PRIMARY KEY,
 user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 list_key varchar(64) NOT NULL CHECK (list_key ~ '^[a-z][a-z0-9-]{0,63}$'),
 name varchar(100) NOT NULL CHECK (name = btrim(name) AND length(name) BETWEEN 1 AND 100),
 state jsonb NOT NULL CHECK (jsonb_typeof(state) = 'object' AND octet_length(state::text) <= 4096),
 revision bigint NOT NULL DEFAULT 1 CHECK (revision BETWEEN 1 AND 9007199254740991),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CONSTRAINT saved_views_name_unique UNIQUE (user_id, list_key, name)
);

INSERT INTO schema_migrations(version,checksum) VALUES(39,'e2b5ff7d68ed8b312f6b342c427ba009c781e79547e8af7feecc5b4ef3894bb1');
-- Exact source: services/control/migrations/postgresql/000040_control_outbox.up.sql
-- Transactional outbox (ARC-011, ADR 0028). A writer appends in the transaction of its change, and
-- every gateway reads rows below the oldest running transaction: a transaction that started first
-- can commit last, so no value assigned at write time follows commit order. The key orders the read;
-- the second index serves trimming. No sequence, so the serving role needs none.
CREATE TABLE control_outbox (
 site_id varchar(32) NOT NULL DEFAULT local_site_id(),
 cell_id varchar(32) NOT NULL DEFAULT local_cell_id(),
 transaction_id xid8 NOT NULL DEFAULT pg_current_xact_id(),
 id uuid NOT NULL DEFAULT gen_random_uuid(),
 topic varchar(64) NOT NULL CHECK (topic ~ '^[a-z][a-z0-9_.]*$'),
 key varchar(128) NOT NULL CHECK (char_length(key)>0),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (site_id,cell_id,transaction_id,id)
);
CREATE INDEX control_outbox_trim_idx ON control_outbox(created_at);

INSERT INTO schema_migrations(version,checksum) VALUES(40,'7d1ccd8d8fc069ccd765daca70c66e3bb8134d1a78fc81753c422becce8e345e');
-- Exact source: services/control/migrations/postgresql/000041_partitioned_audit.up.sql
-- Monthly partitions for append tables (ARC-012, ADR 0029). The serving role has no DDL rights, so
-- this function, owned by the migration role, creates partitions ahead and drops expired ones. It
-- trusts no caller setting: its search_path is pinned, and the schema it is given must hold both
-- this function and the named partitioned table, owned by its owner. Only append tables are
-- partitioned, so that check is the list of tables it serves.
CREATE FUNCTION manage_time_partitions(owner_schema text, parent text, months_ahead integer, keep_days integer, OUT created integer, OUT dropped integer, OUT blocked integer, OUT reaches timestamptz)
LANGUAGE plpgsql SECURITY DEFINER
SET search_path = pg_catalog SET lock_timeout = '2s' SET timezone = 'UTC' AS $$
DECLARE
 parent_oid oid;
 this_month timestamptz := date_trunc('month', clock_timestamp());
 month_start timestamptz;
 cutoff timestamptz := clock_timestamp() - make_interval(days => keep_days);
 expired name;
 upper_bound CONSTANT text := 'TO \(''(.*)''\)';
BEGIN
 created := 0; dropped := 0; blocked := 0;
 IF months_ahead < 1 OR months_ahead > 24 OR keep_days < 0 THEN
  RAISE EXCEPTION 'TIME_PARTITION_ARGUMENTS_INVALID';
 END IF;
 SELECT c.oid INTO parent_oid FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
  WHERE n.nspname=owner_schema AND c.relname=parent AND c.relkind='p' AND c.relowner=(SELECT oid FROM pg_roles WHERE rolname=current_user)
   AND EXISTS(SELECT 1 FROM pg_proc p WHERE p.pronamespace=n.oid AND p.proname='manage_time_partitions');
 IF parent_oid IS NULL THEN
  RAISE EXCEPTION 'TIME_PARTITION_SCOPE_INVALID';
 END IF;

 SELECT max(substring(pg_get_expr(c.relpartbound, c.oid), upper_bound)::timestamptz) INTO reaches
  FROM pg_inherits i JOIN pg_class c ON c.oid=i.inhrelid WHERE i.inhparent=parent_oid;
 -- Months that passed unattended take no writes, so only those from now on are created.
 month_start := greatest(reaches, this_month);
 WHILE month_start <= this_month + make_interval(months => months_ahead) LOOP
  BEGIN
   -- Attaching, unlike PARTITION OF, does not block writers of the parent.
   EXECUTE format('CREATE TABLE %I.%I (LIKE %I.%I INCLUDING DEFAULTS INCLUDING CONSTRAINTS)', owner_schema, parent || '_p' || to_char(month_start, 'YYYYMM'), owner_schema, parent);
   EXECUTE format('ALTER TABLE %I.%I ATTACH PARTITION %I.%I FOR VALUES FROM (%L) TO (%L)', owner_schema, parent, owner_schema, parent || '_p' || to_char(month_start, 'YYYYMM'), month_start, month_start + interval '1 month');
  EXCEPTION WHEN lock_not_available THEN
   blocked := blocked + 1;
   RETURN;
  END;
  created := created + 1;
  month_start := month_start + interval '1 month';
  reaches := month_start;
 END LOOP;

 IF keep_days = 0 THEN
  RETURN;
 END IF;
 FOR expired IN
  SELECT c.relname FROM pg_inherits i JOIN pg_class c ON c.oid=i.inhrelid
   WHERE i.inhparent=parent_oid AND substring(pg_get_expr(c.relpartbound, c.oid), upper_bound)::timestamptz <= cutoff
 LOOP
  BEGIN
   EXECUTE format('DROP TABLE %I.%I', owner_schema, expired);
   dropped := dropped + 1;
  EXCEPTION WHEN lock_not_available THEN
   blocked := blocked + 1;
  END;
 END LOOP;
END
$$;

-- Existing rows stay where they are: the old table becomes the first partition and leaves with the
-- first drop past its last row. Nothing refers to this table, and it no longer refers to anything:
-- an audit row outlives what it names, each foreign key cost a statement per write, and the
-- Workspace key made renames wait for agent traffic (ADR 0020).
DO $$
DECLARE
 name_of_constraint name;
BEGIN
 FOR name_of_constraint IN SELECT conname FROM pg_constraint WHERE conrelid='audit_events'::regclass AND contype IN ('f','p') LOOP
  EXECUTE format('ALTER TABLE audit_events DROP CONSTRAINT %I', name_of_constraint);
 END LOOP;
END
$$;
-- Access goes through the parent, so a partition grants nothing itself.
DO $$
DECLARE
 grantee_role name;
BEGIN
 FOR grantee_role IN SELECT DISTINCT r.rolname FROM pg_class c CROSS JOIN LATERAL aclexplode(c.relacl) a JOIN pg_roles r ON r.oid=a.grantee WHERE c.oid='audit_events'::regclass AND a.grantee<>c.relowner LOOP
  EXECUTE format('REVOKE ALL ON TABLE audit_events FROM %I', grantee_role);
 END LOOP;
END
$$;
ALTER TABLE audit_events RENAME TO audit_events_p000000;
ALTER INDEX audit_list_idx RENAME TO audit_events_p000000_list_idx;
ALTER INDEX audit_action_idx RENAME TO audit_events_p000000_action_idx;
ALTER INDEX audit_operation_lookup_idx RENAME TO audit_events_p000000_operation_idx;
ALTER TABLE audit_events_p000000 ADD PRIMARY KEY (id,occurred_at);
CREATE TABLE audit_events (LIKE audit_events_p000000 INCLUDING DEFAULTS INCLUDING CONSTRAINTS, PRIMARY KEY (id,occurred_at)) PARTITION BY RANGE (occurred_at);
CREATE INDEX audit_list_idx ON audit_events(occurred_at DESC, id DESC);
CREATE INDEX audit_action_idx ON audit_events(action, occurred_at DESC, id DESC);
CREATE INDEX audit_operation_lookup_idx ON audit_events(request_id,actor_user_id,audience,tenant_id);
DO $$
BEGIN
 EXECUTE format('ALTER TABLE audit_events ATTACH PARTITION audit_events_p000000 FOR VALUES FROM (MINVALUE) TO (%L)', date_trunc('month', clock_timestamp() AT TIME ZONE 'UTC') + interval '1 month');
END
$$;
SELECT manage_time_partitions(current_schema()::text, 'audit_events', 3, 0);

INSERT INTO schema_migrations(version,checksum) VALUES(41,'c66a1ab88297dfa55f37263717b0b719e79ef558596c91e1b2793711335e6eb8');
-- Exact source: services/control/migrations/postgresql/000042_partitioned_events.up.sql
-- Training and model preparation events move to monthly partitions (ARC-012, ADR 0029). The key of
-- a partitioned table must hold the partition key, and (attempt_id,sequence) must stay unique, so
-- rows partition by the creation time of their attempt: it is fixed per attempt, and adding it to
-- the key weakens nothing. An attempt's events stay together, and a running attempt writes to the
-- month it started in.
ALTER TABLE training_events DROP CONSTRAINT training_events_pkey;
DROP INDEX training_event_job_page_idx;
DROP INDEX training_checkpoint_event_step_idx;
DROP TRIGGER training_event_immutable ON training_events;
ALTER TABLE training_events ADD COLUMN attempt_created_at timestamptz;
UPDATE training_events e SET attempt_created_at=a.created_at FROM training_attempts a WHERE a.id=e.attempt_id;
ALTER TABLE training_events ALTER COLUMN attempt_created_at SET NOT NULL;

ALTER TABLE model_preparation_events DROP CONSTRAINT model_preparation_events_pkey;
DROP TRIGGER model_preparation_event_immutable ON model_preparation_events;
ALTER TABLE model_preparation_events ADD COLUMN attempt_created_at timestamptz;
UPDATE model_preparation_events e SET attempt_created_at=a.created_at FROM model_preparation_attempts a WHERE a.id=e.attempt_id;
ALTER TABLE model_preparation_events ALTER COLUMN attempt_created_at SET NOT NULL;

-- Access goes through the parent, so a partition grants nothing itself.
DO $$
DECLARE
 event_table name;
 grantee_role name;
BEGIN
 FOREACH event_table IN ARRAY ARRAY['training_events','model_preparation_events'] LOOP
  FOR grantee_role IN SELECT DISTINCT r.rolname FROM pg_class c CROSS JOIN LATERAL aclexplode(c.relacl) a JOIN pg_roles r ON r.oid=a.grantee WHERE c.oid=event_table::regclass AND a.grantee<>c.relowner LOOP
   EXECUTE format('REVOKE ALL ON TABLE %I FROM %I', event_table, grantee_role);
  END LOOP;
 END LOOP;
END
$$;

ALTER TABLE training_events RENAME TO training_events_p000000;
ALTER TABLE training_events_p000000 ADD PRIMARY KEY (attempt_id,sequence,attempt_created_at);
CREATE TABLE training_events (LIKE training_events_p000000 INCLUDING DEFAULTS INCLUDING CONSTRAINTS, PRIMARY KEY (attempt_id,sequence,attempt_created_at)) PARTITION BY RANGE (attempt_created_at);
ALTER TABLE training_events ADD FOREIGN KEY (tenant_id,workspace_id,job_id,attempt_id) REFERENCES training_attempts(tenant_id,workspace_id,job_id,id);
CREATE INDEX training_event_job_page_idx ON training_events(tenant_id,workspace_id,job_id,attempt_id,sequence);
CREATE UNIQUE INDEX training_checkpoint_event_step_idx ON training_events(attempt_id,global_step,attempt_created_at) WHERE kind='checkpoint';
CREATE TRIGGER training_event_immutable BEFORE UPDATE OR DELETE ON training_events FOR EACH ROW EXECUTE FUNCTION reject_training_immutable_change();

ALTER TABLE model_preparation_events RENAME TO model_preparation_events_p000000;
ALTER TABLE model_preparation_events_p000000 ADD PRIMARY KEY (attempt_id,sequence,attempt_created_at);
CREATE TABLE model_preparation_events (LIKE model_preparation_events_p000000 INCLUDING DEFAULTS INCLUDING CONSTRAINTS, PRIMARY KEY (attempt_id,sequence,attempt_created_at)) PARTITION BY RANGE (attempt_created_at);
ALTER TABLE model_preparation_events ADD FOREIGN KEY (tenant_id,workspace_id,preparation_id,attempt_id) REFERENCES model_preparation_attempts(tenant_id,workspace_id,preparation_id,id);
CREATE TRIGGER model_preparation_event_immutable BEFORE UPDATE OR DELETE ON model_preparation_events FOR EACH ROW EXECUTE FUNCTION protect_model_preparation_event();

DO $$
DECLARE
 next_month timestamptz := date_trunc('month', clock_timestamp() AT TIME ZONE 'UTC') + interval '1 month';
BEGIN
 EXECUTE format('ALTER TABLE training_events ATTACH PARTITION training_events_p000000 FOR VALUES FROM (MINVALUE) TO (%L)', next_month);
 EXECUTE format('ALTER TABLE model_preparation_events ATTACH PARTITION model_preparation_events_p000000 FOR VALUES FROM (MINVALUE) TO (%L)', next_month);
END
$$;
SELECT manage_time_partitions(current_schema()::text, 'training_events', 3, 0);
SELECT manage_time_partitions(current_schema()::text, 'model_preparation_events', 3, 0);

INSERT INTO schema_migrations(version,checksum) VALUES(42,'9828587b06630df4c6cd1d48305d89b588fb393b7a8a9579151ee5192fee0ffe');
-- Exact source: services/control/migrations/postgresql/000043_node_task_history.up.sql
-- Finished tasks get an append table (ARC-012, ADR 0029). node_tasks cannot be partitioned: its rows
-- change, other tables refer to them and one partial unique index keeps one running task per node.
-- A trigger writes the history, so no path that ends a task can miss it, and finished rows nothing
-- refers to then leave node_tasks. The columns are those the task list shows.
CREATE TABLE node_task_history (
 site_id varchar(32) NOT NULL DEFAULT local_site_id(),
 cell_id varchar(32) NOT NULL DEFAULT local_cell_id(),
 id uuid NOT NULL,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 node_id uuid NOT NULL,
 operation varchar(64) NOT NULL,
 state varchar(16) NOT NULL CHECK (state IN ('succeeded','failed','cancelled')),
 attempt integer NOT NULL,
 error_code varchar(64),
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 deadline_at timestamptz NOT NULL,
 completed_at timestamptz NOT NULL,
 PRIMARY KEY (id,completed_at)
) PARTITION BY RANGE (completed_at);
CREATE INDEX node_task_history_list_idx ON node_task_history(tenant_id,workspace_id,node_id,created_at DESC,id DESC);

-- Tasks that finished before this migration land in the first partition.
CREATE TABLE node_task_history_p000000 (LIKE node_task_history INCLUDING DEFAULTS INCLUDING CONSTRAINTS);
DO $$
BEGIN
 EXECUTE format('ALTER TABLE node_task_history ATTACH PARTITION node_task_history_p000000 FOR VALUES FROM (MINVALUE) TO (%L)', date_trunc('month', clock_timestamp() AT TIME ZONE 'UTC') + interval '1 month');
END
$$;
SELECT manage_time_partitions(current_schema()::text, 'node_task_history', 3, 0);
INSERT INTO node_task_history(site_id,cell_id,id,tenant_id,workspace_id,node_id,operation,state,attempt,error_code,created_at,updated_at,deadline_at,completed_at)
 SELECT site_id,cell_id,id,tenant_id,workspace_id,node_id,operation,state,attempt,error_code,created_at,updated_at,deadline_at,completed_at FROM node_tasks WHERE completed_at IS NOT NULL;

CREATE FUNCTION record_node_task_history() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.completed_at IS NULL OR (TG_OP='UPDATE' AND OLD.completed_at IS NOT NULL) THEN
  RETURN NULL;
 END IF;
 INSERT INTO node_task_history(site_id,cell_id,id,tenant_id,workspace_id,node_id,operation,state,attempt,error_code,created_at,updated_at,deadline_at,completed_at)
  VALUES(NEW.site_id,NEW.cell_id,NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.node_id,NEW.operation,NEW.state,NEW.attempt,NEW.error_code,NEW.created_at,NEW.updated_at,NEW.deadline_at,NEW.completed_at);
 RETURN NULL;
END$$;
CREATE TRIGGER node_task_finished AFTER INSERT OR UPDATE OF state ON node_tasks FOR EACH ROW EXECUTE FUNCTION record_node_task_history();

-- Pruning walks finished tasks in order, and deleting one checks what refers to it.
CREATE INDEX node_tasks_finished_idx ON node_tasks(completed_at,id) WHERE completed_at IS NOT NULL;
CREATE INDEX managed_components_last_task_idx ON managed_components(last_task_id) WHERE last_task_id IS NOT NULL;
CREATE INDEX deployments_last_task_idx ON deployments(last_task_id) WHERE last_task_id IS NOT NULL;
CREATE INDEX model_preparation_attempts_task_idx ON model_preparation_attempts(task_id);
CREATE INDEX task_event_outbox_task_idx ON task_event_outbox(task_id);
CREATE INDEX image_registry_grants_task_idx ON image_registry_grants(task_id);

INSERT INTO schema_migrations(version,checksum) VALUES(43,'a03214b3a1839f71ba1928b5cde88b42d0ee91ea3b3ddce550a7ea2defb77909');
-- Exact source: services/control/migrations/postgresql/000044_node_task_progress.up.sql
-- What a running task reports: its steps and its log (UX-104, ADR 0030). This is an append table, so
-- it partitions by month (ADR 0029), by the creation time of its task: that is fixed per task, so a
-- task's rows stay together and adding it to the key weakens nothing. It keeps no foreign key,
-- because a task's row leaves node_tasks while its progress stays readable through the history.
CREATE TABLE node_task_progress (
 site_id varchar(32) NOT NULL DEFAULT local_site_id(),
 cell_id varchar(32) NOT NULL DEFAULT local_cell_id(),
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 node_id uuid NOT NULL,
 task_id uuid NOT NULL,
 task_created_at timestamptz NOT NULL,
 attempt integer NOT NULL CHECK (attempt>=1),
 sequence integer NOT NULL CHECK (sequence BETWEEN 1 AND 4129),
 kind varchar(8) NOT NULL CHECK (kind IN ('step','log')),
 step varchar(32),
 step_state varchar(16) CHECK (step_state IN ('running','succeeded','failed')),
 level varchar(8) CHECK (level IN ('info','warning','error')),
 message varchar(512),
 reported_at timestamptz NOT NULL,
 received_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (task_id,attempt,sequence,task_created_at),
 CHECK ((kind='step' AND step IS NOT NULL AND step_state IS NOT NULL AND level IS NULL AND message IS NULL)
     OR (kind='log' AND step IS NULL AND step_state IS NULL AND level IS NOT NULL AND message IS NOT NULL))
) PARTITION BY RANGE (task_created_at);
-- The timeline reads a few step rows out of a log that may hold thousands of lines.
CREATE INDEX node_task_progress_steps_idx ON node_task_progress(task_id,attempt,sequence) WHERE kind='step';

-- A task created before this migration may still run, and reports into the first partition.
DO $$
BEGIN
 EXECUTE format('CREATE TABLE node_task_progress_p000000 PARTITION OF node_task_progress FOR VALUES FROM (MINVALUE) TO (%L)', date_trunc('month', clock_timestamp() AT TIME ZONE 'UTC') + interval '1 month');
END
$$;
SELECT manage_time_partitions(current_schema()::text, 'node_task_progress', 3, 0);

-- The task list reads every node of a tenant, newest first, live tasks and the history apart.
CREATE INDEX node_tasks_tenant_live_idx ON node_tasks(tenant_id,created_at DESC,id DESC) WHERE completed_at IS NULL;
CREATE INDEX node_tasks_requester_live_idx ON node_tasks(requested_by,created_at DESC,id DESC) WHERE completed_at IS NULL;
CREATE INDEX node_task_history_tenant_idx ON node_task_history(tenant_id,created_at DESC,id DESC);
CREATE INDEX node_task_history_tenant_state_idx ON node_task_history(tenant_id,state,created_at DESC,id DESC);

INSERT INTO schema_migrations(version,checksum) VALUES(44,'28bc69025a891ed270bef69fdb0f3f96b0e9c504bac0e1f3a849b541e2f4c36f');
-- Exact source: services/control/migrations/postgresql/000045_single_key_shortcuts.up.sql
-- A person can turn the console's one-character shortcuts off, and the choice follows the account
-- (UX-122, WCAG 2.2 criterion 2.1.4). Existing rows keep their revision: nobody chose this.
ALTER TABLE user_preferences ADD COLUMN single_key_shortcuts boolean NOT NULL DEFAULT true;

INSERT INTO schema_migrations(version,checksum) VALUES(45,'9bff012606cd4b5d891ce12ae1936ae4326fae37316b6db0eee0b3683d5694f3');
-- Exact source: services/control/migrations/postgresql/000046_navigation_collapsed.up.sql
-- A person can keep the sidebar to its icons, and the choice follows the account (UX-123, ADR 0037).
-- Existing rows keep their revision: nobody chose this.
ALTER TABLE user_preferences ADD COLUMN navigation_collapsed boolean NOT NULL DEFAULT false;

INSERT INTO schema_migrations(version,checksum) VALUES(46,'9661869ed9d8e32e590fdbed3017fc3026a31a45d999e25a930cd099c802eac4');
-- Exact source: services/control/migrations/postgresql/000047_pinned_destinations.up.sql
-- A person can pin pages to the top of the sidebar. The console and the portal have different pages, so
-- each entrance keeps its own list (UX-124, ADR 0037). Existing rows keep their revision: nobody chose this.
ALTER TABLE user_preferences
 ADD COLUMN pinned_console jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(pinned_console)='array'),
 ADD COLUMN pinned_portal jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(pinned_portal)='array');

INSERT INTO schema_migrations(version,checksum) VALUES(47,'bcddac6366163a6fa08e02a9992457862d54ee5affc76af15cd8643abe956b37');
-- Exact source: services/control/migrations/postgresql/000048_audit_target_index.up.sql
-- The activity of one resource, such as a node's detail page (UX-127). Most events have a target, and this
-- leaves out those that do not. An index on the parent reaches every partition, now and to come (ADR 0029).
CREATE INDEX audit_events_target_idx ON audit_events(target_id,occurred_at DESC,id DESC) WHERE target_id IS NOT NULL;

INSERT INTO schema_migrations(version,checksum) VALUES(48,'4c11b9c0dc7462ea5c05a385ffa20013a28081e5d608334239ffea8e6332c8a9');
-- Exact source: services/control/migrations/postgresql/000049_agent_shed.up.sql
-- An operator can tell control to refuse agent traffic, lowest priority first (ARC-016, ADR 0043). The
-- level lives beside the pacing of the cell, so it survives a restart and reaches every gateway.
ALTER TABLE agent_pacing
 ADD COLUMN shed_level text NOT NULL DEFAULT 'none' CHECK (shed_level IN ('none','bulk','telemetry','heartbeats')),
 ADD COLUMN shed_version bigint NOT NULL DEFAULT 1 CHECK (shed_version>=1),
 ADD COLUMN shed_updated_at timestamptz NOT NULL DEFAULT clock_timestamp();

INSERT INTO schema_migrations(version,checksum) VALUES(49,'1b73791a4ca9cdafe570921906881c0cff7c95015bdda3ab0898b848db84b0f2');
-- Exact source: services/control/migrations/postgresql/000050_node_view_events.up.sql
-- Each gateway answers repeated heartbeats from a view of the node held in memory (ARC-101, ADR 0047).
-- These triggers tell it what changed, in the transaction of the change, so no writer can forget.
-- Only the columns of the view count: writes that repeat a value, as heartbeats do, append nothing.
CREATE FUNCTION node_view_changed() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN
  INSERT INTO control_outbox(topic,key) VALUES('node.view_changed',OLD.id::text);
 ELSIF OLD.status IS DISTINCT FROM NEW.status OR OLD.credential_hash IS DISTINCT FROM NEW.credential_hash
  OR OLD.protocol_version IS DISTINCT FROM NEW.protocol_version OR OLD.execution_profile IS DISTINCT FROM NEW.execution_profile
  OR OLD.capabilities IS DISTINCT FROM NEW.capabilities OR OLD.workspace_id IS DISTINCT FROM NEW.workspace_id THEN
  INSERT INTO control_outbox(topic,key) VALUES('node.view_changed',NEW.id::text);
 END IF;
 RETURN NULL;
END $$;

CREATE TRIGGER managed_nodes_view_changed AFTER DELETE OR UPDATE OF status,credential_hash,protocol_version,execution_profile,capabilities,workspace_id
 ON managed_nodes FOR EACH ROW EXECUTE FUNCTION node_view_changed();

-- A workspace or tenant holds many nodes and changes rarely, so its event drops every view.
CREATE FUNCTION node_views_changed() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.status IS DISTINCT FROM NEW.status THEN
  INSERT INTO control_outbox(topic,key) VALUES('node.views_changed',NEW.id::text);
 END IF;
 RETURN NULL;
END $$;

CREATE TRIGGER workspaces_views_changed AFTER UPDATE OF status ON workspaces FOR EACH ROW EXECUTE FUNCTION node_views_changed();
CREATE TRIGGER tenants_views_changed AFTER UPDATE OF status ON tenants FOR EACH ROW EXECUTE FUNCTION node_views_changed();

INSERT INTO schema_migrations(version,checksum) VALUES(50,'bedd7922eeefa170fcbbb1086ed7a3d86cfe3b22377fb5f115e400494aa19a74');
-- Exact source: services/control/migrations/postgresql/000051_key_rotation.up.sql
-- A rotated key names the key it replaces, and a key is replaced at most once, so two people
-- rotating the same key at once cannot both win (GW-113, docs/research/gateway-keys.md).
ALTER TABLE api_keys ADD COLUMN rotated_from uuid;
ALTER TABLE api_keys ADD CONSTRAINT api_key_rotated_from_fk FOREIGN KEY(tenant_id,workspace_id,rotated_from) REFERENCES api_keys(tenant_id,workspace_id,id);
ALTER TABLE api_keys ADD CONSTRAINT api_key_not_its_own_successor CHECK(rotated_from IS NULL OR rotated_from<>id);
CREATE UNIQUE INDEX api_key_one_successor_idx ON api_keys(rotated_from) WHERE rotated_from IS NOT NULL;
-- One key's calls, newest first, for the key's call log.
CREATE INDEX gateway_request_key_idx ON gateway_requests(tenant_id,workspace_id,api_key_id,admitted_at DESC,id DESC);

INSERT INTO schema_migrations(version,checksum) VALUES(51,'fe5fd1209d158062288a35f481b6bdb3e248f7b26f92168dd359176ee8a13c8d');
-- Exact source: services/control/migrations/postgresql/000052_node_certificates.up.sql
-- The keys of a node's client certificates (ARC-103, ADR 0050). A connection's key must be the
-- current one or, while a rotation has not been confirmed, the previous one.
ALTER TABLE managed_nodes
 ADD COLUMN certificate_key_sha256 bytea CHECK (certificate_key_sha256 IS NULL OR octet_length(certificate_key_sha256)=32),
 ADD COLUMN previous_certificate_key_sha256 bytea CHECK (previous_certificate_key_sha256 IS NULL OR octet_length(previous_certificate_key_sha256)=32),
 ADD COLUMN certificate_not_after timestamptz,
 ADD CONSTRAINT managed_nodes_previous_certificate_needs_current CHECK (previous_certificate_key_sha256 IS NULL OR certificate_key_sha256 IS NOT NULL);

-- Gateways hold the pins in their node views, so a change to them is a change to the view.
CREATE OR REPLACE FUNCTION node_view_changed() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN
  INSERT INTO control_outbox(topic,key) VALUES('node.view_changed',OLD.id::text);
 ELSIF OLD.status IS DISTINCT FROM NEW.status OR OLD.credential_hash IS DISTINCT FROM NEW.credential_hash
  OR OLD.protocol_version IS DISTINCT FROM NEW.protocol_version OR OLD.execution_profile IS DISTINCT FROM NEW.execution_profile
  OR OLD.capabilities IS DISTINCT FROM NEW.capabilities OR OLD.workspace_id IS DISTINCT FROM NEW.workspace_id
  OR OLD.certificate_key_sha256 IS DISTINCT FROM NEW.certificate_key_sha256
  OR OLD.previous_certificate_key_sha256 IS DISTINCT FROM NEW.previous_certificate_key_sha256 THEN
  INSERT INTO control_outbox(topic,key) VALUES('node.view_changed',NEW.id::text);
 END IF;
 RETURN NULL;
END $$;

DROP TRIGGER managed_nodes_view_changed ON managed_nodes;
CREATE TRIGGER managed_nodes_view_changed AFTER DELETE OR UPDATE OF status,credential_hash,protocol_version,execution_profile,capabilities,workspace_id,certificate_key_sha256,previous_certificate_key_sha256
 ON managed_nodes FOR EACH ROW EXECUTE FUNCTION node_view_changed();

INSERT INTO schema_migrations(version,checksum) VALUES(52,'736d56df8e2d21f9ae75cd0d2e1495d487eebf34a70173ed8622a97910947dd9');
-- Exact source: services/control/migrations/postgresql/000053_audit_hash_chain.up.sql
-- Existing rows precede the chain. Keeping their columns null avoids rewriting
-- every retained partition and makes the coverage boundary explicit.
ALTER TABLE audit_events
    ADD COLUMN chain_position bigint,
    ADD COLUMN prev_hash bytea,
    ADD COLUMN hash bytea,
    ADD CONSTRAINT audit_events_chain_shape CHECK (
        (chain_position IS NULL AND prev_hash IS NULL AND hash IS NULL)
        OR (chain_position IS NOT NULL AND chain_position > 0
            AND prev_hash IS NOT NULL AND octet_length(prev_hash) = 32
            AND hash IS NOT NULL AND octet_length(hash) = 32)
    );

CREATE TABLE audit_chain_head (
    singleton boolean PRIMARY KEY CHECK (singleton),
    chain_position bigint NOT NULL CHECK (chain_position >= 0),
    hash bytea NOT NULL CHECK (octet_length(hash) = 32)
) WITH (fillfactor = 70);

INSERT INTO audit_chain_head(singleton, chain_position, hash)
VALUES (true, 0, decode(repeat('00', 32), 'hex'));

CREATE FUNCTION audit_chain_frame(value text) RETURNS bytea
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT CASE WHEN value IS NULL THEN int4send(-1)
        ELSE int4send(octet_length(convert_to(value, 'UTF8'))) || convert_to(value, 'UTF8')
    END
$$;

CREATE FUNCTION audit_chain_metadata(value jsonb) RETURNS bytea
LANGUAGE plpgsql STABLE STRICT PARALLEL SAFE AS $$
BEGIN
    CASE jsonb_typeof(value)
        WHEN 'null' THEN RETURN convert_to('n', 'UTF8');
        WHEN 'boolean' THEN
            RETURN convert_to(CASE WHEN value = 'true'::jsonb THEN 't' ELSE 'f' END, 'UTF8');
        WHEN 'string' THEN
            RETURN convert_to('s', 'UTF8') || audit_chain_frame(value #>> '{}');
        WHEN 'number' THEN
            RETURN convert_to('d', 'UTF8') || audit_chain_frame(trim_scale(value::numeric)::text);
        WHEN 'array' THEN
            RETURN (
                SELECT convert_to('a', 'UTF8') || int4send(count(*)::integer)
                    || coalesce(string_agg(audit_chain_metadata(item), ''::bytea ORDER BY ordinal), ''::bytea)
                FROM jsonb_array_elements(value) WITH ORDINALITY AS entries(item, ordinal)
            );
        WHEN 'object' THEN
            RETURN (
                SELECT convert_to('o', 'UTF8') || int4send(count(*)::integer)
                    || coalesce(string_agg(audit_chain_frame(key) || audit_chain_metadata(item), ''::bytea
                        ORDER BY convert_to(key, 'UTF8')), ''::bytea)
                FROM jsonb_each(value) AS entries(key, item)
            );
    END CASE;
END
$$;

-- The trigger and operator export share this projection. It commits to existing
-- diagnostic columns without putting their contents in the exported document.
CREATE FUNCTION audit_chain_projection(event jsonb, event_position bigint, occurred timestamptz, identifier bytea)
RETURNS jsonb LANGUAGE sql STABLE PARALLEL SAFE AS $$
    SELECT jsonb_build_object(
        'format', 'ownsolid.audit.v1',
        'position', event_position::text,
        'id', event ->> 'id',
        'occurred_at', to_char(occurred AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
        'actor_user_id', event ->> 'actor_user_id',
        'subject_user_id', event ->> 'subject_user_id',
        'tenant_id', event ->> 'tenant_id',
        'workspace_id', event ->> 'workspace_id',
        'audience', event ->> 'audience',
        'action', event ->> 'action',
        'outcome', event ->> 'outcome',
        'target_type', event ->> 'target_type',
        'target_id', event ->> 'target_id',
        'request_id', event ->> 'request_id',
        'reason_sha256', encode(sha256(convert_to(event ->> 'reason', 'UTF8')), 'hex'),
        'identifier_digest_sha256', encode(sha256(identifier), 'hex'),
        'metadata_sha256', encode(sha256(audit_chain_metadata(event -> 'metadata')), 'hex')
    )
$$;

CREATE FUNCTION audit_chain_append() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
DECLARE
    next_position bigint;
    predecessor bytea;
    projection jsonb;
    digest bytea;
    updated integer;
BEGIN
    IF NEW.chain_position IS NOT NULL OR NEW.prev_hash IS NOT NULL OR NEW.hash IS NOT NULL THEN
        RAISE EXCEPTION 'AUDIT_CHAIN_FIELDS_ASSIGNED_BY_DATABASE';
    END IF;
    IF NEW.occurred_at < '0001-01-01T00:00:00Z'::timestamptz
        OR NEW.occurred_at >= '10000-01-01T00:00:00Z'::timestamptz THEN
        RAISE EXCEPTION 'AUDIT_CHAIN_TIMESTAMP_OUT_OF_RANGE';
    END IF;

    -- Only the owner can bind this function as a trigger. Use the schema of
    -- that relation, never the writer's search path, for internal helpers.
    PERFORM set_config('search_path', format('pg_catalog, %I, pg_temp', TG_TABLE_SCHEMA), true);
    SELECT chain_position + 1, hash INTO STRICT next_position, predecessor
        FROM audit_chain_head WHERE singleton FOR UPDATE;
    projection := audit_chain_projection(to_jsonb(NEW), next_position, NEW.occurred_at, NEW.identifier_digest);

    SELECT sha256(convert_to('ownsolid.audit.v1', 'UTF8') || decode('00', 'hex') || predecessor
        || string_agg(audit_chain_frame(projection ->> field), ''::bytea ORDER BY ordinal))
    INTO STRICT digest
    FROM unnest(ARRAY[
        'position', 'id', 'occurred_at', 'actor_user_id', 'subject_user_id', 'tenant_id', 'workspace_id',
        'audience', 'action', 'outcome', 'target_type', 'target_id', 'request_id',
        'reason_sha256', 'identifier_digest_sha256', 'metadata_sha256'
    ]) WITH ORDINALITY AS fields(field, ordinal);

    UPDATE audit_events SET chain_position = next_position, prev_hash = predecessor, hash = digest
        WHERE id = NEW.id AND occurred_at = NEW.occurred_at AND chain_position IS NULL;
    GET DIAGNOSTICS updated = ROW_COUNT;
    IF updated <> 1 THEN
        RAISE EXCEPTION 'AUDIT_CHAIN_WRITE_UNCONFIRMED';
    END IF;
    UPDATE audit_chain_head SET chain_position = next_position, hash = digest WHERE singleton;
    RETURN NULL;
END
$$;

REVOKE ALL ON FUNCTION audit_chain_append() FROM PUBLIC;
REVOKE ALL ON TABLE audit_chain_head FROM PUBLIC;

-- AFTER INSERT only processes rows that survived ON CONFLICT DO NOTHING.
CREATE TRIGGER audit_chain_append AFTER INSERT ON audit_events
    FOR EACH ROW EXECUTE FUNCTION audit_chain_append();

INSERT INTO schema_migrations(version,checksum) VALUES(53,'fedc30201d7d8aea733598b5a85b6f3403d666db0692ae4b7ba2e198fbe4ae1a');
-- Exact source: services/control/migrations/postgresql/000054_audit_chain_scope.up.sql
-- Preserve the original chain and any checkpoints held outside PostgreSQL.
-- The first V2 position suffices to select the format for each retained row.
ALTER TABLE audit_chain_head ADD COLUMN scoped_from bigint
    CHECK (scoped_from BETWEEN 1 AND chain_position);

CREATE FUNCTION audit_chain_projection_v2(event jsonb, event_position bigint, occurred timestamptz, identifier bytea)
RETURNS jsonb LANGUAGE sql STABLE PARALLEL SAFE AS $$
    SELECT audit_chain_projection(event, event_position, occurred, identifier)
        || jsonb_build_object('format', 'ownsolid.audit.v2', 'site_id', event ->> 'site_id', 'cell_id', event ->> 'cell_id')
$$;

CREATE FUNCTION audit_chain_append_v2() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
DECLARE
    next_position bigint;
    predecessor bytea;
    projection jsonb;
    digest bytea;
    updated integer;
BEGIN
    IF NEW.chain_position IS NOT NULL OR NEW.prev_hash IS NOT NULL OR NEW.hash IS NOT NULL THEN
        RAISE EXCEPTION 'AUDIT_CHAIN_FIELDS_ASSIGNED_BY_DATABASE';
    END IF;
    IF NEW.occurred_at < '0001-01-01T00:00:00Z'::timestamptz
        OR NEW.occurred_at >= '10000-01-01T00:00:00Z'::timestamptz THEN
        RAISE EXCEPTION 'AUDIT_CHAIN_TIMESTAMP_OUT_OF_RANGE';
    END IF;

    -- Only the owner can bind this function as a trigger. Use the schema of
    -- that relation, never the writer's search path, for internal helpers.
    PERFORM set_config('search_path', format('pg_catalog, %I, pg_temp', TG_TABLE_SCHEMA), true);
    SELECT chain_position + 1, hash INTO STRICT next_position, predecessor
        FROM audit_chain_head WHERE singleton FOR UPDATE;
    projection := audit_chain_projection_v2(to_jsonb(NEW), next_position, NEW.occurred_at, NEW.identifier_digest);

    SELECT sha256(convert_to('ownsolid.audit.v2', 'UTF8') || decode('00', 'hex') || predecessor
        || string_agg(audit_chain_frame(projection ->> field), ''::bytea ORDER BY ordinal))
    INTO STRICT digest
    FROM unnest(ARRAY[
        'position', 'id', 'occurred_at', 'actor_user_id', 'subject_user_id', 'tenant_id', 'workspace_id',
        'audience', 'action', 'outcome', 'target_type', 'target_id', 'request_id',
        'reason_sha256', 'identifier_digest_sha256', 'metadata_sha256', 'site_id', 'cell_id'
    ]) WITH ORDINALITY AS fields(field, ordinal);

    UPDATE audit_events SET chain_position = next_position, prev_hash = predecessor, hash = digest
        WHERE id = NEW.id AND occurred_at = NEW.occurred_at AND chain_position IS NULL;
    GET DIAGNOSTICS updated = ROW_COUNT;
    IF updated <> 1 THEN
        RAISE EXCEPTION 'AUDIT_CHAIN_WRITE_UNCONFIRMED';
    END IF;
    UPDATE audit_chain_head SET chain_position = next_position, hash = digest,
        scoped_from = coalesce(scoped_from, next_position) WHERE singleton;
    RETURN NULL;
END
$$;

REVOKE ALL ON FUNCTION audit_chain_append_v2() FROM PUBLIC;
DROP TRIGGER audit_chain_append ON audit_events;
CREATE TRIGGER audit_chain_append AFTER INSERT ON audit_events
    FOR EACH ROW EXECUTE FUNCTION audit_chain_append_v2();

INSERT INTO schema_migrations(version,checksum) VALUES(54,'8aab33c9fc7d87adceec2f5e57d321c26b1ba86945e0d47042c7acae7e5f7bb4');
-- Exact source: services/control/migrations/postgresql/000055_audit_append_only.up.sql
-- The effective owner also executes the chain trigger's same-transaction
-- update. The serving role can append events and read the resulting hashes.
CREATE FUNCTION audit_events_require_owner() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog, pg_temp AS $$
BEGIN
    IF current_user <> (SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid = TG_RELID) THEN
        RAISE EXCEPTION USING ERRCODE = '42501', MESSAGE = 'AUDIT_EVENTS_APPEND_ONLY';
    END IF;
    IF TG_OP = 'UPDATE' THEN
        RETURN NEW;
    END IF;
    RETURN OLD;
END
$$;

REVOKE ALL ON FUNCTION audit_events_require_owner() FROM PUBLIC;
CREATE TRIGGER audit_events_require_owner_for_mutation
    BEFORE UPDATE OR DELETE ON audit_events
    FOR EACH ROW EXECUTE FUNCTION audit_events_require_owner();
CREATE TRIGGER audit_events_require_owner_for_truncate
    BEFORE TRUNCATE ON audit_events
    FOR EACH STATEMENT EXECUTE FUNCTION audit_events_require_owner();

INSERT INTO schema_migrations(version,checksum) VALUES(55,'21a35b57f58041b09750767286bd509e3c225c51c28bfe7a55de81dbbd53c037');
-- Exact source: services/control/migrations/postgresql/000056_cell_clusters.up.sql
-- One Kubernetes cluster per cell, installed by ownsolid on nodes of the kubernetes_node
-- profile (ARC-104, ADR 0051). Control writes the tasks; each node's agent installs RKE2.
ALTER TABLE node_join_tokens DROP CONSTRAINT node_join_tokens_execution_profile_check;
ALTER TABLE node_join_tokens ADD CONSTRAINT node_join_tokens_execution_profile_check CHECK(execution_profile IN ('read_only','systemd_components','systemd_maintenance','docker_inference','docker_ai','docker_model_lifecycle','kubernetes_node'));
ALTER TABLE managed_nodes DROP CONSTRAINT managed_nodes_execution_profile_check;
ALTER TABLE managed_nodes ADD CONSTRAINT managed_nodes_execution_profile_check CHECK(execution_profile IN ('read_only','systemd_components','systemd_maintenance','docker_inference','docker_ai','docker_model_lifecycle','kubernetes_node'));
ALTER TABLE managed_nodes DROP CONSTRAINT managed_nodes_capabilities_check;
ALTER TABLE managed_nodes ADD CONSTRAINT managed_nodes_capabilities_check CHECK(
 (execution_profile='read_only' AND capabilities='["collect_facts.v1"]'::jsonb) OR
 (execution_profile='systemd_components' AND capabilities='["collect_facts.v1","node_exporter.install.v1","node_exporter.start.v1","node_exporter.stop.v1","node_exporter.remove.v1","node_exporter.observe.v1"]'::jsonb) OR
 (execution_profile='systemd_maintenance' AND capabilities='["collect_facts.v1","node_exporter.install.v1","node_exporter.start.v1","node_exporter.stop.v1","node_exporter.remove.v1","node_exporter.observe.v1","node_exporter.configure.v1","node_exporter.restart.v1","node_exporter.upgrade.v1","node_exporter.rollback.v1","node_exporter.logs.v1"]'::jsonb) OR
 (execution_profile='docker_inference' AND capabilities='["collect_facts.v1","inference.deploy.v1","inference.stop.v1","inference.remove.v1","inference.observe.v1","inference.verify.v1"]'::jsonb) OR
 (execution_profile='docker_ai' AND capabilities='["collect_facts.v1","inference.deploy.v1","inference.stop.v1","inference.remove.v1","inference.observe.v1","inference.verify.v1","training.run.v1"]'::jsonb) OR
 (execution_profile='docker_model_lifecycle' AND capabilities='["collect_facts.v1","inference.deploy.v1","inference.stop.v1","inference.remove.v1","inference.observe.v1","inference.verify.v1","training.run.v1","model.prepare.v1","inference.derived.v1"]'::jsonb) OR
 (execution_profile='kubernetes_node' AND capabilities='["collect_facts.v1","kubernetes.join.v1","kubernetes.token.v1","kubernetes.observe.v1"]'::jsonb));
ALTER TABLE node_tasks DROP CONSTRAINT node_tasks_operation_check;
ALTER TABLE node_tasks ADD CONSTRAINT node_tasks_operation_check CHECK(operation IN ('collect_facts.v1','node_exporter.install.v1','node_exporter.start.v1','node_exporter.stop.v1','node_exporter.remove.v1','node_exporter.observe.v1','node_exporter.configure.v1','node_exporter.restart.v1','node_exporter.upgrade.v1','node_exporter.rollback.v1','node_exporter.logs.v1','inference.deploy.v1','inference.stop.v1','inference.remove.v1','inference.observe.v1','inference.verify.v1','training.run.v1','model.prepare.v1','inference.derived.v1','kubernetes.join.v1','kubernetes.token.v1','kubernetes.observe.v1'));

-- The address a node's agent enrolled from. A Kubernetes node advertises it in its cluster,
-- and the agent refuses to join when no interface of the machine holds it.
ALTER TABLE managed_nodes ADD COLUMN enrolled_address inet;

CREATE TABLE cell_clusters (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL REFERENCES tenants(id),
 site_id varchar(32) NOT NULL DEFAULT local_site_id(),
 cell_id varchar(32) NOT NULL DEFAULT local_cell_id() REFERENCES cells(id),
 name varchar(100) NOT NULL CHECK (length(btrim(name))>0),
 distribution_version varchar(32) NOT NULL,
 -- The SHA-256 of the server CA, which join tokens pin. Known once the server is ready.
 ca_sha256 char(64) CHECK (ca_sha256 ~ '^[0-9a-f]{64}$'),
 -- The bootstrap token workers join with, sealed with a key derived from the control
 -- secret and bound to this cluster. It expires on the server by itself.
 join_token_sealed bytea,
 join_token_expires_at timestamptz,
 -- The server's token task while it runs, so only one is asked for at a time.
 token_task_id uuid,
 created_by uuid NOT NULL REFERENCES users(id),
 idempotency_key uuid NOT NULL,
 request_fingerprint bytea NOT NULL CHECK (octet_length(request_fingerprint)=32),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE (tenant_id,id),
 UNIQUE (tenant_id,idempotency_key),
 CHECK ((join_token_sealed IS NULL)=(join_token_expires_at IS NULL)),
 CHECK (join_token_sealed IS NULL OR ca_sha256 IS NOT NULL)
);
-- A cell has one cluster.
CREATE UNIQUE INDEX cell_clusters_cell_idx ON cell_clusters(cell_id);
CREATE INDEX cell_clusters_list_idx ON cell_clusters(tenant_id,created_at DESC,id DESC);

-- A node of a cluster. A node belongs to at most one cluster, and a cluster has one server
-- until ARC-115. The address is fixed when the member is added: a server whose address
-- changes after its first start loses its etcd membership.
CREATE TABLE cell_cluster_members (
 cluster_id uuid NOT NULL,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 node_id uuid NOT NULL,
 site_id varchar(32) NOT NULL DEFAULT local_site_id(),
 cell_id varchar(32) NOT NULL DEFAULT local_cell_id(),
 role varchar(16) NOT NULL CHECK (role IN ('server','worker')),
 node_address inet NOT NULL,
 state varchar(16) NOT NULL CHECK (state IN ('pending','joining','ready','not_ready','failed')),
 error_code varchar(64),
 -- The node task working for this member, while it runs. Finished tasks move to the
 -- history, so this keeps no foreign key.
 task_id uuid,
 -- Who asked for the member's latest join: the creator, or whoever retried it.
 requested_by uuid NOT NULL REFERENCES users(id),
 join_attempts integer NOT NULL DEFAULT 0 CHECK (join_attempts>=0),
 observed_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (cluster_id,node_id),
 FOREIGN KEY (tenant_id,cluster_id) REFERENCES cell_clusters(tenant_id,id) ON DELETE CASCADE,
 FOREIGN KEY (tenant_id,workspace_id,node_id) REFERENCES managed_nodes(tenant_id,workspace_id,id),
 CHECK ((state='failed')=(error_code IS NOT NULL)),
 CHECK (state<>'joining' OR task_id IS NOT NULL)
);
CREATE UNIQUE INDEX cell_cluster_members_node_idx ON cell_cluster_members(node_id);
CREATE UNIQUE INDEX cell_cluster_members_server_idx ON cell_cluster_members(cluster_id) WHERE role='server';
CREATE UNIQUE INDEX cell_cluster_members_task_idx ON cell_cluster_members(task_id) WHERE task_id IS NOT NULL;
CREATE INDEX cell_cluster_members_workspace_fk ON cell_cluster_members(tenant_id,workspace_id,node_id);
-- The reconciler visits only clusters that still have work.
CREATE INDEX cell_cluster_members_open_idx ON cell_cluster_members(cluster_id) WHERE state IN ('pending','joining');

INSERT INTO schema_migrations(version,checksum) VALUES(56,'28cdc94a221bcd33757bb272e33dbc9cc0fb924bbadb75f8eefe4e8415dde629');
COMMIT;
