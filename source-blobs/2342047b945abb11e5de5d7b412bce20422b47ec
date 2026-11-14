\set ON_ERROR_STOP on
-- Metadata derivation only: this never substitutes for production Migrate.
BEGIN;
SET LOCAL search_path = :"schema";
-- Exact source: services/control/migrations/postgresql/000056_cell_clusters.up.sql
-- One Kubernetes cluster per cell, installed by ownsolid on nodes of the kubernetes_node
-- profile (ARC-104, ADR 0051). Control writes the tasks; each node's agent installs RKE2.
ALTER TABLE node_join_tokens DROP CONSTRAINT node_join_tokens_execution_profile_check;
ALTER TABLE node_join_tokens ADD CONSTRAINT node_join_tokens_execution_profile_check CHECK(execution_profile IN ('read_only','systemd_components','systemd_maintenance','docker_inference','docker_ai','docker_model_lifecycle','kubernetes_node'));
ALTER TABLE managed_nodes DROP CONSTRAINT managed_nodes_execution_profile_check;
ALTER TABLE managed_nodes ADD CONSTRAINT managed_nodes_execution_profile_check CHECK(execution_profile IN ('read_only','systemd_components','systemd_maintenance','docker_inference','docker_ai','docker_model_lifecycle','kubernetes_node'));
ALTER TABLE managed_nodes DROP CONSTRAINT managed_nodes_capabilities_check;
ALTER TABLE managed_nodes ADD CONSTRAINT managed_nodes_capabilities_check CHECK(
 (execution_profile='read_only' AND capabilities='["collect_facts.v1"]'::jsonb) OR
 (execution_profile='systemd_components' AND capabilities='["collect_facts.v1","node_exporter.install.v1","node_exporter.start.v1","node_exporter.stop.v1","node_exporter.remove.v1","node_exporter.observe.v1"]'::jsonb) OR
 (execution_profile='systemd_maintenance' AND capabilities='["collect_facts.v1","node_exporter.install.v1","node_exporter.start.v1","node_exporter.stop.v1","node_exporter.remove.v1","node_exporter.observe.v1","node_exporter.configure.v1","node_exporter.restart.v1","node_exporter.upgrade.v1","node_exporter.rollback.v1","node_exporter.logs.v1"]'::jsonb) OR
 (execution_profile='docker_inference' AND capabilities='["collect_facts.v1","inference.deploy.v1","inference.stop.v1","inference.remove.v1","inference.observe.v1","inference.verify.v1"]'::jsonb) OR
 (execution_profile='docker_ai' AND capabilities='["collect_facts.v1","inference.deploy.v1","inference.stop.v1","inference.remove.v1","inference.observe.v1","inference.verify.v1","training.run.v1"]'::jsonb) OR
 (execution_profile='docker_model_lifecycle' AND capabilities='["collect_facts.v1","inference.deploy.v1","inference.stop.v1","inference.remove.v1","inference.observe.v1","inference.verify.v1","training.run.v1","model.prepare.v1","inference.derived.v1"]'::jsonb) OR
 (execution_profile='kubernetes_node' AND capabilities='["collect_facts.v1","kubernetes.join.v1","kubernetes.token.v1","kubernetes.observe.v1"]'::jsonb));
ALTER TABLE node_tasks DROP CONSTRAINT node_tasks_operation_check;
ALTER TABLE node_tasks ADD CONSTRAINT node_tasks_operation_check CHECK(operation IN ('collect_facts.v1','node_exporter.install.v1','node_exporter.start.v1','node_exporter.stop.v1','node_exporter.remove.v1','node_exporter.observe.v1','node_exporter.configure.v1','node_exporter.restart.v1','node_exporter.upgrade.v1','node_exporter.rollback.v1','node_exporter.logs.v1','inference.deploy.v1','inference.stop.v1','inference.remove.v1','inference.observe.v1','inference.verify.v1','training.run.v1','model.prepare.v1','inference.derived.v1','kubernetes.join.v1','kubernetes.token.v1','kubernetes.observe.v1'));

-- The address a node's agent enrolled from. A Kubernetes node advertises it in its cluster,
-- and the agent refuses to join when no interface of the machine holds it.
ALTER TABLE managed_nodes ADD COLUMN enrolled_address inet;

