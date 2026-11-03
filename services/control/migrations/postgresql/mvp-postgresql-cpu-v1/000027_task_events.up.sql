-- New account22-derived MVP lineage; immutable legacy assets are unchanged.
-- M25 transactional task event state and exact runtime guards.
-- Inactive source candidate. Parent assigns migration numbers and activation.
-- Requires the real node_tasks full scope key introduced by PostgreSQL M05.
CREATE TABLE task_event_outbox (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL,workspace_id uuid NOT NULL,node_id uuid NOT NULL,task_id uuid NOT NULL,
 namespace_id uuid NOT NULL,event_kind varchar(32) NOT NULL CHECK(event_kind='node.task.queued.v1'),operation varchar(64) NOT NULL,
 event_bytes bytea NOT NULL CHECK(octet_length(event_bytes) BETWEEN 1 AND 4096),logical_sha256 char(64) NOT NULL CHECK(logical_sha256~'^[a-f0-9]{64}$'),
 delivery_id uuid NOT NULL,generation integer NOT NULL CHECK(generation>=1),
 state varchar(16) NOT NULL CHECK(state IN('prepared','sent','published','received','obsolete','dead')),
 outcome_unknown boolean NOT NULL DEFAULT false,publish_attempts integer NOT NULL DEFAULT 0 CHECK(publish_attempts>=0),consume_attempts bigint NOT NULL DEFAULT 0 CHECK(consume_attempts>=0),
 claim_id uuid,claim_expires_at timestamptz,broker_identity char(64) NOT NULL CHECK(broker_identity~'^[a-f0-9]{64}$'),broker_sequence numeric(20,0),
 consumer_name varchar(64) NOT NULL,error_code varchar(64),not_before timestamptz NOT NULL,expires_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL,updated_at timestamptz NOT NULL,
 UNIQUE(tenant_id,workspace_id,node_id,task_id,id),UNIQUE(tenant_id,workspace_id,node_id,task_id,event_kind),UNIQUE(id,logical_sha256),UNIQUE(delivery_id),
 FOREIGN KEY(tenant_id,workspace_id,node_id,task_id) REFERENCES node_tasks(tenant_id,workspace_id,node_id,id),
 CONSTRAINT task_event_outbox_claim CHECK((claim_id IS NULL)=(claim_expires_at IS NULL)),
 CONSTRAINT task_event_outbox_unknown CHECK(NOT outcome_unknown OR state IN('sent','dead')),
 CONSTRAINT task_event_outbox_sequence CHECK(broker_sequence IS NULL OR (broker_sequence>=1 AND broker_sequence<=18446744073709551615 AND broker_identity IS NOT NULL)),
 CONSTRAINT task_event_outbox_ack CHECK(state NOT IN('published','received','obsolete') OR broker_sequence IS NOT NULL),
 CONSTRAINT task_event_outbox_time CHECK(expires_at>created_at AND updated_at>=created_at)
);
CREATE TABLE task_event_receipts (
 id uuid PRIMARY KEY,event_id uuid NOT NULL UNIQUE,tenant_id uuid NOT NULL,workspace_id uuid NOT NULL,node_id uuid NOT NULL,task_id uuid NOT NULL,
 logical_sha256 char(64) NOT NULL CHECK(logical_sha256~'^[a-f0-9]{64}$'),delivery_id uuid NOT NULL,generation integer NOT NULL CHECK(generation>=1),
 consumer_name varchar(64) NOT NULL,broker_identity char(64) NOT NULL CHECK(broker_identity~'^[a-f0-9]{64}$'),
 broker_sequence numeric(20,0) NOT NULL CHECK(broker_sequence BETWEEN 1 AND 18446744073709551615),consumer_sequence numeric(20,0) NOT NULL CHECK(consumer_sequence BETWEEN 1 AND 18446744073709551615),
 outcome varchar(16) NOT NULL CHECK(outcome IN('received','obsolete')),task_state varchar(16) NOT NULL CHECK(task_state IN('pending','running','succeeded','failed','cancelled')),
 created_at timestamptz NOT NULL,
 FOREIGN KEY(tenant_id,workspace_id,node_id,task_id,event_id) REFERENCES task_event_outbox(tenant_id,workspace_id,node_id,task_id,id),
 FOREIGN KEY(event_id,logical_sha256) REFERENCES task_event_outbox(id,logical_sha256)
);
CREATE TABLE task_event_dead_letters (
 id uuid PRIMARY KEY,event_id uuid,tenant_id uuid NOT NULL,workspace_id uuid,node_id uuid,task_id uuid,generation integer,delivery_id uuid,
 kind varchar(16) NOT NULL CHECK(kind IN('publish','consume','expiry','poison')),outcome_unknown boolean NOT NULL,
 broker_identity char(64) NOT NULL CHECK(broker_identity~'^[a-f0-9]{64}$'),broker_sequence numeric(20,0),consumer_name varchar(64) NOT NULL,
 envelope_sha256 char(64) NOT NULL CHECK(envelope_sha256~'^[a-f0-9]{64}$'),error_code varchar(64) NOT NULL,
 retry_actor uuid REFERENCES users(id),retry_key uuid,retry_reason varchar(500),retry_fingerprint bytea CHECK(retry_fingerprint IS NULL OR octet_length(retry_fingerprint)=32),retry_delivery_id uuid,retry_generation integer,retried_at timestamptz,
 created_at timestamptz NOT NULL,
 UNIQUE(event_id,generation),UNIQUE(delivery_id),UNIQUE(broker_identity,broker_sequence,consumer_name),UNIQUE(retry_actor,retry_key),
 FOREIGN KEY(tenant_id,workspace_id,node_id,task_id,event_id) REFERENCES task_event_outbox(tenant_id,workspace_id,node_id,task_id,id),
 CONSTRAINT task_event_dead_letters_scope CHECK((event_id IS NOT NULL AND workspace_id IS NOT NULL AND node_id IS NOT NULL AND task_id IS NOT NULL AND generation IS NOT NULL AND generation>=1 AND delivery_id IS NOT NULL) OR (event_id IS NULL AND workspace_id IS NULL AND node_id IS NULL AND task_id IS NULL AND generation IS NULL AND delivery_id IS NULL AND kind='poison')),
 CONSTRAINT task_event_dead_letters_sequence CHECK(broker_sequence IS NULL OR broker_sequence BETWEEN 1 AND 18446744073709551615),
 CONSTRAINT task_event_dead_letters_retry CHECK((retry_actor IS NULL AND retry_key IS NULL AND retry_reason IS NULL AND retry_fingerprint IS NULL AND retry_delivery_id IS NULL AND retry_generation IS NULL AND retried_at IS NULL) OR (event_id IS NOT NULL AND retry_actor IS NOT NULL AND retry_key IS NOT NULL AND retry_reason IS NOT NULL AND retry_fingerprint IS NOT NULL AND retry_delivery_id IS NOT NULL AND retry_generation IS NOT NULL AND retry_generation>generation AND retried_at IS NOT NULL AND retried_at>=created_at))
);
CREATE INDEX task_event_outbox_due ON task_event_outbox(state,not_before,created_at,id);
CREATE INDEX task_event_outbox_scope ON task_event_outbox(tenant_id,workspace_id,node_id,created_at DESC,id DESC);
CREATE INDEX task_event_dead_letters_scope ON task_event_dead_letters(tenant_id,workspace_id,node_id,created_at DESC,id DESC);

