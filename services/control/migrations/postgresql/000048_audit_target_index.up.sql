-- The activity of one resource, such as a node's detail page (UX-127). Most events have a target, and this
-- leaves out those that do not. An index on the parent reaches every partition, now and to come (ADR 0029).
CREATE INDEX audit_events_target_idx ON audit_events(target_id,occurred_at DESC,id DESC) WHERE target_id IS NOT NULL;
