-- Private artifact ingress; formal schema follows AWS000022.
CREATE TABLE artifact_objects (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, workspace_id uuid NOT NULL,
 created_by uuid NOT NULL REFERENCES users(id), requested_audience varchar(8) NOT NULL CHECK(requested_audience IN ('admin','portal')),
 requested_epoch bigint NOT NULL CHECK(requested_epoch>0), membership_authority uuid,
 execution_epoch bigint NOT NULL CHECK(execution_epoch>0),execution_membership uuid,
 creation_key uuid NOT NULL, fingerprint bytea NOT NULL CHECK(octet_length(fingerprint)=32),
 store_id uuid NOT NULL, name varchar(200) NOT NULL, purpose varchar(24) NOT NULL CHECK(purpose IN ('model_bundle','dataset_source')),
 expected_bytes bigint NOT NULL CHECK(expected_bytes BETWEEN 1 AND 5497558138880), sha256 char(64) NOT NULL CHECK(sha256~'^[a-f0-9]{64}$'),
 part_bytes bigint NOT NULL CHECK(part_bytes BETWEEN 5242880 AND 5368709120),
 state varchar(16) NOT NULL CHECK(state IN ('creating','uploading','completing','ready','deleting','deleted','aborting','aborted')),
 remote_upload_id varchar(4096), remote_etag varchar(1024),
 operation_id uuid, operation_kind varchar(16), operation_phase varchar(16), operation_expires_at timestamptz,operation_authority jsonb,
 error_code varchar(64), revision bigint NOT NULL DEFAULT 1 CHECK(revision>0),
 upload_expires_at timestamptz NOT NULL, expires_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,workspace_id,id), UNIQUE(tenant_id,workspace_id,created_by,creation_key),
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id),
 CHECK((operation_id IS NULL)=(operation_kind IS NULL)),CHECK((operation_id IS NULL)=(operation_phase IS NULL)),
 CHECK(operation_phase IS NULL OR operation_phase IN ('prepared','sent')),
 CHECK(operation_kind IS NULL OR operation_kind IN ('create','part','complete','delete','abort','ingest')),
 CHECK(state<>'ready' OR remote_etag IS NOT NULL),CHECK((operation_id IS NULL)=(operation_expires_at IS NULL)),
 CHECK(expires_at>=upload_expires_at),CHECK((expected_bytes+part_bytes-1)/part_bytes<=10000),
 CHECK((operation_id IS NULL)=(operation_authority IS NULL))
);
CREATE TABLE artifact_object_parts (
 tenant_id uuid NOT NULL,workspace_id uuid NOT NULL,object_id uuid NOT NULL,number integer NOT NULL CHECK(number BETWEEN 1 AND 10000),
 bytes bigint NOT NULL CHECK(bytes BETWEEN 1 AND 5368709120),sha256 char(64) NOT NULL CHECK(sha256~'^[a-f0-9]{64}$'),
 etag varchar(1024),state varchar(16) NOT NULL CHECK(state IN ('intent','uploaded')),stage_id uuid NOT NULL,
 PRIMARY KEY(object_id,number),FOREIGN KEY(tenant_id,workspace_id,object_id) REFERENCES artifact_objects(tenant_id,workspace_id,id),
 CHECK((state='uploaded')=(etag IS NOT NULL))
);
CREATE TABLE artifact_object_grants (
 id uuid PRIMARY KEY,tenant_id uuid NOT NULL,workspace_id uuid NOT NULL,object_id uuid NOT NULL,
 recipient_user_id uuid NOT NULL REFERENCES users(id),created_by uuid NOT NULL REFERENCES users(id),
 creator_epoch bigint NOT NULL,creator_membership uuid,recipient_epoch bigint NOT NULL,recipient_membership uuid NOT NULL,
 action varchar(24) NOT NULL CHECK(action IN ('read','ingest_model','ingest_dataset')),
 creation_key uuid NOT NULL,fingerprint bytea NOT NULL CHECK(octet_length(fingerprint)=32),
 not_before timestamptz NOT NULL,expires_at timestamptz NOT NULL,revoked_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,workspace_id,object_id,id),UNIQUE(tenant_id,workspace_id,object_id,created_by,creation_key),FOREIGN KEY(tenant_id,workspace_id,object_id) REFERENCES artifact_objects(tenant_id,workspace_id,id),
 CHECK(expires_at>not_before)
);
CREATE TABLE artifact_object_ingestions (
 id uuid PRIMARY KEY,tenant_id uuid NOT NULL,workspace_id uuid NOT NULL,object_id uuid NOT NULL,
 created_by uuid NOT NULL REFERENCES users(id),creation_key uuid NOT NULL,fingerprint bytea NOT NULL CHECK(octet_length(fingerprint)=32),
 kind varchar(16) NOT NULL CHECK(kind IN ('model','dataset')),model_import_id uuid,dataset_draft_id uuid,target_dataset_id uuid,
 state varchar(16) NOT NULL CHECK(state IN ('intent','running','submitted','failed')),
 source_verified_sha256 char(64),source_verified_at timestamptz,
 input jsonb NOT NULL CHECK(jsonb_typeof(input)='object' AND octet_length(input::text)<=16384),
 receipt jsonb CHECK(receipt IS NULL OR jsonb_typeof(receipt)='object' AND octet_length(receipt::text)<=16384),
 error_code varchar(64),created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,workspace_id,object_id,created_by,creation_key),
 FOREIGN KEY(tenant_id,workspace_id,object_id) REFERENCES artifact_objects(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,target_dataset_id) REFERENCES datasets(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,model_import_id) REFERENCES model_imports(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,target_dataset_id,dataset_draft_id) REFERENCES dataset_drafts(tenant_id,workspace_id,dataset_id,id),
 CHECK((kind='model' AND dataset_draft_id IS NULL AND target_dataset_id IS NULL) OR (kind='dataset' AND model_import_id IS NULL AND target_dataset_id IS NOT NULL)),
 CHECK(state<>'submitted' OR (kind='model' AND model_import_id IS NOT NULL) OR (kind='dataset' AND dataset_draft_id IS NOT NULL))
);
CREATE INDEX artifact_objects_scope_page ON artifact_objects(tenant_id,workspace_id,created_at DESC,id DESC);
CREATE INDEX artifact_grant_recipient ON artifact_object_grants(tenant_id,workspace_id,recipient_user_id,object_id);

