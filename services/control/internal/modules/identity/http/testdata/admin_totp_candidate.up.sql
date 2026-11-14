-- Unnumbered IAM-105 migration for global identity. Tenant scope is mandatory.
CREATE TABLE admin_totp_factors (
    user_id uuid PRIMARY KEY REFERENCES users(id),
    tenant_id uuid NOT NULL REFERENCES tenants(id),
    id uuid NOT NULL UNIQUE,
    key_id varchar(64) COLLATE "C" NOT NULL CHECK(key_id ~ '^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$'),
    nonce bytea NOT NULL CHECK(octet_length(nonce)=12),
    seed bytea NOT NULL CHECK(octet_length(seed)=36),
    enrollment_session_id uuid,
    enrollment_expires_at timestamptz,
    enabled_at timestamptz,
    last_step bigint NOT NULL DEFAULT -1 CHECK(last_step>=-1),
    UNIQUE(user_id,tenant_id,id),
    CHECK ((enabled_at IS NULL AND enrollment_session_id IS NOT NULL AND enrollment_expires_at IS NOT NULL AND last_step=-1)
        OR (enabled_at IS NOT NULL AND enrollment_session_id IS NULL AND enrollment_expires_at IS NULL AND last_step>=0))
);
CREATE INDEX admin_totp_enrollment_expiry_idx ON admin_totp_factors(enrollment_expires_at) WHERE enabled_at IS NULL;
CREATE TABLE admin_totp_challenges (
    challenge_hash bytea PRIMARY KEY CHECK(octet_length(challenge_hash)=32),
    browser_hash bytea NOT NULL CHECK(octet_length(browser_hash)=32),
    previous_hash bytea NOT NULL CHECK(octet_length(previous_hash)=32),
    user_id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    factor_id uuid NOT NULL,
    auth_epoch bigint NOT NULL CHECK(auth_epoch>0),
    credential_version bigint NOT NULL CHECK(credential_version>0),
    expires_at timestamptz NOT NULL,
    UNIQUE(user_id,browser_hash),
    FOREIGN KEY(user_id,tenant_id,factor_id) REFERENCES admin_totp_factors(user_id,tenant_id,id) ON DELETE CASCADE
);
CREATE INDEX admin_totp_challenge_expiry_idx ON admin_totp_challenges(expires_at);
CREATE TABLE admin_totp_recovery_codes (
    digest bytea PRIMARY KEY CHECK(octet_length(digest)=32),
    user_id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    factor_id uuid NOT NULL,
    expires_at timestamptz NOT NULL,
    FOREIGN KEY(user_id,tenant_id,factor_id) REFERENCES admin_totp_factors(user_id,tenant_id,id) ON DELETE CASCADE
);
CREATE INDEX admin_totp_recovery_factor_idx ON admin_totp_recovery_codes(user_id,tenant_id,factor_id);
CREATE INDEX admin_totp_recovery_expiry_idx ON admin_totp_recovery_codes(expires_at);