CREATE TABLE cell_clusters (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL REFERENCES tenants(id),
 site_id varchar(32) NOT NULL DEFAULT local_site_id(),
 cell_id varchar(32) NOT NULL DEFAULT local_cell_id() REFERENCES cells(id),
 name varchar(100) NOT NULL CHECK (length(btrim(name))>0),
 distribution_version varchar(32) NOT NULL,
 -- The SHA-256 of the server CA, which join tokens pin. Known once the server is ready.
 ca_sha256 char(64) CHECK (ca_sha256 ~ '^[0-9a-f]{64}$'),
 -- The bootstrap token workers join with, sealed with a key derived from the control
 -- secret and bound to this cluster. It expires on the server by itself.
 join_token_sealed bytea,
 join_token_expires_at timestamptz,
 -- The server's token task while it runs, so only one is asked for at a time.
 token_task_id uuid,
 created_by uuid NOT NULL REFERENCES users(id),
 idempotency_key uuid NOT NULL,
 request_fingerprint bytea NOT NULL CHECK (octet_length(request_fingerprint)=32),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE (tenant_id,id),
 UNIQUE (tenant_id,idempotency_key),
 CHECK ((join_token_sealed IS NULL)=(join_token_expires_at IS NULL)),
 CHECK (join_token_sealed IS NULL OR ca_sha256 IS NOT NULL)
);
-- A cell has one cluster.
CREATE UNIQUE INDEX cell_clusters_cell_idx ON cell_clusters(cell_id);
CREATE INDEX cell_clusters_list_idx ON cell_clusters(tenant_id,created_at DESC,id DESC);

-- A node of a cluster. A node belongs to at most one cluster, and a cluster has one server
-- until ARC-115. The address is fixed when the member is added: a server whose address
-- changes after its first start loses its etcd membership.
CREATE TABLE cell_cluster_members (
 cluster_id uuid NOT NULL,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 node_id uuid NOT NULL,
 site_id varchar(32) NOT NULL DEFAULT local_site_id(),
 cell_id varchar(32) NOT NULL DEFAULT local_cell_id(),
 role varchar(16) NOT NULL CHECK (role IN ('server','worker')),
 node_address inet NOT NULL,
 state varchar(16) NOT NULL CHECK (state IN ('pending','joining','ready','not_ready','failed')),
 error_code varchar(64),
 -- The node task working for this member, while it runs. Finished tasks move to the
 -- history, so this keeps no foreign key.
 task_id uuid,
 -- Who asked for the member's latest join: the creator, or whoever retried it.
 requested_by uuid NOT NULL REFERENCES users(id),
 join_attempts integer NOT NULL DEFAULT 0 CHECK (join_attempts>=0),
 observed_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (cluster_id,node_id),
 FOREIGN KEY (tenant_id,cluster_id) REFERENCES cell_clusters(tenant_id,id) ON DELETE CASCADE,
 FOREIGN KEY (tenant_id,workspace_id,node_id) REFERENCES managed_nodes(tenant_id,workspace_id,id),
 CHECK ((state='failed')=(error_code IS NOT NULL)),
 CHECK (state<>'joining' OR task_id IS NOT NULL)
);
CREATE UNIQUE INDEX cell_cluster_members_node_idx ON cell_cluster_members(node_id);
CREATE UNIQUE INDEX cell_cluster_members_server_idx ON cell_cluster_members(cluster_id) WHERE role='server';
CREATE UNIQUE INDEX cell_cluster_members_task_idx ON cell_cluster_members(task_id) WHERE task_id IS NOT NULL;
CREATE INDEX cell_cluster_members_workspace_fk ON cell_cluster_members(tenant_id,workspace_id,node_id);
-- The reconciler visits only clusters that still have work.
CREATE INDEX cell_cluster_members_open_idx ON cell_cluster_members(cluster_id) WHERE state IN ('pending','joining');

