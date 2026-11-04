-- M12 initial TXT/384-dimensional encoder path. Migrations1-11 remain frozen.
-- The operator installs the approved database-wide extension before product migration.
DO $$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace WHERE e.extname='vector' AND e.extversion='0.8.7' AND n.nspname='public') THEN
  RAISE EXCEPTION 'KNOWLEDGE_APPROVED_VECTOR_EXTENSION_REQUIRED';
 END IF;
 IF EXISTS (SELECT 1 FROM pg_namespace n CROSS JOIN LATERAL aclexplode(COALESCE(n.nspacl,acldefault('n',n.nspowner))) a WHERE n.nspname='public' AND a.grantee=0 AND a.privilege_type='CREATE') THEN
  RAISE EXCEPTION 'KNOWLEDGE_TRUSTED_VECTOR_SCHEMA_REQUIRED';
 END IF;
END $$;

CREATE TABLE knowledge_bases (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 name varchar(100) NOT NULL CHECK(length(name)>0),
 name_key varchar(200) NOT NULL CHECK(length(name_key)>0),
 enabled boolean NOT NULL DEFAULT true,
 revision bigint NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9007199254740991),
 active_generation_id uuid NOT NULL,
 building_generation_id uuid,
 created_by uuid NOT NULL REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 creation_key uuid NOT NULL,
 creation_fingerprint bytea NOT NULL CHECK(octet_length(creation_fingerprint)=32),
 UNIQUE(tenant_id,workspace_id,id),
 UNIQUE(tenant_id,workspace_id,created_by,creation_key),
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id),
 CHECK(building_generation_id IS NULL OR building_generation_id<>active_generation_id)
);
CREATE UNIQUE INDEX knowledge_base_name_idx ON knowledge_bases(tenant_id,workspace_id,name_key);
CREATE INDEX knowledge_base_page_idx ON knowledge_bases(tenant_id,workspace_id,created_at DESC,id DESC);

CREATE TABLE knowledge_generations (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 base_id uuid NOT NULL,
 catalog_sha256 varchar(64) NOT NULL CHECK(catalog_sha256 ~ '^[a-f0-9]{64}$'),
 dimension integer NOT NULL CHECK(dimension=384),
 chunk_tokens integer NOT NULL CHECK(chunk_tokens BETWEEN 8 AND 254),
 overlap_tokens integer NOT NULL CHECK(overlap_tokens>=0 AND overlap_tokens<chunk_tokens),
 state varchar(12) NOT NULL CHECK(state IN ('building','active','failed','retired')),
 requested_by uuid NOT NULL REFERENCES users(id),
 requested_audience varchar(8) NOT NULL CHECK(requested_audience IN ('portal','admin')),
 requested_epoch bigint NOT NULL CHECK(requested_epoch>=0),
 membership_authority uuid,
 expected_documents integer NOT NULL CHECK(expected_documents BETWEEN 0 AND 1000),
 error_code varchar(80) CHECK(error_code IS NULL OR error_code ~ '^[A-Z][A-Z0-9_]{1,79}$'),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 activated_at timestamptz,
 finished_at timestamptz,
 UNIQUE(tenant_id,workspace_id,base_id,id),
 FOREIGN KEY(tenant_id,workspace_id,base_id) REFERENCES knowledge_bases(tenant_id,workspace_id,id),
 CHECK(requested_audience='admin' OR membership_authority IS NOT NULL),
 CHECK(state<>'active' OR activated_at IS NOT NULL)
);
CREATE UNIQUE INDEX knowledge_one_active_generation_idx ON knowledge_generations(tenant_id,workspace_id,base_id) WHERE state='active';
CREATE UNIQUE INDEX knowledge_one_building_generation_idx ON knowledge_generations(tenant_id,workspace_id,base_id) WHERE state='building';
ALTER TABLE knowledge_bases ADD CONSTRAINT knowledge_active_generation_scope FOREIGN KEY(tenant_id,workspace_id,id,active_generation_id) REFERENCES knowledge_generations(tenant_id,workspace_id,base_id,id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE knowledge_bases ADD CONSTRAINT knowledge_building_generation_scope FOREIGN KEY(tenant_id,workspace_id,id,building_generation_id) REFERENCES knowledge_generations(tenant_id,workspace_id,base_id,id) DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE knowledge_documents (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 base_id uuid NOT NULL,
 name varchar(255) NOT NULL CHECK(length(name)>0),
 name_key varchar(600) NOT NULL CHECK(length(name_key)>0),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9007199254740991),
 state varchar(12) NOT NULL CHECK(state IN ('pending','running','ready','failed','deleted')),
 visible_source_id uuid,
 pending_source_id uuid,
 current_attempt_id uuid,
 created_by uuid NOT NULL REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 deleted_at timestamptz,
 delete_reason varchar(500),
 UNIQUE(tenant_id,workspace_id,base_id,id),
 FOREIGN KEY(tenant_id,workspace_id,base_id) REFERENCES knowledge_bases(tenant_id,workspace_id,id),
 CHECK((state='deleted' AND deleted_at IS NOT NULL AND length(delete_reason)>0) OR(state<>'deleted' AND deleted_at IS NULL AND delete_reason IS NULL)),
 CHECK(state<>'ready' OR visible_source_id IS NOT NULL)
);
CREATE UNIQUE INDEX knowledge_document_name_idx ON knowledge_documents(tenant_id,workspace_id,base_id,name_key) WHERE deleted_at IS NULL;
CREATE INDEX knowledge_document_page_idx ON knowledge_documents(tenant_id,workspace_id,base_id,created_at DESC,id DESC);

