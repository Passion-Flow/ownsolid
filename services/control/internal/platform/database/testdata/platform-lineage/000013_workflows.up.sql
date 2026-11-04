-- M13 bounded workflow drafts, immutable publications and private durable runs.
CREATE TABLE workflows (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 name varchar(120) NOT NULL CHECK(length(btrim(name))>0),
 name_key varchar(240) NOT NULL CHECK(length(btrim(name_key))>0),
 enabled boolean NOT NULL DEFAULT true,
 execution_authority uuid NOT NULL,
 revision bigint NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9007199254740991),
 draft_graph jsonb NOT NULL CHECK(jsonb_typeof(draft_graph)='object' AND octet_length(draft_graph::text)<=1048576),
 draft_sha256 varchar(64) NOT NULL CHECK(draft_sha256~'^[a-f0-9]{64}$'),
 current_version_id uuid,
 created_by uuid NOT NULL REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id)
);
CREATE UNIQUE INDEX workflow_name_idx ON workflows(tenant_id,workspace_id,name_key);
CREATE INDEX workflow_page_idx ON workflows(tenant_id,workspace_id,created_at DESC,id DESC);
CREATE TABLE workflow_versions (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 workflow_id uuid NOT NULL,
 version_number integer NOT NULL CHECK(version_number>0),
 draft_revision bigint NOT NULL CHECK(draft_revision BETWEEN 1 AND 9007199254740991),
 graph jsonb NOT NULL CHECK(jsonb_typeof(graph)='object' AND octet_length(graph::text)<=1048576),
 graph_sha256 varchar(64) NOT NULL CHECK(graph_sha256~'^[a-f0-9]{64}$'),
 published_by uuid NOT NULL REFERENCES users(id),
 published_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,workspace_id,workflow_id,id),
 UNIQUE(tenant_id,workspace_id,workflow_id,version_number),
 FOREIGN KEY(tenant_id,workspace_id,workflow_id) REFERENCES workflows(tenant_id,workspace_id,id)
);
ALTER TABLE workflows ADD CONSTRAINT workflow_current_version_scope FOREIGN KEY(tenant_id,workspace_id,id,current_version_id) REFERENCES workflow_versions(tenant_id,workspace_id,workflow_id,id);
CREATE FUNCTION workflow_immutable_publication() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'WORKFLOW_VERSION_IMMUTABLE'; END $$;
CREATE TRIGGER workflow_publication_immutable BEFORE UPDATE OR DELETE ON workflow_versions FOR EACH ROW EXECUTE FUNCTION workflow_immutable_publication();
CREATE TABLE workflow_conversations (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 workflow_id uuid NOT NULL,
 version_id uuid NOT NULL,
 owner_id uuid NOT NULL REFERENCES users(id),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9007199254740991),
 turn_count integer NOT NULL DEFAULT 0 CHECK(turn_count BETWEEN 0 AND 10000),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 deleted_at timestamptz,
 UNIQUE(tenant_id,workspace_id,workflow_id,id),
 UNIQUE(tenant_id,workspace_id,workflow_id,id,owner_id,version_id),
 FOREIGN KEY(tenant_id,workspace_id,workflow_id,version_id) REFERENCES workflow_versions(tenant_id,workspace_id,workflow_id,id)
);
CREATE INDEX workflow_conversation_page_idx ON workflow_conversations(tenant_id,workspace_id,owner_id,created_at DESC,id DESC) WHERE deleted_at IS NULL;
CREATE TABLE workflow_runs (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 workflow_id uuid NOT NULL,
 owner_id uuid NOT NULL REFERENCES users(id),
 conversation_id uuid,
 turn_number integer CHECK(turn_number BETWEEN 1 AND 10000),
 version_id uuid,
 draft_revision bigint CHECK(draft_revision BETWEEN 1 AND 9007199254740991),
 execution_authority uuid NOT NULL,
 membership_authority uuid NOT NULL,
 requested_audience varchar(6) NOT NULL CHECK(requested_audience IN ('portal','admin')),
 graph_snapshot jsonb NOT NULL CHECK(jsonb_typeof(graph_snapshot)='object' AND octet_length(graph_snapshot::text)<=1048576),
 graph_sha256 varchar(64) NOT NULL CHECK(graph_sha256~'^[a-f0-9]{64}$'),
 state varchar(16) NOT NULL CHECK(state IN ('pending','running','succeeded','failed','cancelled','interrupted')),
 question text NOT NULL CHECK(octet_length(question) BETWEEN 1 AND 32768),
 answer text CHECK(octet_length(answer)<=1048576),
 evidence jsonb CHECK(evidence IS NULL OR jsonb_typeof(evidence)='object'),
 alias_snapshot jsonb CHECK(alias_snapshot IS NULL OR jsonb_typeof(alias_snapshot)='object'),
 usage jsonb CHECK(usage IS NULL OR jsonb_typeof(usage)='object'),
 error_code varchar(100),
 process_owner uuid,
 lease_id uuid,
 lease_expires_at timestamptz,
 deadline_at timestamptz NOT NULL,
 cancellation_requested_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 started_at timestamptz,
 finished_at timestamptz,
 UNIQUE(tenant_id,workspace_id,workflow_id,id),
 UNIQUE(conversation_id,turn_number),
 FOREIGN KEY(tenant_id,workspace_id,workflow_id) REFERENCES workflows(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,workflow_id,version_id) REFERENCES workflow_versions(tenant_id,workspace_id,workflow_id,id),
 FOREIGN KEY(tenant_id,workspace_id,workflow_id,conversation_id,owner_id,version_id) REFERENCES workflow_conversations(tenant_id,workspace_id,workflow_id,id,owner_id,version_id),
 CHECK((version_id IS NOT NULL AND draft_revision IS NULL AND conversation_id IS NOT NULL AND turn_number IS NOT NULL) OR (version_id IS NULL AND draft_revision IS NOT NULL AND conversation_id IS NULL AND turn_number IS NULL)),
 CHECK((state IN ('pending','running') AND finished_at IS NULL) OR (state NOT IN ('pending','running') AND finished_at IS NOT NULL)),
 CHECK(state='succeeded' OR answer IS NULL),
 CHECK(state<>'succeeded' OR (answer IS NOT NULL AND evidence IS NOT NULL AND alias_snapshot IS NOT NULL AND usage IS NOT NULL AND error_code IS NULL)),
 CHECK((state='running' AND process_owner IS NOT NULL AND lease_id IS NOT NULL AND lease_expires_at IS NOT NULL AND started_at IS NOT NULL) OR (state<>'running' AND process_owner IS NULL AND lease_id IS NULL AND lease_expires_at IS NULL))
);
CREATE UNIQUE INDEX workflow_one_active_turn_idx ON workflow_runs(conversation_id) WHERE conversation_id IS NOT NULL AND state IN ('pending','running');
CREATE INDEX workflow_active_workspace_idx ON workflow_runs(tenant_id,workspace_id,state,lease_expires_at) WHERE state IN ('pending','running');
CREATE INDEX workflow_run_page_idx ON workflow_runs(tenant_id,workspace_id,owner_id,created_at DESC,id DESC);
CREATE TABLE workflow_node_traces (
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 workflow_id uuid NOT NULL,
 run_id uuid NOT NULL,
 node_id uuid NOT NULL,
 kind varchar(16) NOT NULL CHECK(kind IN ('start','retrieval','answer','end')),
 state varchar(16) NOT NULL CHECK(state IN ('not_started','running','succeeded','failed','cancelled','interrupted')),
 result_metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK(jsonb_typeof(result_metadata)='object' AND octet_length(result_metadata::text)<=65536),
 error_code varchar(100),
 started_at timestamptz,
 finished_at timestamptz,
 PRIMARY KEY(run_id,node_id),
 UNIQUE(run_id,kind),
 FOREIGN KEY(tenant_id,workspace_id,workflow_id,run_id) REFERENCES workflow_runs(tenant_id,workspace_id,workflow_id,id) ON DELETE CASCADE,
 CHECK((state='not_started' AND started_at IS NULL AND finished_at IS NULL) OR (state='running' AND started_at IS NOT NULL AND finished_at IS NULL) OR (state IN ('succeeded','failed','cancelled','interrupted') AND finished_at IS NOT NULL))
);
CREATE TABLE workflow_mutation_intents (
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 actor_id uuid NOT NULL REFERENCES users(id),
 idempotency_key uuid NOT NULL,
 purpose varchar(64) NOT NULL,
 request_hash bytea NOT NULL CHECK(octet_length(request_hash)=32),
 entity_id uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(tenant_id,workspace_id,actor_id,idempotency_key),
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id)
);

CREATE FUNCTION workflow_run_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.workflow_id,NEW.owner_id,NEW.conversation_id,NEW.turn_number,NEW.version_id,NEW.draft_revision,NEW.execution_authority,NEW.membership_authority,NEW.requested_audience,NEW.graph_snapshot,NEW.graph_sha256,NEW.question,NEW.deadline_at,NEW.created_at)
 IS DISTINCT FROM ROW(OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.workflow_id,OLD.owner_id,OLD.conversation_id,OLD.turn_number,OLD.version_id,OLD.draft_revision,OLD.execution_authority,OLD.membership_authority,OLD.requested_audience,OLD.graph_snapshot,OLD.graph_sha256,OLD.question,OLD.deadline_at,OLD.created_at) THEN
  RAISE EXCEPTION 'WORKFLOW_RUN_IDENTITY_IMMUTABLE';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER workflow_run_identity_guard BEFORE UPDATE ON workflow_runs FOR EACH ROW EXECUTE FUNCTION workflow_run_identity();
