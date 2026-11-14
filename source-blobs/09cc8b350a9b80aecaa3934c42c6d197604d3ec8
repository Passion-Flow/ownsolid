-- Connectivity changes of a node (ADR 0022). The last-seen time alone cannot say when a node
-- came back or how often it flaps. Readers that decide anything keep deriving connectivity
-- from last_heartbeat_at, so a stopped worker cannot leave a node looking online.
ALTER TABLE managed_nodes ADD COLUMN connectivity varchar(8) NOT NULL DEFAULT 'online' CHECK (connectivity IN ('online','unknown','offline'));
ALTER TABLE managed_nodes ADD COLUMN connectivity_changed_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP;