CREATE TABLE knowledge_source_versions (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 base_id uuid NOT NULL,
 document_id uuid NOT NULL,
 source_version bigint NOT NULL CHECK(source_version BETWEEN 1 AND 9007199254740991),
 content bytea NOT NULL CHECK(octet_length(content) BETWEEN 1 AND 2097152 AND length(pg_catalog.convert_from(content,'UTF8'))>0),
 size_bytes integer GENERATED ALWAYS AS (octet_length(content)) STORED,
 sha256 varchar(64) NOT NULL CHECK(sha256 ~ '^[a-f0-9]{64}$' AND sha256=pg_catalog.encode(pg_catalog.sha256(content),'hex')),
 created_by uuid NOT NULL REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 superseded_at timestamptz CHECK(superseded_at IS NULL OR superseded_at>=created_at),
 UNIQUE(tenant_id,workspace_id,base_id,document_id,id),
 UNIQUE(document_id,source_version),
 FOREIGN KEY(tenant_id,workspace_id,base_id,document_id) REFERENCES knowledge_documents(tenant_id,workspace_id,base_id,id)
);
ALTER TABLE knowledge_documents ADD CONSTRAINT knowledge_visible_source_scope FOREIGN KEY(tenant_id,workspace_id,base_id,id,visible_source_id) REFERENCES knowledge_source_versions(tenant_id,workspace_id,base_id,document_id,id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE knowledge_documents ADD CONSTRAINT knowledge_pending_source_scope FOREIGN KEY(tenant_id,workspace_id,base_id,id,pending_source_id) REFERENCES knowledge_source_versions(tenant_id,workspace_id,base_id,document_id,id) DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE knowledge_attempts (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 base_id uuid NOT NULL,
 document_id uuid NOT NULL,
 source_id uuid NOT NULL,
 generation_id uuid NOT NULL,
 kind varchar(8) NOT NULL CHECK(kind IN ('ingest','rebuild')),
 attempt_number integer NOT NULL CHECK(attempt_number BETWEEN 1 AND 10),
 expected_document_revision bigint NOT NULL CHECK(expected_document_revision BETWEEN 1 AND 9007199254740991),
 requested_by uuid NOT NULL REFERENCES users(id),
 requested_audience varchar(8) NOT NULL CHECK(requested_audience IN ('portal','admin')),
 requested_epoch bigint NOT NULL CHECK(requested_epoch>=0),
 membership_authority uuid,
 state varchar(12) NOT NULL CHECK(state IN ('pending','running','ready','failed','cancelled')),
 lease_hash bytea CHECK(lease_hash IS NULL OR octet_length(lease_hash)=32),
 lease_expires_at timestamptz,
 chunk_count integer NOT NULL DEFAULT 0 CHECK(chunk_count BETWEEN 0 AND 4096),
 error_code varchar(80) CHECK(error_code IS NULL OR error_code ~ '^[A-Z][A-Z0-9_]{1,79}$'),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 started_at timestamptz,
 finished_at timestamptz,
 UNIQUE(tenant_id,workspace_id,base_id,document_id,generation_id,source_id,id),
 UNIQUE(tenant_id,workspace_id,base_id,document_id,id),
 UNIQUE(generation_id,document_id,source_id,attempt_number),
 FOREIGN KEY(tenant_id,workspace_id,base_id,document_id,source_id) REFERENCES knowledge_source_versions(tenant_id,workspace_id,base_id,document_id,id),
 FOREIGN KEY(tenant_id,workspace_id,base_id,generation_id) REFERENCES knowledge_generations(tenant_id,workspace_id,base_id,id),
 CHECK(requested_audience='admin' OR membership_authority IS NOT NULL),
 CHECK((state='running' AND lease_hash IS NOT NULL AND lease_expires_at IS NOT NULL) OR(state<>'running' AND lease_hash IS NULL AND lease_expires_at IS NULL)),
 CHECK((state IN ('failed','cancelled') AND error_code IS NOT NULL) OR(state NOT IN ('failed','cancelled') AND error_code IS NULL)),
 CHECK((state IN ('ready','failed','cancelled') AND finished_at IS NOT NULL) OR(state IN ('pending','running') AND finished_at IS NULL))
);
CREATE INDEX knowledge_pending_attempt_idx ON knowledge_attempts(state,created_at,id) WHERE state IN ('pending','running');
CREATE UNIQUE INDEX knowledge_one_ready_attempt_idx ON knowledge_attempts(generation_id,document_id,source_id) WHERE state='ready';
CREATE UNIQUE INDEX knowledge_one_live_attempt_idx ON knowledge_attempts(generation_id,document_id,source_id) WHERE state IN ('pending','running');
ALTER TABLE knowledge_documents ADD CONSTRAINT knowledge_current_attempt_scope FOREIGN KEY(tenant_id,workspace_id,base_id,id,current_attempt_id) REFERENCES knowledge_attempts(tenant_id,workspace_id,base_id,document_id,id) DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE knowledge_chunks (
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 base_id uuid NOT NULL,
 document_id uuid NOT NULL,
 generation_id uuid NOT NULL,
 source_id uuid NOT NULL,
 attempt_id uuid NOT NULL,
 ordinal integer NOT NULL CHECK(ordinal BETWEEN 0 AND 4095),
 start_byte integer NOT NULL CHECK(start_byte BETWEEN 0 AND 2097151),
 end_byte integer NOT NULL CHECK(end_byte>start_byte AND end_byte<=2097152 AND end_byte-start_byte<=65536),
 token_count integer NOT NULL CHECK(token_count BETWEEN 1 AND 254),
 embedding public.vector(384) NOT NULL CHECK(abs(public.vector_norm(embedding)-1)<0.001),
 PRIMARY KEY(attempt_id,ordinal),
 FOREIGN KEY(tenant_id,workspace_id,base_id,document_id,generation_id,source_id,attempt_id) REFERENCES knowledge_attempts(tenant_id,workspace_id,base_id,document_id,generation_id,source_id,id)
);
CREATE INDEX knowledge_scoped_exact_search_idx ON knowledge_chunks(tenant_id,workspace_id,base_id,generation_id,document_id,source_id);

CREATE TABLE knowledge_mutation_intents (
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 actor_id uuid NOT NULL REFERENCES users(id),
 idempotency_key uuid NOT NULL,
 purpose varchar(24) NOT NULL CHECK(purpose IN ('create_base','upload_source','replace_source','retry_document','rebuild')),
 request_hash bytea NOT NULL CHECK(octet_length(request_hash)=32),
 entity_id uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(tenant_id,workspace_id,actor_id,idempotency_key),
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id)
);

