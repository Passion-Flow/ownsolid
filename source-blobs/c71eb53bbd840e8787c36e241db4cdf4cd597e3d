\set ON_ERROR_STOP on
-- Metadata derivation only: this never substitutes for production Migrate.
BEGIN;
SET LOCAL search_path = :"schema";
-- Exact source: services/control/migrations/postgresql/000059_model_alias_description.up.sql
ALTER TABLE model_aliases
 ADD COLUMN description varchar(500) NOT NULL DEFAULT '',
 ADD COLUMN description_revision bigint NOT NULL DEFAULT 1
  CHECK(description_revision BETWEEN 1 AND 9007199254740991),
 ADD CONSTRAINT model_alias_description_scope_key UNIQUE(tenant_id,workspace_id,id,site_id,cell_id);
CREATE TABLE model_alias_description_intents (
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 site_id varchar(32) NOT NULL DEFAULT local_site_id(),
 cell_id varchar(32) NOT NULL DEFAULT local_cell_id(),
 actor_user_id uuid NOT NULL REFERENCES users(id),
 idempotency_key uuid NOT NULL,
 alias_id uuid NOT NULL,
 result_revision bigint NOT NULL CHECK(result_revision BETWEEN 1 AND 9007199254740991),
 fingerprint bytea NOT NULL CHECK(octet_length(fingerprint)=32),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(tenant_id,workspace_id,actor_user_id,idempotency_key),
 CONSTRAINT model_alias_description_scope_fk
  FOREIGN KEY(tenant_id,workspace_id,alias_id,site_id,cell_id)
  REFERENCES model_aliases(tenant_id,workspace_id,id,site_id,cell_id)
);
CREATE TRIGGER model_alias_description_intent_immutable
 BEFORE UPDATE OR DELETE ON model_alias_description_intents
 FOR EACH ROW EXECUTE FUNCTION protect_alias_cutover_record();

INSERT INTO schema_migrations(version,checksum) VALUES(59,'399432d5de8be8daad68db91d74706e52456ff3d0560048e33d5a10088ab72ab');
-- Exact source: services/control/migrations/postgresql/000060_dataset_draft_import_receipts.up.sql
ALTER TABLE dataset_drafts
    ADD CONSTRAINT dataset_draft_import_scope_key
    UNIQUE (tenant_id, workspace_id, dataset_id, id, site_id, cell_id);

CREATE TABLE dataset_draft_import_candidates (
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    dataset_id uuid NOT NULL,
    draft_id uuid NOT NULL,
    site_id varchar(32) NOT NULL DEFAULT local_site_id(),
    cell_id varchar(32) NOT NULL DEFAULT local_cell_id(),
    source_format text NOT NULL CHECK (source_format IN ('csv', 'parquet')),
    report jsonb NOT NULL CHECK (jsonb_typeof(report) = 'object'),
    PRIMARY KEY (tenant_id, workspace_id, dataset_id, draft_id),
    CONSTRAINT dataset_draft_import_candidate_scope_fk
        FOREIGN KEY (tenant_id, workspace_id, dataset_id, draft_id, site_id, cell_id)
        REFERENCES dataset_drafts (tenant_id, workspace_id, dataset_id, id, site_id, cell_id)
);

INSERT INTO schema_migrations(version,checksum) VALUES(60,'d7e8eb61bd8afa4c1a3a1363d092685d13c55ca35d6965257457247336066868');
-- Exact source: services/control/migrations/postgresql/000061_model_families.up.sql
-- Retain explicit model family identities (MOD-103).
CREATE TABLE model_families (
 id uuid PRIMARY KEY CHECK (id<>'00000000-0000-0000-0000-000000000000'),
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 initial_version_id uuid NOT NULL UNIQUE,
 CHECK (id<>initial_version_id),
 UNIQUE (tenant_id,workspace_id,id),
 FOREIGN KEY (tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id),
 CONSTRAINT model_family_initial_version_fk FOREIGN KEY (tenant_id,workspace_id,initial_version_id)
  REFERENCES model_versions(tenant_id,workspace_id,id) DEFERRABLE INITIALLY DEFERRED
);
CREATE TABLE model_family_members (
 version_id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 family_id uuid NOT NULL,
 anchor_version_id uuid,
 CHECK (anchor_version_id IS NULL OR anchor_version_id<>version_id),
 UNIQUE (tenant_id,workspace_id,family_id,version_id),
 CONSTRAINT model_family_member_version_fk FOREIGN KEY (tenant_id,workspace_id,version_id)
  REFERENCES model_versions(tenant_id,workspace_id,id) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY (tenant_id,workspace_id,family_id) REFERENCES model_families(tenant_id,workspace_id,id),
 CONSTRAINT model_family_member_anchor_fk FOREIGN KEY (tenant_id,workspace_id,family_id,anchor_version_id)
  REFERENCES model_family_members(tenant_id,workspace_id,family_id,version_id) DEFERRABLE INITIALLY DEFERRED
);

