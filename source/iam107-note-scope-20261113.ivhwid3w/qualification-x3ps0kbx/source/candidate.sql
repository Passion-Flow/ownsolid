CREATE TABLE iam_role_proto_roles (
 id uuid NOT NULL,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 name varchar(100) NOT NULL CHECK (length(name)>0),
 version bigint NOT NULL DEFAULT 1 CHECK(version BETWEEN 1 AND 9007199254740991),
 active boolean NOT NULL DEFAULT true,
 PRIMARY KEY(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id)
);
CREATE TABLE iam_role_proto_actions (
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 role_id uuid NOT NULL,
 action text NOT NULL CHECK(action IN ('workspace.note.update','workspace.note.delete')),
 PRIMARY KEY(tenant_id,workspace_id,role_id,action),
 FOREIGN KEY(tenant_id,workspace_id,role_id) REFERENCES iam_role_proto_roles(tenant_id,workspace_id,id) ON DELETE CASCADE
);
CREATE TABLE iam_role_proto_grants (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 user_id uuid NOT NULL REFERENCES users(id),
 authority_id uuid NOT NULL REFERENCES workspace_memberships(authority_id) ON DELETE CASCADE,
 role_id uuid NOT NULL,
 note_id uuid REFERENCES workspace_notes(id) ON DELETE CASCADE,
 version bigint NOT NULL DEFAULT 1 CHECK(version BETWEEN 1 AND 9007199254740991),
 active boolean NOT NULL DEFAULT true,
 FOREIGN KEY(tenant_id,workspace_id,role_id) REFERENCES iam_role_proto_roles(tenant_id,workspace_id,id),
 UNIQUE NULLS NOT DISTINCT(tenant_id,workspace_id,user_id,authority_id,role_id,note_id)
);
CREATE INDEX iam_role_proto_subject_idx ON iam_role_proto_grants(tenant_id,workspace_id,user_id,id) WHERE active;
CREATE FUNCTION iam_role_proto_scope_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_TABLE_NAME='iam_role_proto_roles' THEN
  IF TG_OP='UPDATE' AND (NEW.id,NEW.tenant_id,NEW.workspace_id) IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.workspace_id) THEN
   RAISE EXCEPTION 'IAM_ROLE_IDENTITY_IMMUTABLE' USING ERRCODE='23514';
  END IF;
 ELSE
  IF TG_OP='UPDATE' AND (NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.user_id,NEW.authority_id,NEW.note_id) IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.user_id,OLD.authority_id,OLD.note_id) THEN
   RAISE EXCEPTION 'IAM_ROLE_GRANT_SCOPE_IMMUTABLE' USING ERRCODE='23514';
  END IF;
  IF NOT EXISTS(SELECT 1 FROM workspace_memberships m WHERE m.authority_id=NEW.authority_id AND m.tenant_id=NEW.tenant_id AND m.workspace_id=NEW.workspace_id AND m.user_id=NEW.user_id) THEN
   RAISE EXCEPTION 'IAM_ROLE_GRANT_SCOPE_INVALID' USING ERRCODE='23514';
  END IF;
  IF NEW.note_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM workspace_notes n WHERE n.id=NEW.note_id AND n.tenant_id=NEW.tenant_id AND n.workspace_id=NEW.workspace_id AND (NOT NEW.active OR n.deleted_at IS NULL)) THEN
   RAISE EXCEPTION 'IAM_ROLE_RESOURCE_SCOPE_INVALID' USING ERRCODE='23514';
  END IF;
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER iam_role_proto_role_scope BEFORE UPDATE ON iam_role_proto_roles FOR EACH ROW EXECUTE FUNCTION iam_role_proto_scope_guard();
CREATE TRIGGER iam_role_proto_grant_scope BEFORE INSERT OR UPDATE ON iam_role_proto_grants FOR EACH ROW EXECUTE FUNCTION iam_role_proto_scope_guard();
