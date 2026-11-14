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