ALTER TABLE model_families ADD CONSTRAINT model_family_initial_member_fk
 FOREIGN KEY (tenant_id,workspace_id,id,initial_version_id)
 REFERENCES model_family_members(tenant_id,workspace_id,family_id,version_id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE model_versions ADD CONSTRAINT model_version_family_member_fk
 FOREIGN KEY (id) REFERENCES model_family_members(version_id) DEFERRABLE INITIALLY DEFERRED NOT VALID;

-- No established grouping exists in the predecessor. Persist one mapping per ID.
INSERT INTO model_families(id,tenant_id,workspace_id,initial_version_id)
 SELECT gen_random_uuid(),tenant_id,workspace_id,id FROM model_versions;
INSERT INTO model_family_members(version_id,tenant_id,workspace_id,family_id)
 SELECT initial_version_id,tenant_id,workspace_id,id FROM model_families;
-- Finish only this domain's pending checks before further catalog alterations.
SET CONSTRAINTS model_family_initial_version_fk,model_family_member_version_fk,
 model_family_member_anchor_fk,model_family_initial_member_fk,model_version_family_member_fk IMMEDIATE;
ALTER TABLE model_versions VALIDATE CONSTRAINT model_version_family_member_fk;
SET CONSTRAINTS model_family_initial_version_fk,model_family_member_version_fk,
 model_family_member_anchor_fk,model_family_initial_member_fk,model_version_family_member_fk DEFERRED;

CREATE INDEX model_family_scope_idx ON model_family_members(tenant_id,workspace_id,family_id,version_id);
CREATE INDEX model_version_history_order_idx ON model_versions(tenant_id,workspace_id,created_at DESC,id DESC);

CREATE FUNCTION model_family_before_version() RETURNS trigger
 LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE member_tenant uuid; member_workspace uuid; family uuid;
BEGIN
 -- Both explicit selection and default publication acquire this before the version row.
 PERFORM pg_advisory_xact_lock(hashtextextended('ownsolid.models.family:'||NEW.id::text,0));
 EXECUTE format('SELECT tenant_id,workspace_id FROM %I.model_family_members WHERE version_id=$1',TG_TABLE_SCHEMA)
  INTO member_tenant,member_workspace USING NEW.id;
 IF member_tenant IS NULL THEN
  family:=gen_random_uuid();
  EXECUTE format('INSERT INTO %I.model_families(id,tenant_id,workspace_id,initial_version_id) VALUES($1,$2,$3,$4)',TG_TABLE_SCHEMA)
   USING family,NEW.tenant_id,NEW.workspace_id,NEW.id;
  EXECUTE format('INSERT INTO %I.model_family_members(version_id,tenant_id,workspace_id,family_id) VALUES($1,$2,$3,$4)',TG_TABLE_SCHEMA)
   USING NEW.id,NEW.tenant_id,NEW.workspace_id,family;
 ELSIF (member_tenant,member_workspace) IS DISTINCT FROM (NEW.tenant_id,NEW.workspace_id) THEN
  RAISE EXCEPTION 'MODEL_FAMILY_SCOPE_CONFLICT' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER model_family_version_insert BEFORE INSERT ON model_versions
 FOR EACH ROW EXECUTE FUNCTION model_family_before_version();

CREATE FUNCTION model_family_member_insert() RETURNS trigger
 LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE initial uuid;
BEGIN
 EXECUTE format('SELECT initial_version_id FROM %I.model_families WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3',TG_TABLE_SCHEMA)
  INTO initial USING NEW.tenant_id,NEW.workspace_id,NEW.family_id;
 IF initial IS NULL OR (NEW.version_id=initial) IS DISTINCT FROM (NEW.anchor_version_id IS NULL) THEN
  RAISE EXCEPTION 'MODEL_FAMILY_ANCHOR_REQUIRED' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER model_family_member_anchor BEFORE INSERT ON model_family_members
 FOR EACH ROW EXECUTE FUNCTION model_family_member_insert();

CREATE FUNCTION model_family_immutable() RETURNS trigger
 LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 RAISE EXCEPTION 'MODEL_FAMILY_IMMUTABLE' USING ERRCODE='23514';
END $$;
CREATE TRIGGER model_families_immutable BEFORE UPDATE OR DELETE ON model_families
 FOR EACH ROW EXECUTE FUNCTION model_family_immutable();
CREATE TRIGGER model_family_members_immutable BEFORE UPDATE OR DELETE ON model_family_members
 FOR EACH ROW EXECUTE FUNCTION model_family_immutable();
CREATE TRIGGER model_families_no_truncate BEFORE TRUNCATE ON model_families
 FOR EACH STATEMENT EXECUTE FUNCTION model_family_immutable();
CREATE TRIGGER model_family_members_no_truncate BEFORE TRUNCATE ON model_family_members
 FOR EACH STATEMENT EXECUTE FUNCTION model_family_immutable();

-- Platform cell ownership.
ALTER TABLE model_versions
 ADD CONSTRAINT model_family_version_cell_key
 UNIQUE (tenant_id,workspace_id,id,site_id,cell_id);
ALTER TABLE model_families
 ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(),
 ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id(),
 ADD CONSTRAINT model_family_cell_key UNIQUE (tenant_id,workspace_id,id,site_id,cell_id),
 ADD CONSTRAINT model_family_initial_version_cell_fk
  FOREIGN KEY (tenant_id,workspace_id,initial_version_id,site_id,cell_id)
  REFERENCES model_versions(tenant_id,workspace_id,id,site_id,cell_id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE model_family_members
 ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(),
 ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id(),
 ADD CONSTRAINT model_family_member_version_cell_fk
  FOREIGN KEY (tenant_id,workspace_id,version_id,site_id,cell_id)
  REFERENCES model_versions(tenant_id,workspace_id,id,site_id,cell_id) DEFERRABLE INITIALLY DEFERRED,
 ADD CONSTRAINT model_family_member_family_cell_fk
  FOREIGN KEY (tenant_id,workspace_id,family_id,site_id,cell_id)
  REFERENCES model_families(tenant_id,workspace_id,id,site_id,cell_id);

INSERT INTO schema_migrations(version,checksum) VALUES(61,'6d5d106d6a9349eadf032c279e1895b64395660d269f030930bebee7ad2be531');
COMMIT;