INSERT INTO schema_migrations(version,checksum) VALUES(56,'28cdc94a221bcd33757bb272e33dbc9cc0fb924bbadb75f8eefe4e8415dde629');
-- Exact source: services/control/migrations/postgresql/000057_agent_gateways.up.sql
-- Model gateways the agent runs beside the model on a serving node (GW-114, ADR 0052). The
-- agent generates the credential and key on the node and activates them under a gateway.start
-- task's lease, so an agent-delivered instance has no credential and no origin until then.
-- Manual instances keep the earlier rule: control generated the credential and an
-- administrator copied it. Lists are written as text arrays, not IN on varchar: IN deparses in
-- one form when created and another after pg_dump and pg_restore, so an installation restored
-- from a backup would otherwise end this migration with a catalog no release knows.
-- Inference nodes enrolled at protocol 1.6 also run gateways; earlier enrollments keep their list.
ALTER TABLE managed_nodes DROP CONSTRAINT managed_nodes_capabilities_check;
ALTER TABLE managed_nodes ADD CONSTRAINT managed_nodes_capabilities_check CHECK(
 (execution_profile='read_only' AND capabilities='["collect_facts.v1"]'::jsonb) OR
 (execution_profile='systemd_components' AND capabilities='["collect_facts.v1","node_exporter.install.v1","node_exporter.start.v1","node_exporter.stop.v1","node_exporter.remove.v1","node_exporter.observe.v1"]'::jsonb) OR
 (execution_profile='systemd_maintenance' AND capabilities='["collect_facts.v1","node_exporter.install.v1","node_exporter.start.v1","node_exporter.stop.v1","node_exporter.remove.v1","node_exporter.observe.v1","node_exporter.configure.v1","node_exporter.restart.v1","node_exporter.upgrade.v1","node_exporter.rollback.v1","node_exporter.logs.v1"]'::jsonb) OR
 (execution_profile='docker_inference' AND capabilities='["collect_facts.v1","inference.deploy.v1","inference.stop.v1","inference.remove.v1","inference.observe.v1","inference.verify.v1"]'::jsonb) OR
 (execution_profile='docker_inference' AND capabilities='["collect_facts.v1","inference.deploy.v1","inference.stop.v1","inference.remove.v1","inference.observe.v1","inference.verify.v1","gateway.start.v1","gateway.stop.v1"]'::jsonb) OR
 (execution_profile='docker_ai' AND capabilities='["collect_facts.v1","inference.deploy.v1","inference.stop.v1","inference.remove.v1","inference.observe.v1","inference.verify.v1","training.run.v1"]'::jsonb) OR
 (execution_profile='docker_model_lifecycle' AND capabilities='["collect_facts.v1","inference.deploy.v1","inference.stop.v1","inference.remove.v1","inference.observe.v1","inference.verify.v1","training.run.v1","model.prepare.v1","inference.derived.v1"]'::jsonb) OR
 (execution_profile='kubernetes_node' AND capabilities='["collect_facts.v1","kubernetes.join.v1","kubernetes.token.v1","kubernetes.observe.v1"]'::jsonb));
ALTER TABLE node_tasks DROP CONSTRAINT node_tasks_operation_check;
ALTER TABLE node_tasks ADD CONSTRAINT node_tasks_operation_check CHECK (operation::text = ANY (ARRAY['collect_facts.v1'::text,'node_exporter.install.v1'::text,'node_exporter.start.v1'::text,'node_exporter.stop.v1'::text,'node_exporter.remove.v1'::text,'node_exporter.observe.v1'::text,'node_exporter.configure.v1'::text,'node_exporter.restart.v1'::text,'node_exporter.upgrade.v1'::text,'node_exporter.rollback.v1'::text,'node_exporter.logs.v1'::text,'inference.deploy.v1'::text,'inference.stop.v1'::text,'inference.remove.v1'::text,'inference.observe.v1'::text,'inference.verify.v1'::text,'training.run.v1'::text,'model.prepare.v1'::text,'inference.derived.v1'::text,'kubernetes.join.v1'::text,'kubernetes.token.v1'::text,'kubernetes.observe.v1'::text,'gateway.start.v1'::text,'gateway.stop.v1'::text]));

ALTER TABLE gateway_instances
 ADD COLUMN delivery varchar(16) NOT NULL DEFAULT 'manual' CHECK (delivery::text = ANY (ARRAY['manual'::text,'agent'::text])),
 ADD COLUMN delivery_state varchar(16) CHECK (delivery_state::text = ANY (ARRAY['starting'::text,'running'::text,'failed'::text,'stopping'::text,'stopped'::text])),
 ADD COLUMN delivery_error_code varchar(64) CHECK (delivery_error_code ~ '^GATEWAY_[A-Z_]{1,56}$'),
 ADD COLUMN delivery_task_id uuid,
 ADD COLUMN certificate_not_after timestamptz,
 ALTER COLUMN client_origin DROP NOT NULL;
