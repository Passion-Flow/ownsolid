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
