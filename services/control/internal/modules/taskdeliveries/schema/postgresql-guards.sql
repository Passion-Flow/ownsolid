-- Inactive PostgreSQL source guards; apply after the three real candidate tables.
CREATE FUNCTION task_event_outbox_guard() RETURNS trigger LANGUAGE plpgsql AS $$
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
  IF jsonb_typeof(j)<>'object' OR (SELECT count(*) FROM jsonb_object_keys(j))<>11 OR jsonb_typeof(j->'schema') IS DISTINCT FROM 'number' OR j->>'schema' IS DISTINCT FROM '1' OR j->>'event_id' IS DISTINCT FROM NEW.id::text OR j->>'namespace_id' IS DISTINCT FROM NEW.namespace_id::text OR j->>'tenant_id' IS DISTINCT FROM NEW.tenant_id::text OR j->>'workspace_id' IS DISTINCT FROM NEW.workspace_id::text OR j->>'node_id' IS DISTINCT FROM NEW.node_id::text OR j->>'task_id' IS DISTINCT FROM NEW.task_id::text OR j->>'kind' IS DISTINCT FROM NEW.event_kind OR j->>'operation' IS DISTINCT FROM NEW.operation OR (j->>'created_at')::timestamptz IS DISTINCT FROM NEW.created_at OR (j->>'task_created_at')::timestamptz IS DISTINCT FROM source_time THEN RAISE EXCEPTION 'TASK_EVENT_SOURCE_INVALID'; END IF;
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
CREATE TRIGGER task_event_outbox_guard BEFORE INSERT OR UPDATE OR DELETE ON task_event_outbox FOR EACH ROW EXECUTE FUNCTION task_event_outbox_guard();

CREATE FUNCTION task_event_receipt_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE source task_event_outbox;
BEGIN
 IF TG_OP='UPDATE' THEN RAISE EXCEPTION 'TASK_EVENT_RECEIPT_IMMUTABLE'; END IF;
 IF TG_OP='DELETE' THEN
  IF NOT EXISTS(SELECT 1 FROM task_event_outbox o JOIN node_tasks t ON (t.tenant_id,t.workspace_id,t.node_id,t.id)=(o.tenant_id,o.workspace_id,o.node_id,o.task_id) WHERE o.id=OLD.event_id AND NOT o.outcome_unknown AND o.claim_id IS NULL AND o.state IN('received','obsolete','dead') AND o.expires_at<=clock_timestamp() AND t.state IN('succeeded','failed','cancelled')) THEN RAISE EXCEPTION 'TASK_EVENT_RETENTION_REFUSED'; END IF;
  RETURN OLD;
 END IF;
 SELECT * INTO source FROM task_event_outbox WHERE id=NEW.event_id FOR SHARE;
 IF NOT FOUND OR (source.tenant_id,source.workspace_id,source.node_id,source.task_id,source.logical_sha256,source.delivery_id,source.generation,source.broker_identity,source.consumer_name) IS DISTINCT FROM (NEW.tenant_id,NEW.workspace_id,NEW.node_id,NEW.task_id,NEW.logical_sha256,NEW.delivery_id,NEW.generation,NEW.broker_identity,NEW.consumer_name) OR source.state NOT IN('sent','published','dead') OR NEW.created_at<source.created_at OR NOT EXISTS(SELECT 1 FROM node_tasks t WHERE (t.tenant_id,t.workspace_id,t.node_id,t.id)=(NEW.tenant_id,NEW.workspace_id,NEW.node_id,NEW.task_id) AND t.state=NEW.task_state) THEN RAISE EXCEPTION 'TASK_EVENT_RECEIPT_INVALID'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER task_event_receipt_guard BEFORE INSERT OR UPDATE OR DELETE ON task_event_receipts FOR EACH ROW EXECUTE FUNCTION task_event_receipt_guard();

CREATE FUNCTION task_event_dead_letter_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE source task_event_outbox;
BEGIN
 IF TG_OP='DELETE' THEN
  IF OLD.event_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM task_event_outbox o JOIN node_tasks t ON (t.tenant_id,t.workspace_id,t.node_id,t.id)=(o.tenant_id,o.workspace_id,o.node_id,o.task_id) WHERE o.id=OLD.event_id AND NOT o.outcome_unknown AND o.claim_id IS NULL AND o.state IN('received','obsolete','dead') AND o.expires_at<=clock_timestamp() AND t.state IN('succeeded','failed','cancelled')) THEN RAISE EXCEPTION 'TASK_EVENT_RETENTION_REFUSED'; END IF;
  RETURN OLD;
 END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.retry_key IS NOT NULL THEN RAISE EXCEPTION 'TASK_EVENT_RETRY_INVALID'; END IF;
  IF NEW.event_id IS NOT NULL THEN
   SELECT * INTO source FROM task_event_outbox WHERE id=NEW.event_id FOR SHARE;
   IF NOT FOUND OR (source.tenant_id,source.workspace_id,source.node_id,source.task_id,source.generation,source.delivery_id,source.broker_identity,source.consumer_name) IS DISTINCT FROM (NEW.tenant_id,NEW.workspace_id,NEW.node_id,NEW.task_id,NEW.generation,NEW.delivery_id,NEW.broker_identity,NEW.consumer_name) OR source.state IN('received','obsolete') OR NEW.created_at<source.created_at OR NEW.envelope_sha256<>encode(sha256(convert_to('{"delivery_id":"'||source.delivery_id::text||'","event":','UTF8')||source.event_bytes||convert_to('}','UTF8')),'hex') THEN RAISE EXCEPTION 'TASK_EVENT_DLQ_INVALID'; END IF;
  ELSIF NEW.kind<>'poison' OR NEW.broker_sequence IS NULL OR NEW.outcome_unknown THEN RAISE EXCEPTION 'TASK_EVENT_DLQ_INVALID'; END IF;
  RETURN NEW;
 END IF;
 IF (NEW.id,NEW.event_id,NEW.tenant_id,NEW.workspace_id,NEW.node_id,NEW.task_id,NEW.generation,NEW.delivery_id,NEW.kind,NEW.outcome_unknown,NEW.broker_identity,NEW.broker_sequence,NEW.consumer_name,NEW.envelope_sha256,NEW.error_code,NEW.created_at) IS DISTINCT FROM (OLD.id,OLD.event_id,OLD.tenant_id,OLD.workspace_id,OLD.node_id,OLD.task_id,OLD.generation,OLD.delivery_id,OLD.kind,OLD.outcome_unknown,OLD.broker_identity,OLD.broker_sequence,OLD.consumer_name,OLD.envelope_sha256,OLD.error_code,OLD.created_at) OR OLD.retry_key IS NOT NULL OR NEW.retry_key IS NULL OR NEW.retry_generation<>OLD.generation+1 OR NOT EXISTS(SELECT 1 FROM users u WHERE u.id=NEW.retry_actor AND u.status='active' AND u.is_platform_admin) OR NOT EXISTS(SELECT 1 FROM task_event_outbox o WHERE o.id=OLD.event_id AND o.generation=OLD.generation AND o.state='dead' AND o.expires_at>NEW.retried_at) THEN RAISE EXCEPTION 'TASK_EVENT_RETRY_INVALID'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER task_event_dead_letter_guard BEFORE INSERT OR UPDATE OR DELETE ON task_event_dead_letters FOR EACH ROW EXECUTE FUNCTION task_event_dead_letter_guard();
