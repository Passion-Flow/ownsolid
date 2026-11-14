-- Every installation has a site and a cell (ADR 0006). Tables that belong to the global
-- layer (identity, tenancy, licensing) carry neither column; the list is enforced by
-- TestEveryCellOwnedTableCarriesScope. Rows default to the cell this database serves, so
-- the catalog is the same in every cell database and a cell's identity is one row of data.
CREATE TABLE sites (
 id varchar(32) PRIMARY KEY CHECK(id ~ '^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$'),
 display_name varchar(128) NOT NULL CHECK(length(display_name) BETWEEN 1 AND 128),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE cells (
 id varchar(32) PRIMARY KEY CHECK(id ~ '^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$'),
 site_id varchar(32) NOT NULL REFERENCES sites(id),
 display_name varchar(128) NOT NULL CHECK(length(display_name) BETWEEN 1 AND 128),
 is_local boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX cells_site_fk ON cells(site_id);
CREATE UNIQUE INDEX cells_one_local ON cells(is_local) WHERE is_local;
INSERT INTO sites(id,display_name) VALUES('default','Default site');
INSERT INTO cells(id,site_id,display_name,is_local) VALUES('default','default','Default cell',true);
CREATE FUNCTION local_site_id() RETURNS varchar LANGUAGE sql STABLE AS $$ SELECT site_id FROM cells WHERE is_local $$;
CREATE FUNCTION local_cell_id() RETURNS varchar LANGUAGE sql STABLE AS $$ SELECT id FROM cells WHERE is_local $$;
ALTER TABLE api_keys ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE artifact_object_grants ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE artifact_object_ingestions ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE artifact_object_parts ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE artifact_objects ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE audit_events ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE aws_discovered_resources ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE aws_import_receipts ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE component_log_snapshots ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE dataset_drafts ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE dataset_job_attempts ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE dataset_jobs ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE dataset_row_history ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE dataset_rows ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE dataset_versions ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE datasets ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE deployments ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE gateway_instances ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE gateway_recoveries ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE gateway_requests ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE gpu_leases ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE idempotency_records ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE image_registry_blobs ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE image_registry_deployment_refs ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE image_registry_grants ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE image_registry_imports ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE image_registry_mutations ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE image_registry_offline_refs ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE image_registry_pins ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE inference_nodes ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE knowledge_attempts ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE knowledge_bases ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE knowledge_chunks ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE knowledge_documents ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE knowledge_generations ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE knowledge_mutation_intents ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE knowledge_source_versions ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE managed_components ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE managed_nodes ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE middleware_instances ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE middleware_operations ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE model_alias_cutover_intents ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE model_alias_history ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE model_alias_target_versions ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE model_aliases ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE model_derivations ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE model_holdout_prompts ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE model_holdout_sources ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE model_holdouts ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE model_import_chunks ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE model_imports ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE model_preparation_artifacts ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE model_preparation_attempts ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE model_preparation_events ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE model_preparation_recoveries ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE model_preparation_reviews ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE model_preparation_upload_chunks ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE model_preparation_uploads ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE model_preparations ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE model_versions ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE mongodb_dataset_imports ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE node_join_tokens ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE node_tasks ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE plugin_grants ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE plugin_mutation_intents ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE plugin_versions ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE private_cluster_connections ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE private_cluster_intents ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE private_cluster_runs ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE rate_limit_buckets ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE task_event_dead_letters ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE task_event_outbox ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE task_event_receipts ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE training_artifacts ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE training_attempts ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE training_checkpoints ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE training_events ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE training_jobs ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE training_recoveries ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE training_upload_chunks ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE training_uploads ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE workflow_conversations ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE workflow_mutation_intents ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE workflow_node_traces ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE workflow_plugin_references ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE workflow_runs ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE workflow_versions ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
ALTER TABLE workflows ADD COLUMN site_id varchar(32) NOT NULL DEFAULT local_site_id(), ADD COLUMN cell_id varchar(32) NOT NULL DEFAULT local_cell_id();
-- Task events now carry their scope; shared brokers route on it.
CREATE OR REPLACE FUNCTION task_event_outbox_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE source_time timestamptz; source_operation text; j jsonb;
BEGIN
 IF TG_OP='DELETE' THEN
  IF OLD.outcome_unknown OR OLD.claim_id IS NOT NULL OR OLD.state NOT IN('received','obsolete','dead') OR OLD.expires_at>clock_timestamp() OR NOT EXISTS(SELECT 1 FROM node_tasks t WHERE (t.tenant_id,t.workspace_id,t.node_id,t.id)=(OLD.tenant_id,OLD.workspace_id,OLD.node_id,OLD.task_id) AND t.state IN('succeeded','failed','cancelled')) THEN
   RAISE EXCEPTION 'TASK_EVENT_RETENTION_REFUSED';
  END IF;
  RETURN OLD;
 END IF;
 IF TG_OP='INSERT' THEN
  SELECT t.created_at,t.operation INTO source_time,source_operation FROM node_tasks t JOIN managed_nodes n ON (n.tenant_id,n.workspace_id,n.id)=(t.tenant_id,t.workspace_id,t.node_id) JOIN workspaces w ON (w.tenant_id,w.id)=(n.tenant_id,n.workspace_id) WHERE (t.tenant_id,t.workspace_id,t.node_id,t.id)=(NEW.tenant_id,NEW.workspace_id,NEW.node_id,NEW.task_id) AND t.state='pending' AND t.attempt=0 AND n.status='active' AND w.status='active';
  IF NOT FOUND OR source_operation<>NEW.operation OR NEW.generation<>1 OR NEW.state<>'prepared' OR NEW.outcome_unknown OR NEW.publish_attempts<>0 OR NEW.consume_attempts<>0 OR NEW.claim_id IS NOT NULL OR NEW.broker_sequence IS NOT NULL OR NEW.error_code IS NOT NULL OR NEW.not_before<>NEW.created_at OR NEW.updated_at<>NEW.created_at OR NEW.created_at<source_time OR encode(sha256(NEW.event_bytes),'hex')<>NEW.logical_sha256 THEN RAISE EXCEPTION 'TASK_EVENT_SOURCE_INVALID'; END IF;
  j:=convert_from(NEW.event_bytes,'UTF8')::jsonb;
  IF jsonb_typeof(j)<>'object' OR (SELECT count(*) FROM jsonb_object_keys(j))<>13 OR j->>'site_id' IS DISTINCT FROM NEW.site_id OR j->>'cell_id' IS DISTINCT FROM NEW.cell_id OR jsonb_typeof(j->'schema') IS DISTINCT FROM 'number' OR j->>'schema' IS DISTINCT FROM '1' OR j->>'event_id' IS DISTINCT FROM NEW.id::text OR j->>'namespace_id' IS DISTINCT FROM NEW.namespace_id::text OR j->>'tenant_id' IS DISTINCT FROM NEW.tenant_id::text OR j->>'workspace_id' IS DISTINCT FROM NEW.workspace_id::text OR j->>'node_id' IS DISTINCT FROM NEW.node_id::text OR j->>'task_id' IS DISTINCT FROM NEW.task_id::text OR j->>'kind' IS DISTINCT FROM NEW.event_kind OR j->>'operation' IS DISTINCT FROM NEW.operation OR (j->>'created_at')::timestamptz IS DISTINCT FROM NEW.created_at OR (j->>'task_created_at')::timestamptz IS DISTINCT FROM source_time THEN RAISE EXCEPTION 'TASK_EVENT_SOURCE_INVALID'; END IF;
  RETURN NEW;
 END IF;
 IF (NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.node_id,NEW.task_id,NEW.namespace_id,NEW.event_kind,NEW.operation,NEW.event_bytes,NEW.logical_sha256,NEW.created_at,NEW.expires_at,NEW.broker_identity,NEW.consumer_name) IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.node_id,OLD.task_id,OLD.namespace_id,OLD.event_kind,OLD.operation,OLD.event_bytes,OLD.logical_sha256,OLD.created_at,OLD.expires_at,OLD.broker_identity,OLD.consumer_name) OR NEW.updated_at<OLD.updated_at OR OLD.state IN('received','obsolete') THEN RAISE EXCEPTION 'TASK_EVENT_IMMUTABLE'; END IF;
 IF NEW.generation<>OLD.generation THEN
  IF NEW.generation<>OLD.generation+1 OR NEW.delivery_id=OLD.delivery_id OR OLD.state<>'dead' OR NEW.state<>'prepared' OR NEW.outcome_unknown OR NEW.publish_attempts<>0 OR NEW.consume_attempts<>0 OR NEW.broker_sequence IS NOT NULL OR NEW.claim_id IS NOT NULL OR NEW.error_code IS NOT NULL OR NEW.not_before<>NEW.updated_at OR NOT EXISTS(SELECT 1 FROM task_event_dead_letters d WHERE d.event_id=OLD.id AND d.generation=OLD.generation AND d.retry_generation=NEW.generation AND d.retry_delivery_id=NEW.delivery_id AND d.retried_at=NEW.updated_at) THEN RAISE EXCEPTION 'TASK_EVENT_RETRY_INVALID'; END IF;
 ELSE
  IF NEW.delivery_id<>OLD.delivery_id OR NEW.publish_attempts<OLD.publish_attempts OR NEW.consume_attempts<OLD.consume_attempts OR NOT ((OLD.state='prepared' AND NEW.state IN('prepared','sent','dead')) OR (OLD.state='sent' AND NEW.state IN('sent','published','received','obsolete','dead')) OR (OLD.state='published' AND NEW.state IN('published','received','obsolete','dead')) OR (OLD.state='dead' AND NEW.state IN('dead','received','obsolete'))) THEN RAISE EXCEPTION 'TASK_EVENT_TRANSITION_INVALID'; END IF;
 END IF;
 IF NEW.state IN('received','obsolete') AND NOT EXISTS(SELECT 1 FROM task_event_receipts r WHERE r.event_id=NEW.id AND r.logical_sha256=NEW.logical_sha256 AND r.delivery_id=NEW.delivery_id AND r.generation=NEW.generation AND r.broker_identity=NEW.broker_identity AND r.consumer_name=NEW.consumer_name AND r.broker_sequence=NEW.broker_sequence AND r.outcome=NEW.state) THEN RAISE EXCEPTION 'TASK_EVENT_RECEIPT_INVALID'; END IF;
 IF NEW.state='dead' AND NOT EXISTS(SELECT 1 FROM task_event_dead_letters d WHERE d.event_id=NEW.id AND d.generation=NEW.generation AND d.error_code=NEW.error_code) THEN RAISE EXCEPTION 'TASK_EVENT_DLQ_INVALID'; END IF;
 RETURN NEW;
END $$;
