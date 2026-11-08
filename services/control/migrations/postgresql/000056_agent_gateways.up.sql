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
 (execution_profile='docker_model_lifecycle' AND capabilities='["collect_facts.v1","inference.deploy.v1","inference.stop.v1","inference.remove.v1","inference.observe.v1","inference.verify.v1","training.run.v1","model.prepare.v1","inference.derived.v1"]'::jsonb));
ALTER TABLE node_tasks DROP CONSTRAINT node_tasks_operation_check;
ALTER TABLE node_tasks ADD CONSTRAINT node_tasks_operation_check CHECK (operation::text = ANY (ARRAY['collect_facts.v1'::text,'node_exporter.install.v1'::text,'node_exporter.start.v1'::text,'node_exporter.stop.v1'::text,'node_exporter.remove.v1'::text,'node_exporter.observe.v1'::text,'node_exporter.configure.v1'::text,'node_exporter.restart.v1'::text,'node_exporter.upgrade.v1'::text,'node_exporter.rollback.v1'::text,'node_exporter.logs.v1'::text,'inference.deploy.v1'::text,'inference.stop.v1'::text,'inference.remove.v1'::text,'inference.observe.v1'::text,'inference.verify.v1'::text,'training.run.v1'::text,'model.prepare.v1'::text,'inference.derived.v1'::text,'gateway.start.v1'::text,'gateway.stop.v1'::text]));

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
