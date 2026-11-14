-- Cumulative M14 after the immutable19-entry account prefix.
ALTER TABLE workflows DROP CONSTRAINT workflow_schema1_draft;
ALTER TABLE workflow_versions DROP CONSTRAINT workflow_schema1_version;
ALTER TABLE workflow_runs DROP CONSTRAINT workflow_schema1_run;
-- M14 immutable signed builtin tool provenance and current Workspace grants.
CREATE TABLE plugin_versions (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL REFERENCES tenants(id),
 plugin_id varchar(100) NOT NULL,
 release_version varchar(32) NOT NULL,
 version_major integer NOT NULL CHECK(version_major=1),
 version_minor integer NOT NULL CHECK(version_minor BETWEEN 0 AND 9999),
 version_patch integer NOT NULL CHECK(version_patch BETWEEN 0 AND 9999),
 abi varchar(100) NOT NULL,
 executor varchar(100) NOT NULL,
 capsule bytea NOT NULL CHECK(octet_length(capsule) BETWEEN 1 AND 1048576),
 capsule_sha256 varchar(64) NOT NULL CHECK(capsule_sha256~'^[a-f0-9]{64}$'),
 manifest jsonb NOT NULL CHECK(jsonb_typeof(manifest)='object' AND octet_length(manifest::text)<=16384),
 manifest_sha256 varchar(64) NOT NULL CHECK(manifest_sha256~'^[a-f0-9]{64}$'),
 key_id varchar(64) NOT NULL CHECK(key_id~'^[a-f0-9]{64}$'),
 state varchar(16) NOT NULL CHECK(state IN ('imported','installed','uninstalled')),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9007199254740991),
 imported_by uuid NOT NULL REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 installed_at timestamptz,
 uninstalled_at timestamptz,
 UNIQUE(tenant_id,id),
 UNIQUE(tenant_id,plugin_id,release_version),
 UNIQUE(tenant_id,capsule_sha256),
 CHECK(state<>'installed' OR installed_at IS NOT NULL),
 CHECK(release_version=version_major::text||'.'||version_minor::text||'.'||version_patch::text)
);
CREATE INDEX plugin_version_page_idx ON plugin_versions(tenant_id,created_at DESC,id DESC);
CREATE FUNCTION plugin_immutable_provenance() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'PLUGIN_PROVENANCE_IMMUTABLE'; END IF;
 IF ROW(NEW.id,NEW.tenant_id,NEW.plugin_id,NEW.release_version,NEW.version_major,NEW.version_minor,NEW.version_patch,NEW.abi,NEW.executor,NEW.capsule,NEW.capsule_sha256,NEW.manifest,NEW.manifest_sha256,NEW.key_id,NEW.imported_by,NEW.created_at)
 IS DISTINCT FROM ROW(OLD.id,OLD.tenant_id,OLD.plugin_id,OLD.release_version,OLD.version_major,OLD.version_minor,OLD.version_patch,OLD.abi,OLD.executor,OLD.capsule,OLD.capsule_sha256,OLD.manifest,OLD.manifest_sha256,OLD.key_id,OLD.imported_by,OLD.created_at) THEN
  RAISE EXCEPTION 'PLUGIN_PROVENANCE_IMMUTABLE';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER plugin_provenance_immutable BEFORE UPDATE OR DELETE ON plugin_versions FOR EACH ROW EXECUTE FUNCTION plugin_immutable_provenance();