-- Inactive candidate; one complete PostgreSQL statement.
CREATE FUNCTION m22_artifact_objects_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.state<>'creating' OR NEW.remote_upload_id IS NOT NULL OR NEW.remote_etag IS NOT NULL OR NEW.operation_id IS NULL OR NEW.operation_kind<>'create' OR NEW.operation_phase<>'prepared' OR NEW.operation_authority IS NULL OR NEW.execution_epoch<>NEW.requested_epoch OR NEW.execution_membership IS DISTINCT FROM NEW.membership_authority THEN RAISE EXCEPTION 'OBJECT_STORED_PROVENANCE_INVALID' USING ERRCODE='23514'; END IF;
 ELSE
  IF (OLD.remote_upload_id IS NOT NULL AND (NEW.remote_upload_id IS DISTINCT FROM OLD.remote_upload_id)) OR (OLD.remote_etag IS NOT NULL AND (NEW.remote_etag IS DISTINCT FROM OLD.remote_etag)) OR (OLD.state IN ('deleted','aborted') AND NEW.operation_id IS NOT NULL) OR ROW(NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.created_by,NEW.requested_audience,NEW.requested_epoch,NEW.membership_authority,NEW.creation_key,NEW.fingerprint,NEW.store_id,NEW.name,NEW.purpose,NEW.expected_bytes,NEW.sha256,NEW.part_bytes,NEW.created_at) IS DISTINCT FROM ROW(OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.created_by,OLD.requested_audience,OLD.requested_epoch,OLD.membership_authority,OLD.creation_key,OLD.fingerprint,OLD.store_id,OLD.name,OLD.purpose,OLD.expected_bytes,OLD.sha256,OLD.part_bytes,OLD.created_at) OR NEW.upload_expires_at>OLD.upload_expires_at OR NEW.expires_at>OLD.expires_at OR NEW.revision<OLD.revision OR NOT (NEW.state=OLD.state OR (OLD.state='creating' AND NEW.state IN ('uploading','aborting')) OR (OLD.state='uploading' AND NEW.state IN ('completing','aborting')) OR (OLD.state='completing' AND NEW.state IN ('ready','aborting')) OR (OLD.state='ready' AND NEW.state='deleting') OR (OLD.state='deleting' AND NEW.state IN ('deleted','aborting')) OR (OLD.state='aborting' AND NEW.state='aborted')) THEN RAISE EXCEPTION 'OBJECT_STORED_PROVENANCE_INVALID' USING ERRCODE='23514'; END IF;
 END IF;
 RETURN NEW;
