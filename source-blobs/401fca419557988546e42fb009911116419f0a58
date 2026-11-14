ALTER TABLE model_aliases
 ADD COLUMN description varchar(500) NOT NULL DEFAULT '',
 ADD COLUMN description_revision bigint NOT NULL DEFAULT 1
  CHECK(description_revision BETWEEN 1 AND 9007199254740991);
CREATE TABLE model_alias_description_intents (
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 actor_user_id uuid NOT NULL REFERENCES users(id),
 idempotency_key uuid NOT NULL,
 alias_id uuid NOT NULL,
 result_revision bigint NOT NULL CHECK(result_revision BETWEEN 1 AND 9007199254740991),
 fingerprint bytea NOT NULL CHECK(octet_length(fingerprint)=32),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(tenant_id,workspace_id,actor_user_id,idempotency_key),
 FOREIGN KEY(tenant_id,workspace_id,alias_id) REFERENCES model_aliases(tenant_id,workspace_id,id)
);
CREATE TRIGGER model_alias_description_intent_immutable
 BEFORE UPDATE OR DELETE ON model_alias_description_intents
 FOR EACH ROW EXECUTE FUNCTION protect_alias_cutover_record();
