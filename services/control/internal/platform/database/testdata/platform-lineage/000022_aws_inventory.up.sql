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
