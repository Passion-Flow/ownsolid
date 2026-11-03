CREATE TABLE workflow_mutation_intents (
 tenant_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 workspace_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 actor_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 idempotency_key CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 purpose VARCHAR(64) NOT NULL,
 request_hash VARBINARY(32) NOT NULL CHECK(OCTET_LENGTH(request_hash)=32),
 entity_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
 PRIMARY KEY(tenant_id,workspace_id,actor_id,idempotency_key),
 FOREIGN KEY(actor_id) REFERENCES users(id),
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id),
 CONSTRAINT workflow_mutation_intents_tenant_id_canonical CHECK(REGEXP_LIKE(tenant_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
 CONSTRAINT workflow_mutation_intents_workspace_id_canonical CHECK(REGEXP_LIKE(workspace_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
 CONSTRAINT workflow_mutation_intents_actor_id_canonical CHECK(REGEXP_LIKE(actor_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
 CONSTRAINT workflow_mutation_intents_idempotency_key_canonical CHECK(REGEXP_LIKE(idempotency_key,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
 CONSTRAINT workflow_mutation_intents_entity_id_canonical CHECK(REGEXP_LIKE(entity_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
