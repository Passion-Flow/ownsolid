-- Transactional outbox (ARC-011, ADR 0028). A writer appends in the transaction of its change, and
-- every gateway reads rows below the oldest running transaction: a transaction that started first
-- can commit last, so no value assigned at write time follows commit order. The key orders the read;
-- the second index serves trimming. No sequence, so the serving role needs none.
CREATE TABLE control_outbox (
 site_id varchar(32) NOT NULL DEFAULT local_site_id(),
 cell_id varchar(32) NOT NULL DEFAULT local_cell_id(),
 transaction_id xid8 NOT NULL DEFAULT pg_current_xact_id(),
 id uuid NOT NULL DEFAULT gen_random_uuid(),
 topic varchar(64) NOT NULL CHECK (topic ~ '^[a-z][a-z0-9_.]*$'),
 key varchar(128) NOT NULL CHECK (char_length(key)>0),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (site_id,cell_id,transaction_id,id)
);
CREATE INDEX control_outbox_trim_idx ON control_outbox(created_at);