CREATE TABLE plugin_grants (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 plugin_version_id uuid NOT NULL,
 enabled boolean NOT NULL DEFAULT true,
 authority_id uuid NOT NULL,
 revision bigint NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9007199254740991),
 granted_by uuid NOT NULL REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,workspace_id,plugin_version_id),
 UNIQUE(tenant_id,workspace_id,id,plugin_version_id),
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id),
 FOREIGN KEY(tenant_id,plugin_version_id) REFERENCES plugin_versions(tenant_id,id)
);
CREATE INDEX plugin_grant_page_idx ON plugin_grants(tenant_id,workspace_id,created_at DESC,id DESC);
CREATE TABLE plugin_mutation_intents (
 tenant_id uuid NOT NULL REFERENCES tenants(id),
 actor_id uuid NOT NULL REFERENCES users(id),
 idempotency_key uuid NOT NULL,
 purpose varchar(100) NOT NULL,
 request_hash bytea NOT NULL CHECK(octet_length(request_hash)=32),
 entity_id uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(tenant_id,actor_id,idempotency_key)
);
CREATE INDEX plugin_intent_retention_idx ON plugin_mutation_intents(tenant_id,created_at,idempotency_key);
CREATE TABLE workflow_plugin_references (
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 workflow_id uuid NOT NULL,
 workflow_version_id uuid NOT NULL,
 plugin_version_id uuid NOT NULL,
 grant_id uuid NOT NULL,
 publication_grant_authority uuid NOT NULL,
 PRIMARY KEY(workflow_version_id,plugin_version_id),
 FOREIGN KEY(tenant_id,workspace_id,workflow_id,workflow_version_id) REFERENCES workflow_versions(tenant_id,workspace_id,workflow_id,id),
 FOREIGN KEY(tenant_id,workspace_id,grant_id,plugin_version_id) REFERENCES plugin_grants(tenant_id,workspace_id,id,plugin_version_id)
);
CREATE TRIGGER workflow_plugin_reference_immutable BEFORE UPDATE OR DELETE ON workflow_plugin_references FOR EACH ROW EXECUTE FUNCTION workflow_immutable_publication();
ALTER TABLE workflow_runs ADD COLUMN result_kind varchar(16) NOT NULL DEFAULT 'answer' CHECK(result_kind IN ('answer','model_list'));
ALTER TABLE workflow_runs ADD COLUMN tool_result jsonb CHECK(tool_result IS NULL OR jsonb_typeof(tool_result)='object' AND octet_length(tool_result::text)<=1048576);
ALTER TABLE workflow_runs ADD COLUMN tool_proof jsonb CHECK(tool_proof IS NULL OR jsonb_typeof(tool_proof)='object' AND octet_length(tool_proof::text)<=65536);
-- Replace exactly the three M13 answer-specific checks, independent of generated
-- constraint names. An unexpected input schema aborts rather than widening it.
DO $$ DECLARE item record; found integer:=0;
BEGIN
 FOR item IN SELECT conname FROM pg_constraint WHERE conrelid='workflow_runs'::regclass AND contype='c' AND (SELECT attnum FROM pg_attribute WHERE attrelid='workflow_runs'::regclass AND attname='answer')=ANY(conkey) LOOP
  EXECUTE format('ALTER TABLE workflow_runs DROP CONSTRAINT %I',item.conname); found:=found+1;
 END LOOP;
 IF found<>3 THEN RAISE EXCEPTION 'PLUGIN_WORKFLOW_INPUT_SCHEMA_UNEXPECTED'; END IF;
END $$;
ALTER TABLE workflow_runs ADD CONSTRAINT workflow_answer_bytes CHECK(answer IS NULL OR octet_length(answer)<=1048576);
ALTER TABLE workflow_runs ADD CONSTRAINT workflow_non_success_result_absent CHECK(state='succeeded' OR (answer IS NULL AND tool_result IS NULL AND tool_proof IS NULL));
ALTER TABLE workflow_runs ADD CONSTRAINT workflow_success_result_required CHECK(state<>'succeeded' OR (error_code IS NULL AND ((result_kind='answer' AND answer IS NOT NULL AND evidence IS NOT NULL AND alias_snapshot IS NOT NULL AND usage IS NOT NULL AND tool_result IS NULL AND tool_proof IS NULL) OR (result_kind='model_list' AND answer IS NULL AND evidence IS NULL AND alias_snapshot IS NULL AND usage IS NULL AND tool_result IS NOT NULL AND tool_proof IS NOT NULL))));
ALTER TABLE workflow_runs ADD CONSTRAINT workflow_result_graph_kind CHECK((result_kind='answer' AND graph_snapshot->>'schema_version'='1') OR (result_kind='model_list' AND graph_snapshot->>'schema_version'='2'));
ALTER TABLE workflow_node_traces DROP CONSTRAINT workflow_node_traces_kind_check;
ALTER TABLE workflow_node_traces ADD CONSTRAINT workflow_node_traces_kind_check CHECK(kind IN ('start','retrieval','answer','end','tool'));
CREATE OR REPLACE FUNCTION workflow_run_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.workflow_id,NEW.owner_id,NEW.conversation_id,NEW.turn_number,NEW.version_id,NEW.draft_revision,NEW.execution_authority,NEW.membership_authority,NEW.requested_audience,NEW.graph_snapshot,NEW.graph_sha256,NEW.question,NEW.deadline_at,NEW.created_at,NEW.result_kind)
 IS DISTINCT FROM ROW(OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.workflow_id,OLD.owner_id,OLD.conversation_id,OLD.turn_number,OLD.version_id,OLD.draft_revision,OLD.execution_authority,OLD.membership_authority,OLD.requested_audience,OLD.graph_snapshot,OLD.graph_sha256,OLD.question,OLD.deadline_at,OLD.created_at,OLD.result_kind) THEN
  RAISE EXCEPTION 'WORKFLOW_RUN_IDENTITY_IMMUTABLE';
 END IF;
 RETURN NEW;
END $$;

ALTER TABLE workflows ADD CONSTRAINT workflow_supported_draft CHECK(draft_graph->>'schema_version' IN ('1','2'));
ALTER TABLE workflow_versions ADD CONSTRAINT workflow_supported_version CHECK(graph->>'schema_version' IN ('1','2'));
