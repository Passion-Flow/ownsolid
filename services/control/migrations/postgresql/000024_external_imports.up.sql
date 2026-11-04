-- New account22-derived MVP lineage; immutable legacy assets are unchanged.
-- MongoDB is an external read-only source, never the platform metadata database.
CREATE TABLE mongodb_dataset_imports (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 dataset_id uuid NOT NULL,
 requested_by uuid NOT NULL REFERENCES users(id),
 creation_key uuid NOT NULL,
 intent bytea NOT NULL CHECK(octet_length(intent)=32),
 request jsonb NOT NULL CHECK(jsonb_typeof(request)='object'),
 source_grant jsonb NOT NULL CHECK(jsonb_typeof(source_grant)='object'),
 source_id text NOT NULL,
 state text NOT NULL CHECK(state IN ('reading','exported','imported','failed')),
 attempt_id uuid NOT NULL,
 attempt_count integer NOT NULL CHECK(attempt_count BETWEEN 1 AND 20),
 lease_expires_at timestamptz,
 object_id uuid NOT NULL,
 retained_objects jsonb NOT NULL DEFAULT '[]'::jsonb CHECK(jsonb_typeof(retained_objects)='array'),
 source_sha256 text,
 source_bytes bigint,
 source_rows bigint,
 first_id text,
 last_id text,
 draft_id uuid,
 error_code text,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(tenant_id,workspace_id,dataset_id,requested_by,creation_key),
 FOREIGN KEY(tenant_id,workspace_id,dataset_id) REFERENCES datasets(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,dataset_id,draft_id) REFERENCES dataset_drafts(tenant_id,workspace_id,dataset_id,id),
 CHECK ((source_sha256 IS NULL AND source_bytes IS NULL AND source_rows IS NULL AND first_id IS NULL AND last_id IS NULL) OR
        (source_sha256 IS NOT NULL AND source_bytes IS NOT NULL AND source_rows IS NOT NULL AND first_id IS NOT NULL AND last_id IS NOT NULL AND source_sha256 ~ '^[0-9a-f]{64}$' AND source_bytes>0 AND source_rows>0 AND first_id ~ '^[0-9a-f]{24}$' AND last_id ~ '^[0-9a-f]{24}$' AND first_id<=last_id)),
 CHECK(state NOT IN ('exported','imported') OR source_sha256 IS NOT NULL),
 CHECK(state<>'imported' OR draft_id IS NOT NULL),
 CHECK((state IN ('reading','exported') AND lease_expires_at IS NOT NULL) OR (state IN ('failed','imported') AND lease_expires_at IS NULL))
);
CREATE INDEX mongodb_dataset_import_lease_idx ON mongodb_dataset_imports(tenant_id,lease_expires_at) WHERE lease_expires_at IS NOT NULL;

CREATE TABLE aws_discovered_resources (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 account_id varchar(12) NOT NULL CHECK (account_id ~ '^[0-9]{12}$'),
 region varchar(64) NOT NULL,
 instance_id varchar(32) NOT NULL,
 observation jsonb NOT NULL CHECK (jsonb_typeof(observation)='object'),
 observed_at timestamptz NOT NULL,
 missing boolean NOT NULL DEFAULT false,
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id),
 UNIQUE(tenant_id,workspace_id,account_id,region,instance_id)
);
CREATE INDEX aws_resource_scope ON aws_discovered_resources(tenant_id,workspace_id,id);
CREATE TABLE aws_import_receipts (
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 idempotency_key uuid NOT NULL,
 actor_id uuid NOT NULL REFERENCES users(id),
 connection_name varchar(48) NOT NULL,
 account_id varchar(12) NOT NULL CHECK (account_id ~ '^[0-9]{12}$'),
 region varchar(64) NOT NULL,
 started_at timestamptz NOT NULL,
 completed_at timestamptz NOT NULL CHECK (completed_at>=started_at),
 imported_count integer NOT NULL CHECK (imported_count>=0),
 PRIMARY KEY(tenant_id,workspace_id,idempotency_key),
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id)
);
CREATE INDEX aws_receipt_snapshot ON aws_import_receipts(tenant_id,workspace_id,account_id,region,started_at);
CREATE FUNCTION aws_receipt_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'AWS_RECEIPT_IMMUTABLE'; END;
$$;
CREATE TRIGGER aws_receipt_immutable BEFORE UPDATE OR DELETE ON aws_import_receipts FOR EACH ROW EXECUTE FUNCTION aws_receipt_immutable();
