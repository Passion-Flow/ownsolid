-- M07 scoped gateway authority, immutable intents and metadata-only usage receipts.
ALTER TABLE workspace_memberships ADD COLUMN authority_id uuid NOT NULL DEFAULT gen_random_uuid();
CREATE TABLE gateway_instances (
 client_origin text NOT NULL CHECK (length(client_origin) BETWEEN 9 AND 2048),
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 node_id uuid NOT NULL,
 name varchar(100) NOT NULL,
 control_origin_sha256 varchar(64) NOT NULL CHECK(control_origin_sha256 ~ '^[a-f0-9]{64}$'),
 credential_hash bytea CHECK(credential_hash IS NULL OR octet_length(credential_hash)=32),
 created_by uuid NOT NULL REFERENCES users(id),
 requested_epoch bigint NOT NULL CHECK(requested_epoch>=0),
 idempotency_key uuid NOT NULL,
 fingerprint bytea NOT NULL CHECK(octet_length(fingerprint)=32),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 revoked_at timestamptz,
 revoke_reason varchar(500),
 authority_invalidated_at timestamptz,
 UNIQUE(tenant_id,workspace_id,id),
 UNIQUE(tenant_id,workspace_id,node_id,id),
 UNIQUE(tenant_id,workspace_id,created_by,idempotency_key),
 FOREIGN KEY(tenant_id,workspace_id,node_id) REFERENCES managed_nodes(tenant_id,workspace_id,id),
 CHECK(revoked_at IS NULL OR (revoke_reason IS NOT NULL AND length(revoke_reason)>0)),
 CHECK(credential_hash IS NOT NULL OR authority_invalidated_at IS NOT NULL)
);
CREATE UNIQUE INDEX gateway_one_active_node_idx ON gateway_instances(node_id) WHERE revoked_at IS NULL AND authority_invalidated_at IS NULL;
CREATE TABLE model_aliases (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 node_id uuid NOT NULL,
 gateway_id uuid NOT NULL,
 name varchar(64) NOT NULL CHECK(name ~ '^[a-z][a-z0-9.-]{0,63}$'),
 deployment_id uuid NOT NULL,
 deployment_revision bigint NOT NULL CHECK(deployment_revision BETWEEN 1 AND 9007199254740991),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9007199254740991),
 created_by uuid NOT NULL REFERENCES users(id),
 idempotency_key uuid NOT NULL,
 fingerprint bytea NOT NULL CHECK(octet_length(fingerprint)=32),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 disabled_at timestamptz,
 UNIQUE(tenant_id,workspace_id,id),
 UNIQUE(tenant_id,workspace_id,name),
 UNIQUE(tenant_id,workspace_id,created_by,idempotency_key),
 FOREIGN KEY(tenant_id,workspace_id,node_id,gateway_id) REFERENCES gateway_instances(tenant_id,workspace_id,node_id,id),
 FOREIGN KEY(tenant_id,workspace_id,node_id,deployment_id) REFERENCES deployments(tenant_id,workspace_id,node_id,id)
);
CREATE TABLE model_alias_history (
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 alias_id uuid NOT NULL,
 revision bigint NOT NULL CHECK(revision BETWEEN 1 AND 9007199254740991),
 action varchar(16) NOT NULL CHECK(action IN ('published','disabled')),
 actor_user_id uuid NOT NULL REFERENCES users(id),
 reason varchar(500) NOT NULL CHECK(length(reason)>0),
 occurred_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(tenant_id,workspace_id,alias_id,revision),
 FOREIGN KEY(tenant_id,workspace_id,alias_id) REFERENCES model_aliases(tenant_id,workspace_id,id)
);
CREATE TABLE api_keys (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 alias_id uuid NOT NULL,
 label varchar(100) NOT NULL,
 secret_hash bytea NOT NULL CHECK(octet_length(secret_hash)=32),
 created_by uuid NOT NULL REFERENCES users(id),
 requested_epoch bigint NOT NULL CHECK(requested_epoch>=0),
 membership_authority uuid NOT NULL,
 idempotency_key uuid NOT NULL,
 fingerprint bytea NOT NULL CHECK(octet_length(fingerprint)=32),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 expires_at timestamptz NOT NULL,
 last_used_at timestamptz,
 revoked_at timestamptz,
 revoke_reason varchar(500),
 UNIQUE(tenant_id,workspace_id,id),
 UNIQUE(tenant_id,workspace_id,created_by,idempotency_key),
 FOREIGN KEY(tenant_id,workspace_id,alias_id) REFERENCES model_aliases(tenant_id,workspace_id,id),
 CHECK(expires_at>created_at),
 CHECK(revoked_at IS NULL OR (revoke_reason IS NOT NULL AND length(revoke_reason)>0))
);
CREATE INDEX api_key_owner_idx ON api_keys(tenant_id,workspace_id,created_by,created_at DESC,id DESC);
CREATE TABLE gateway_requests (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 node_id uuid NOT NULL,
 gateway_id uuid NOT NULL,
 api_key_id uuid NOT NULL,
 actor_user_id uuid NOT NULL REFERENCES users(id),
 actor_epoch bigint NOT NULL CHECK(actor_epoch>=0),
 alias_id uuid NOT NULL,
 alias_revision bigint NOT NULL,
 deployment_id uuid NOT NULL,
 deployment_revision bigint NOT NULL CHECK(deployment_revision BETWEEN 1 AND 9007199254740991),
 maximum_output_tokens integer NOT NULL CHECK(maximum_output_tokens BETWEEN 1 AND 512),
 maximum_context_tokens integer NOT NULL CHECK(maximum_context_tokens BETWEEN 1 AND 65536),
 maximum_request_seconds integer NOT NULL CHECK(maximum_request_seconds BETWEEN 1 AND 300),
 stream boolean NOT NULL,
 state varchar(16) NOT NULL CHECK(state IN ('admitted','reported','reconciled')),
 capacity_held boolean NOT NULL DEFAULT true,
 admitted_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 deadline_at timestamptz NOT NULL,
 resolved_at timestamptz,
 outcome jsonb CHECK(outcome IS NULL OR (jsonb_typeof(outcome)='object' AND octet_length(outcome::text)<=4096 AND outcome ?& ARRAY['request_id','status','error_code','usage','elapsed_milliseconds','output_started']::text[] AND outcome - ARRAY['request_id','status','error_code','usage','elapsed_milliseconds','output_started']::text[]='{}'::jsonb AND outcome->>'request_id'=id::text)),
 outcome_sha256 varchar(64) CHECK(outcome_sha256 IS NULL OR outcome_sha256 ~ '^[a-f0-9]{64}$'),
 reported_at timestamptz,
 reconciled_by uuid REFERENCES users(id),
 reconciled_at timestamptz,
 reconciliation_reason varchar(500),
 UNIQUE(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,node_id,gateway_id) REFERENCES gateway_instances(tenant_id,workspace_id,node_id,id),
 FOREIGN KEY(tenant_id,workspace_id,api_key_id) REFERENCES api_keys(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,alias_id,alias_revision) REFERENCES model_alias_history(tenant_id,workspace_id,alias_id,revision),
 FOREIGN KEY(tenant_id,workspace_id,node_id,deployment_id) REFERENCES deployments(tenant_id,workspace_id,node_id,id),
 CHECK(deadline_at>admitted_at),
 CHECK(state<>'admitted' OR capacity_held),
 CHECK(state<>'reconciled' OR NOT capacity_held),
 CHECK(outcome IS NULL OR reconciled_at IS NOT NULL OR (outcome->>'status' IN ('succeeded','client_cancelled','upstream_timeout','upstream_error','invalid_response','gateway_shutdown') AND COALESCE(outcome->>'error_code','')<>'gateway_interrupted') OR capacity_held),
 CHECK((state='admitted' AND resolved_at IS NULL AND outcome IS NULL AND outcome_sha256 IS NULL AND reported_at IS NULL AND reconciled_at IS NULL)
    OR (state='reported' AND resolved_at IS NOT NULL AND outcome IS NOT NULL AND outcome_sha256 IS NOT NULL AND reported_at IS NOT NULL)
    OR (state='reconciled' AND resolved_at IS NOT NULL AND outcome IS NULL AND outcome_sha256 IS NULL AND reported_at IS NULL AND reconciled_at IS NOT NULL)),
 CHECK((reconciled_at IS NULL AND reconciled_by IS NULL AND reconciliation_reason IS NULL)
    OR (reconciled_at IS NOT NULL AND reconciled_by IS NOT NULL AND reconciliation_reason IS NOT NULL AND length(reconciliation_reason)>0))
);
CREATE INDEX gateway_request_held_deployment_idx ON gateway_requests(deployment_id) WHERE capacity_held;
CREATE INDEX gateway_request_owner_idx ON gateway_requests(tenant_id,workspace_id,actor_user_id,admitted_at DESC,id DESC);
CREATE INDEX gateway_request_instance_idx ON gateway_requests(tenant_id,workspace_id,gateway_id,admitted_at DESC,id DESC);
CREATE TABLE gateway_recoveries (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 request_id uuid NOT NULL,
 requested_by uuid NOT NULL REFERENCES users(id),
 reason varchar(500) NOT NULL CHECK(length(reason)>0),
 idempotency_key uuid NOT NULL,
 fingerprint bytea NOT NULL CHECK(octet_length(fingerprint)=32),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 expires_at timestamptz NOT NULL,
 superseded_at timestamptz,
 invalidated_at timestamptz,
 fence_receipt jsonb CHECK(fence_receipt IS NULL OR (jsonb_typeof(fence_receipt)='object' AND octet_length(fence_receipt::text)<=4096)),
 fence_receipt_sha256 varchar(64) CHECK(fence_receipt_sha256 IS NULL OR fence_receipt_sha256 ~ '^[a-f0-9]{64}$'),
 fence_accepted_at timestamptz,
 fence_accepted_by uuid REFERENCES users(id),
 confirmed_at timestamptz,
 confirmation_fingerprint bytea CHECK(confirmation_fingerprint IS NULL OR octet_length(confirmation_fingerprint)=32),
 confirmed_deployment_revision bigint CHECK(confirmed_deployment_revision IS NULL OR confirmed_deployment_revision BETWEEN 1 AND 9007199254740991),
 UNIQUE(tenant_id,workspace_id,request_id,id),
 UNIQUE(tenant_id,workspace_id,request_id,requested_by,idempotency_key),
 FOREIGN KEY(tenant_id,workspace_id,request_id) REFERENCES gateway_requests(tenant_id,workspace_id,id),
 CHECK(expires_at>created_at),
 CHECK((fence_receipt IS NULL AND fence_receipt_sha256 IS NULL AND fence_accepted_at IS NULL AND fence_accepted_by IS NULL)
    OR (fence_receipt IS NOT NULL AND fence_receipt_sha256 IS NOT NULL AND fence_accepted_at IS NOT NULL AND fence_accepted_by IS NOT NULL)),
 CHECK((confirmed_at IS NULL AND confirmation_fingerprint IS NULL AND confirmed_deployment_revision IS NULL)
    OR (confirmed_at IS NOT NULL AND fence_accepted_at IS NOT NULL AND confirmation_fingerprint IS NOT NULL AND confirmed_deployment_revision IS NOT NULL))
);
ALTER TABLE gateway_requests ADD COLUMN active_recovery_id uuid;
ALTER TABLE gateway_requests ADD FOREIGN KEY(tenant_id,workspace_id,id,active_recovery_id) REFERENCES gateway_recoveries(tenant_id,workspace_id,request_id,id);
CREATE INDEX gateway_recovery_request_idx ON gateway_recoveries(tenant_id,workspace_id,request_id,created_at DESC,id DESC);
