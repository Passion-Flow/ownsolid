CREATE TABLE task_event_outbox (
 id char(36) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,tenant_id char(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,workspace_id char(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,node_id char(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,task_id char(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 namespace_id char(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,event_kind varchar(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL CHECK(event_kind='node.task.queued.v1'),operation varchar(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 event_bytes varbinary(4096) NOT NULL CHECK(octet_length(event_bytes) BETWEEN 1 AND 4096),logical_sha256 char(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL CHECK(REGEXP_LIKE(logical_sha256,'^[a-f0-9]{64}$','c')),
 delivery_id char(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,generation integer NOT NULL CHECK(generation>=1),
 state varchar(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL CHECK(state IN('prepared','sent','published','received','obsolete','dead')),
 outcome_unknown boolean NOT NULL DEFAULT false CHECK(outcome_unknown IN(0,1)),publish_attempts integer NOT NULL DEFAULT 0 CHECK(publish_attempts>=0),consume_attempts bigint NOT NULL DEFAULT 0 CHECK(consume_attempts>=0),
 claim_id char(36) CHARACTER SET ascii COLLATE ascii_bin,claim_expires_at datetime(6),broker_identity char(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL CHECK(REGEXP_LIKE(broker_identity,'^[a-f0-9]{64}$','c')),broker_sequence decimal(20,0),
 consumer_name varchar(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,error_code varchar(64) CHARACTER SET ascii COLLATE ascii_bin,not_before datetime(6) NOT NULL,expires_at datetime(6) NOT NULL,
 created_at datetime(6) NOT NULL,updated_at datetime(6) NOT NULL,
 UNIQUE(tenant_id,workspace_id,node_id,task_id,id),UNIQUE(tenant_id,workspace_id,node_id,task_id,event_kind),UNIQUE(id,logical_sha256),UNIQUE(delivery_id),
 FOREIGN KEY(tenant_id,workspace_id,node_id,task_id) REFERENCES node_tasks(tenant_id,workspace_id,node_id,id),
 CONSTRAINT task_event_outbox_claim CHECK((claim_id IS NULL)=(claim_expires_at IS NULL)),
 CONSTRAINT task_event_outbox_unknown CHECK(NOT outcome_unknown OR state IN('sent','dead')),
 CONSTRAINT task_event_outbox_sequence CHECK(broker_sequence IS NULL OR (broker_sequence>=1 AND broker_sequence<=18446744073709551615 AND broker_identity IS NOT NULL)),
 CONSTRAINT task_event_outbox_ack CHECK(state NOT IN('published','received','obsolete') OR broker_sequence IS NOT NULL),
 CONSTRAINT task_event_outbox_time CHECK(expires_at>created_at AND updated_at>=created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;