ALTER TABLE task_event_outbox ADD CONSTRAINT task_event_outbox_error CHECK(error_code IS NULL OR error_code IN('TASK_BUS_READBACK_CONFLICT','TASK_BUS_IDENTITY_CHANGED','TASK_BUS_PROFILE_REJECTED','TASK_BUS_PUBLISH_UNKNOWN','TASK_EVENT_SOURCE_OBSOLETE','TASK_EVENT_SOURCE_CHANGED','TASK_BUS_PUBLISH_EXHAUSTED','TASK_BUS_CONSUME_EXHAUSTED','TRANSPORT_RETENTION_EXPIRED','TASK_EVENT_ENVELOPE_REJECTED','TASK_EVENT_UNCOMMITTED_SOURCE','TASK_EVENT_STALE_DELIVERY','TASK_BUS_RESTORE_FENCED'));
ALTER TABLE task_event_dead_letters ADD CONSTRAINT task_event_dead_letters_error CHECK(error_code IN('TASK_BUS_READBACK_CONFLICT','TASK_BUS_IDENTITY_CHANGED','TASK_BUS_PROFILE_REJECTED','TASK_BUS_PUBLISH_UNKNOWN','TASK_EVENT_SOURCE_OBSOLETE','TASK_EVENT_SOURCE_CHANGED','TASK_BUS_PUBLISH_EXHAUSTED','TASK_BUS_CONSUME_EXHAUSTED','TRANSPORT_RETENTION_EXPIRED','TASK_EVENT_ENVELOPE_REJECTED','TASK_EVENT_UNCOMMITTED_SOURCE','TASK_EVENT_STALE_DELIVERY','TASK_BUS_RESTORE_FENCED'));
ALTER TABLE task_event_outbox ADD CONSTRAINT task_event_outbox_sent CHECK(state<>'sent' OR outcome_unknown);
ALTER TABLE task_event_outbox ADD CONSTRAINT task_event_outbox_prepared CHECK(state<>'prepared' OR (NOT outcome_unknown AND publish_attempts=0 AND consume_attempts=0 AND broker_sequence IS NULL AND claim_id IS NULL AND error_code IS NULL));
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
