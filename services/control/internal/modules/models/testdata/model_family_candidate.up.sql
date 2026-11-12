-- Unnumbered MOD-103 candidate. ARC owns migration, role and catalog admission.
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
