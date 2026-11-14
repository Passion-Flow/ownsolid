CREATE TABLE task_event_receipts (
 id char(36) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,event_id char(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL UNIQUE,tenant_id char(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,workspace_id char(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,node_id char(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,task_id char(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 logical_sha256 char(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL CHECK(REGEXP_LIKE(logical_sha256,'^[a-f0-9]{64}$','c')),delivery_id char(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,generation integer NOT NULL CHECK(generation>=1),
 consumer_name varchar(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,broker_identity char(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL CHECK(REGEXP_LIKE(broker_identity,'^[a-f0-9]{64}$','c')),
 broker_sequence decimal(20,0) NOT NULL CHECK(broker_sequence BETWEEN 1 AND 18446744073709551615),consumer_sequence decimal(20,0) NOT NULL CHECK(consumer_sequence BETWEEN 1 AND 18446744073709551615),
 outcome varchar(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL CHECK(outcome IN('received','obsolete')),task_state varchar(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL CHECK(task_state IN('pending','running','succeeded','failed','cancelled')),
 created_at datetime(6) NOT NULL,
 FOREIGN KEY(tenant_id,workspace_id,node_id,task_id,event_id) REFERENCES task_event_outbox(tenant_id,workspace_id,node_id,task_id,id),
 FOREIGN KEY(event_id,logical_sha256) REFERENCES task_event_outbox(id,logical_sha256)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;