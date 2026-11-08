-- An agent-delivered gateway has no form in the earlier schema; refuse rather than drop it.
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM gateway_instances WHERE delivery='agent') OR EXISTS(SELECT 1 FROM node_tasks WHERE operation IN ('gateway.start.v1','gateway.stop.v1'))
  OR EXISTS(SELECT 1 FROM managed_nodes WHERE capabilities ? 'gateway.start.v1') THEN
  RAISE EXCEPTION 'agent-delivered gateways exist' USING ERRCODE='55000';
 END IF;
END $$;
ALTER TABLE gateway_instances
 DROP CONSTRAINT gateway_instances_credential_check,
 DROP CONSTRAINT gateway_instances_origin_check,
 DROP CONSTRAINT gateway_instances_delivery_task_check,
 DROP CONSTRAINT gateway_instances_activation_check,
 DROP CONSTRAINT gateway_instances_delivery_error_check;
ALTER TABLE gateway_instances ADD CONSTRAINT gateway_instances_check1 CHECK (credential_hash IS NOT NULL OR authority_invalidated_at IS NOT NULL);
ALTER TABLE gateway_instances
 ALTER COLUMN client_origin SET NOT NULL,
 DROP COLUMN certificate_not_after,
 DROP COLUMN delivery_task_id,
 DROP COLUMN delivery_error_code,
 DROP COLUMN delivery_state,
 DROP COLUMN delivery;
ALTER TABLE managed_nodes DROP CONSTRAINT managed_nodes_capabilities_check;
ALTER TABLE managed_nodes ADD CONSTRAINT managed_nodes_capabilities_check CHECK(
 (execution_profile='read_only' AND capabilities='["collect_facts.v1"]'::jsonb) OR
 (execution_profile='systemd_components' AND capabilities='["collect_facts.v1","node_exporter.install.v1","node_exporter.start.v1","node_exporter.stop.v1","node_exporter.remove.v1","node_exporter.observe.v1"]'::jsonb) OR
 (execution_profile='systemd_maintenance' AND capabilities='["collect_facts.v1","node_exporter.install.v1","node_exporter.start.v1","node_exporter.stop.v1","node_exporter.remove.v1","node_exporter.observe.v1","node_exporter.configure.v1","node_exporter.restart.v1","node_exporter.upgrade.v1","node_exporter.rollback.v1","node_exporter.logs.v1"]'::jsonb) OR
 (execution_profile='docker_inference' AND capabilities='["collect_facts.v1","inference.deploy.v1","inference.stop.v1","inference.remove.v1","inference.observe.v1","inference.verify.v1"]'::jsonb) OR
 (execution_profile='docker_ai' AND capabilities='["collect_facts.v1","inference.deploy.v1","inference.stop.v1","inference.remove.v1","inference.observe.v1","inference.verify.v1","training.run.v1"]'::jsonb) OR
 (execution_profile='docker_model_lifecycle' AND capabilities='["collect_facts.v1","inference.deploy.v1","inference.stop.v1","inference.remove.v1","inference.observe.v1","inference.verify.v1","training.run.v1","model.prepare.v1","inference.derived.v1"]'::jsonb));
ALTER TABLE node_tasks DROP CONSTRAINT node_tasks_operation_check;
ALTER TABLE node_tasks ADD CONSTRAINT node_tasks_operation_check CHECK(operation IN ('collect_facts.v1','node_exporter.install.v1','node_exporter.start.v1','node_exporter.stop.v1','node_exporter.remove.v1','node_exporter.observe.v1','node_exporter.configure.v1','node_exporter.restart.v1','node_exporter.upgrade.v1','node_exporter.rollback.v1','node_exporter.logs.v1','inference.deploy.v1','inference.stop.v1','inference.remove.v1','inference.observe.v1','inference.verify.v1','training.run.v1','model.prepare.v1','inference.derived.v1'));
