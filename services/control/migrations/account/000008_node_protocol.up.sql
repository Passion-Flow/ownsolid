-- Minimal M03 uses the existing shared node repository, with discovery-only invariants.
ALTER TABLE node_join_tokens ADD COLUMN execution_profile varchar(32) NOT NULL DEFAULT 'read_only' CHECK (execution_profile='read_only');
ALTER TABLE managed_nodes ADD COLUMN execution_profile varchar(32) NOT NULL DEFAULT 'read_only' CHECK (execution_profile='read_only');
ALTER TABLE managed_nodes ADD COLUMN capabilities jsonb NOT NULL DEFAULT '["collect_facts.v1"]'::jsonb CHECK (capabilities='["collect_facts.v1"]'::jsonb);
-- Reserved repository fields remain inert; no workload owner/schema is constructed.
ALTER TABLE managed_nodes ADD COLUMN inference_fenced_at timestamptz CHECK (inference_fenced_at IS NULL);
ALTER TABLE node_tasks ADD COLUMN payload jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (payload='{}'::jsonb);
ALTER TABLE node_tasks ADD COLUMN request_fingerprint bytea CHECK (request_fingerprint IS NULL);
ALTER TABLE node_tasks ADD COLUMN inference_requested_epoch bigint CHECK (inference_requested_epoch IS NULL);
ALTER TABLE node_tasks ADD CONSTRAINT node_tasks_scope_identity_key UNIQUE(tenant_id,workspace_id,node_id,id);
