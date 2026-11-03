CREATE TABLE sso_mutation_intents (
    tenant_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    actor_user_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    idempotency_key CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    operation VARCHAR(24) NOT NULL CHECK(operation IN('create_connector','update_connector','unlink_binding')),
    target_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    fingerprint VARBINARY(32) NOT NULL CHECK(OCTET_LENGTH(fingerprint)=32),
    result JSON NOT NULL CHECK(JSON_TYPE(result)='OBJECT'),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY(tenant_id,actor_user_id,idempotency_key),
    FOREIGN KEY(tenant_id) REFERENCES tenants(id),
    FOREIGN KEY(actor_user_id) REFERENCES users(id),
    CHECK(REGEXP_LIKE(idempotency_key,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
    CHECK(REGEXP_LIKE(target_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