CREATE FUNCTION knowledge_immutable_source() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'KNOWLEDGE_SOURCE_IMMUTABLE' USING ERRCODE='23514'; END $$;
CREATE FUNCTION knowledge_source_retirement() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.superseded_at IS NOT NULL OR NEW.superseded_at IS NULL OR ROW(NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.base_id,NEW.document_id,NEW.source_version,NEW.content,NEW.sha256,NEW.created_by,NEW.created_at) IS DISTINCT FROM ROW(OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.base_id,OLD.document_id,OLD.source_version,OLD.content,OLD.sha256,OLD.created_by,OLD.created_at) THEN
  RAISE EXCEPTION 'KNOWLEDGE_SOURCE_IMMUTABLE' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER knowledge_source_immutable BEFORE UPDATE ON knowledge_source_versions FOR EACH ROW EXECUTE FUNCTION knowledge_source_retirement();
CREATE TRIGGER knowledge_chunk_immutable BEFORE UPDATE ON knowledge_chunks FOR EACH ROW EXECUTE FUNCTION knowledge_immutable_source();
CREATE FUNCTION knowledge_generation_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.base_id,NEW.catalog_sha256,NEW.dimension,NEW.chunk_tokens,NEW.overlap_tokens,NEW.requested_by,NEW.requested_audience,NEW.requested_epoch,NEW.membership_authority,NEW.expected_documents,NEW.created_at) IS DISTINCT FROM ROW(OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.base_id,OLD.catalog_sha256,OLD.dimension,OLD.chunk_tokens,OLD.overlap_tokens,OLD.requested_by,OLD.requested_audience,OLD.requested_epoch,OLD.membership_authority,OLD.expected_documents,OLD.created_at) THEN
  RAISE EXCEPTION 'KNOWLEDGE_GENERATION_IMMUTABLE' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER knowledge_generation_immutable BEFORE UPDATE ON knowledge_generations FOR EACH ROW EXECUTE FUNCTION knowledge_generation_identity();

CREATE FUNCTION knowledge_attempt_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.base_id,NEW.document_id,NEW.source_id,NEW.generation_id,NEW.kind,NEW.attempt_number,NEW.expected_document_revision,NEW.requested_by,NEW.requested_audience,NEW.requested_epoch,NEW.membership_authority,NEW.created_at) IS DISTINCT FROM ROW(OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.base_id,OLD.document_id,OLD.source_id,OLD.generation_id,OLD.kind,OLD.attempt_number,OLD.expected_document_revision,OLD.requested_by,OLD.requested_audience,OLD.requested_epoch,OLD.membership_authority,OLD.created_at) THEN
  RAISE EXCEPTION 'KNOWLEDGE_ATTEMPT_IMMUTABLE' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER knowledge_attempt_immutable BEFORE UPDATE ON knowledge_attempts FOR EACH ROW EXECUTE FUNCTION knowledge_attempt_identity();