END;
$$;

-- Install only after the matching candidate function/table.
CREATE TRIGGER m22_artifact_objects_guard BEFORE INSERT OR UPDATE ON artifact_objects FOR EACH ROW EXECUTE FUNCTION m22_artifact_objects_guard();

-- Inactive candidate; one complete PostgreSQL statement.
CREATE FUNCTION m22_artifact_object_parts_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.state<>'intent' OR NEW.etag IS NOT NULL OR NOT EXISTS(SELECT 1 FROM artifact_objects o WHERE o.tenant_id=NEW.tenant_id AND o.workspace_id=NEW.workspace_id AND o.id=NEW.object_id AND o.state='uploading' AND o.remote_upload_id IS NOT NULL AND o.operation_id IS NULL AND (NEW.number-1)*o.part_bytes<o.expected_bytes AND NEW.bytes=LEAST(o.part_bytes,o.expected_bytes-(NEW.number-1)*o.part_bytes)) THEN RAISE EXCEPTION 'OBJECT_STORED_PROVENANCE_INVALID' USING ERRCODE='23514'; END IF;
 ELSE
  IF ROW(NEW.tenant_id,NEW.workspace_id,NEW.object_id,NEW.number,NEW.bytes,NEW.sha256,NEW.stage_id) IS DISTINCT FROM ROW(OLD.tenant_id,OLD.workspace_id,OLD.object_id,OLD.number,OLD.bytes,OLD.sha256,OLD.stage_id) OR OLD.state='uploaded' AND (NEW.state<>'uploaded' OR NEW.etag IS DISTINCT FROM OLD.etag) THEN RAISE EXCEPTION 'OBJECT_STORED_PROVENANCE_INVALID' USING ERRCODE='23514'; END IF;
 END IF;
 RETURN NEW;
END;
$$;

-- Install only after the matching candidate function/table.
CREATE TRIGGER m22_artifact_object_parts_guard BEFORE INSERT OR UPDATE ON artifact_object_parts FOR EACH ROW EXECUTE FUNCTION m22_artifact_object_parts_guard();

-- Inactive candidate; one complete PostgreSQL statement.
CREATE FUNCTION m22_artifact_object_grants_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.revoked_at IS NOT NULL OR NOT EXISTS(SELECT 1 FROM artifact_objects o WHERE o.tenant_id=NEW.tenant_id AND o.workspace_id=NEW.workspace_id AND o.id=NEW.object_id AND o.state='ready' AND NEW.expires_at<=o.expires_at) THEN RAISE EXCEPTION 'OBJECT_STORED_PROVENANCE_INVALID' USING ERRCODE='23514'; END IF;
 ELSE
  IF ROW(NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.object_id,NEW.recipient_user_id,NEW.created_by,NEW.creator_epoch,NEW.creator_membership,NEW.recipient_epoch,NEW.recipient_membership,NEW.action,NEW.creation_key,NEW.fingerprint,NEW.not_before,NEW.expires_at,NEW.created_at) IS DISTINCT FROM ROW(OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.object_id,OLD.recipient_user_id,OLD.created_by,OLD.creator_epoch,OLD.creator_membership,OLD.recipient_epoch,OLD.recipient_membership,OLD.action,OLD.creation_key,OLD.fingerprint,OLD.not_before,OLD.expires_at,OLD.created_at) OR OLD.revoked_at IS NOT NULL AND NEW.revoked_at IS DISTINCT FROM OLD.revoked_at THEN RAISE EXCEPTION 'OBJECT_STORED_PROVENANCE_INVALID' USING ERRCODE='23514'; END IF;
 END IF;
 RETURN NEW;
END;
$$;

-- Install only after the matching candidate function/table.
CREATE TRIGGER m22_artifact_object_grants_guard BEFORE INSERT OR UPDATE ON artifact_object_grants FOR EACH ROW EXECUTE FUNCTION m22_artifact_object_grants_guard();

