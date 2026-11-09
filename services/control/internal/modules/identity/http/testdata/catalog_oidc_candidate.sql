-- IAM-106 isolated candidate. Catalog-owned registration and production
-- migrations do not use this fixture.
CREATE TABLE catalog_oidc_candidate_clients (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id),
    redirect_uri text NOT NULL,
    secret_digest bytea NOT NULL CHECK(octet_length(secret_digest)=32),
    version bigint NOT NULL DEFAULT 1 CHECK(version>0),
    enabled boolean NOT NULL DEFAULT true,
    UNIQUE (id, tenant_id)
);

CREATE TABLE catalog_oidc_candidate_codes (
    code_hash bytea PRIMARY KEY CHECK(octet_length(code_hash)=32),
    tenant_id uuid NOT NULL,
    client_id uuid NOT NULL,
    client_version bigint NOT NULL CHECK(client_version>0),
    user_id uuid NOT NULL REFERENCES users(id),
    session_id uuid NOT NULL REFERENCES sessions(id),
    auth_epoch bigint NOT NULL CHECK(auth_epoch>0),
    redirect_uri text NOT NULL,
    challenge text NOT NULL CHECK(length(challenge)=43),
    nonce text NOT NULL CHECK(length(nonce)=43),
    expires_at timestamptz NOT NULL,
    used_at timestamptz,
    FOREIGN KEY (client_id, tenant_id) REFERENCES catalog_oidc_candidate_clients(id, tenant_id)
);

CREATE TABLE catalog_oidc_candidate_tokens (
    id uuid PRIMARY KEY,
    code_hash bytea NOT NULL UNIQUE REFERENCES catalog_oidc_candidate_codes(code_hash),
    expires_at timestamptz NOT NULL
);