ALTER TABLE gateway_instances DROP CONSTRAINT gateway_instances_check1;
ALTER TABLE gateway_instances
 ADD CONSTRAINT gateway_instances_credential_check CHECK (credential_hash IS NOT NULL OR authority_invalidated_at IS NOT NULL OR delivery='agent'),
 ADD CONSTRAINT gateway_instances_origin_check CHECK (client_origin IS NOT NULL OR (delivery='agent' AND credential_hash IS NULL)),
 ADD CONSTRAINT gateway_instances_delivery_task_check CHECK ((delivery='agent') = (delivery_state IS NOT NULL AND delivery_task_id IS NOT NULL)),
 ADD CONSTRAINT gateway_instances_activation_check CHECK (delivery='agent' OR certificate_not_after IS NULL),
 ADD CONSTRAINT gateway_instances_delivery_error_check CHECK (delivery_error_code IS NULL OR delivery_state::text = ANY (ARRAY['failed'::text,'running'::text,'stopped'::text]));

INSERT INTO schema_migrations(version,checksum) VALUES(57,'cf611cd40232759e66b658fd04e056c93975f5097fb60147c1f31bd7c455aa3c');
-- Exact source: services/control/migrations/postgresql/000058_audit_chain_head_scope.up.sql
-- Fence event inserts before locking the head, in the same order as appends.
LOCK TABLE audit_events IN SHARE ROW EXCLUSIVE MODE;

ALTER TABLE audit_chain_head
    ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(),
    ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();

-- Keep the V2 hash domain and record serialization unchanged.
CREATE OR REPLACE FUNCTION audit_chain_append_v2() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
DECLARE
    next_position bigint;
    predecessor bytea;
    projection jsonb;
    digest bytea;
    updated integer;
BEGIN
    IF NEW.chain_position IS NOT NULL OR NEW.prev_hash IS NOT NULL OR NEW.hash IS NOT NULL THEN
        RAISE EXCEPTION 'AUDIT_CHAIN_FIELDS_ASSIGNED_BY_DATABASE';
    END IF;
    IF NEW.occurred_at < '0001-01-01T00:00:00Z'::timestamptz
        OR NEW.occurred_at >= '10000-01-01T00:00:00Z'::timestamptz THEN
        RAISE EXCEPTION 'AUDIT_CHAIN_TIMESTAMP_OUT_OF_RANGE';
    END IF;

    -- Only the owner can bind this function as a trigger. Use the schema of
    -- that relation, never the writer's search path, for internal helpers.
    PERFORM set_config('search_path', format('pg_catalog, %I, pg_temp', TG_TABLE_SCHEMA), true);
    SELECT head.chain_position + 1, head.hash INTO next_position, predecessor
        FROM audit_chain_head AS head
        WHERE head.singleton AND head.site_id = NEW.site_id AND head.cell_id = NEW.cell_id
          AND EXISTS (
              SELECT 1 FROM cells AS local
              WHERE local.is_local AND local.site_id = head.site_id AND local.id = head.cell_id
          )
        FOR UPDATE OF head;
    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'AUDIT_CHAIN_CELL_SCOPE_MISMATCH';
    END IF;
    projection := audit_chain_projection_v2(to_jsonb(NEW), next_position, NEW.occurred_at, NEW.identifier_digest);

    SELECT sha256(convert_to('ownsolid.audit.v2', 'UTF8') || decode('00', 'hex') || predecessor
        || string_agg(audit_chain_frame(projection ->> field), ''::bytea ORDER BY ordinal))
    INTO STRICT digest
    FROM unnest(ARRAY[
        'position', 'id', 'occurred_at', 'actor_user_id', 'subject_user_id', 'tenant_id', 'workspace_id',
        'audience', 'action', 'outcome', 'target_type', 'target_id', 'request_id',
        'reason_sha256', 'identifier_digest_sha256', 'metadata_sha256', 'site_id', 'cell_id'
    ]) WITH ORDINALITY AS fields(field, ordinal);

    UPDATE audit_events SET chain_position = next_position, prev_hash = predecessor, hash = digest
        WHERE id = NEW.id AND occurred_at = NEW.occurred_at AND chain_position IS NULL;
    GET DIAGNOSTICS updated = ROW_COUNT;
    IF updated <> 1 THEN
        RAISE EXCEPTION 'AUDIT_CHAIN_WRITE_UNCONFIRMED';
    END IF;
    UPDATE audit_chain_head SET chain_position = next_position, hash = digest,
        scoped_from = coalesce(scoped_from, next_position) WHERE singleton;
    RETURN NULL;
END
$$;

INSERT INTO schema_migrations(version,checksum) VALUES(58,'c4d228212ca9a4107dc5e4a78468f9f7a35dfba818564d406a0a8861cdca1413');
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