-- Inactive candidate; one complete PostgreSQL statement.
CREATE FUNCTION m22_artifact_object_ingestions_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.state<>'intent' OR NEW.model_import_id IS NOT NULL OR NEW.dataset_draft_id IS NOT NULL OR NEW.source_verified_sha256 IS NOT NULL OR NEW.source_verified_at IS NOT NULL OR NEW.receipt IS NOT NULL OR NEW.error_code IS NOT NULL THEN RAISE EXCEPTION 'OBJECT_STORED_PROVENANCE_INVALID' USING ERRCODE='23514'; END IF;
 ELSE
  IF ROW(NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.object_id,NEW.created_by,NEW.creation_key,NEW.fingerprint,NEW.kind,NEW.target_dataset_id,NEW.input,NEW.created_at) IS DISTINCT FROM ROW(OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.object_id,OLD.created_by,OLD.creation_key,OLD.fingerprint,OLD.kind,OLD.target_dataset_id,OLD.input,OLD.created_at) OR (OLD.model_import_id IS NOT NULL AND NEW.model_import_id IS DISTINCT FROM OLD.model_import_id) OR (OLD.dataset_draft_id IS NOT NULL AND NEW.dataset_draft_id IS DISTINCT FROM OLD.dataset_draft_id) OR (OLD.source_verified_sha256 IS NOT NULL AND (NEW.source_verified_sha256 IS DISTINCT FROM OLD.source_verified_sha256 OR NEW.source_verified_at IS DISTINCT FROM OLD.source_verified_at)) OR ((OLD.state IN ('submitted','failed')) AND NEW IS DISTINCT FROM OLD) OR NOT (NEW.state=OLD.state OR (OLD.state='intent' AND NEW.state IN ('running','failed')) OR (OLD.state='running' AND NEW.state IN ('submitted','failed'))) THEN RAISE EXCEPTION 'OBJECT_STORED_PROVENANCE_INVALID' USING ERRCODE='23514'; END IF;
 END IF;

  IF NEW.model_import_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM model_imports m JOIN artifact_objects o ON o.tenant_id=m.tenant_id AND o.workspace_id=m.workspace_id WHERE o.id=NEW.object_id AND m.id=NEW.model_import_id AND m.tenant_id=NEW.tenant_id AND m.workspace_id=NEW.workspace_id AND m.requested_by=NEW.created_by AND m.expected_bytes=o.expected_bytes) THEN RAISE EXCEPTION 'OBJECT_STORED_PROVENANCE_INVALID' USING ERRCODE='23514'; END IF;
  IF NEW.dataset_draft_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM dataset_drafts d JOIN artifact_objects o ON o.tenant_id=d.tenant_id AND o.workspace_id=d.workspace_id WHERE o.id=NEW.object_id AND d.id=NEW.dataset_draft_id AND d.tenant_id=NEW.tenant_id AND d.workspace_id=NEW.workspace_id AND d.dataset_id=NEW.target_dataset_id AND d.created_by=NEW.created_by AND d.expected_bytes=o.expected_bytes AND d.source_sha256=o.sha256) THEN RAISE EXCEPTION 'OBJECT_STORED_PROVENANCE_INVALID' USING ERRCODE='23514'; END IF;
  IF ((NEW.source_verified_sha256 IS NULL)<>(NEW.source_verified_at IS NULL)) OR (NEW.source_verified_sha256 IS NOT NULL AND NOT EXISTS(SELECT 1 FROM artifact_objects o WHERE o.tenant_id=NEW.tenant_id AND o.workspace_id=NEW.workspace_id AND o.id=NEW.object_id AND o.sha256=NEW.source_verified_sha256)) OR (NEW.state='submitted' AND (NEW.receipt IS NULL OR NEW.source_verified_sha256 IS NULL OR NEW.source_verified_at IS NULL)) THEN RAISE EXCEPTION 'OBJECT_STORED_PROVENANCE_INVALID' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END;
$$;

-- Install only after the matching candidate function/table.
CREATE TRIGGER m22_artifact_object_ingestions_guard BEFORE INSERT OR UPDATE ON artifact_object_ingestions FOR EACH ROW EXECUTE FUNCTION m22_artifact_object_ingestions_guard();
