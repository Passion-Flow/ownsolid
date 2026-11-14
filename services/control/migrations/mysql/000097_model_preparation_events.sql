CREATE TABLE model_preparation_events (
    tenant_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    workspace_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    preparation_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    attempt_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    sequence bigint NOT NULL CHECK(sequence BETWEEN 1 AND 1024),
    kind VARCHAR(24) NOT NULL CHECK(kind IN ('phase','evaluation_progress')),
    payload JSON NOT NULL CHECK(JSON_TYPE(payload)='OBJECT' AND OCTET_LENGTH(CAST(payload AS CHAR CHARACTER SET utf8mb4))<=16384),
    canonical_sha256 VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL CHECK(REGEXP_LIKE(canonical_sha256,'^[a-f0-9]{64}$','c')),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY(attempt_id,sequence),
    FOREIGN KEY(tenant_id,workspace_id,preparation_id,attempt_id) REFERENCES model_preparation_attempts(tenant_id,workspace_id,preparation_id,id),
    CONSTRAINT model_preparation_events_tenant_id_canonical CHECK(REGEXP_LIKE(tenant_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
    CONSTRAINT model_preparation_events_workspace_id_canonical CHECK(REGEXP_LIKE(workspace_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
    CONSTRAINT model_preparation_events_preparation_id_canonical CHECK(REGEXP_LIKE(preparation_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
    CONSTRAINT model_preparation_events_attempt_id_canonical CHECK(REGEXP_LIKE(attempt_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
