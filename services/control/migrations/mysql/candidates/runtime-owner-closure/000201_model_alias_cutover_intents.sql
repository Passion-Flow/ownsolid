CREATE TABLE model_alias_cutover_intents (
    tenant_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    workspace_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    actor_user_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    idempotency_key CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    alias_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    result_revision BIGINT NOT NULL,
    fingerprint VARBINARY(32) NOT NULL CHECK(OCTET_LENGTH(fingerprint)=32),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY(tenant_id,workspace_id,actor_user_id,idempotency_key),
    CONSTRAINT alias_intent_actor_fk FOREIGN KEY(actor_user_id) REFERENCES users(id),
    CONSTRAINT alias_intent_target_scope_fk FOREIGN KEY(tenant_id,workspace_id,alias_id,result_revision) REFERENCES model_alias_target_versions(tenant_id,workspace_id,alias_id,revision),
    CONSTRAINT model_alias_cutover_intents_tenant_id_canonical CHECK(tenant_id IS NULL OR REGEXP_LIKE(tenant_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
    CONSTRAINT model_alias_cutover_intents_workspace_id_canonical CHECK(workspace_id IS NULL OR REGEXP_LIKE(workspace_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
    CONSTRAINT model_alias_cutover_intents_actor_user_id_canonical CHECK(actor_user_id IS NULL OR REGEXP_LIKE(actor_user_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
    CONSTRAINT model_alias_cutover_intents_idempotency_key_canonical CHECK(idempotency_key IS NULL OR REGEXP_LIKE(idempotency_key,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
    CONSTRAINT model_alias_cutover_intents_alias_id_canonical CHECK(alias_id IS NULL OR REGEXP_LIKE(alias_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
