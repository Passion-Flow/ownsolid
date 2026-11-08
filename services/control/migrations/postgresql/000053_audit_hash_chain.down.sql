-- A downgrade must never silently discard audit evidence. Existing chained
-- deployments should retain their database and restore a pre-upgrade snapshot.
LOCK TABLE audit_events IN ACCESS EXCLUSIVE MODE;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM audit_chain_head WHERE chain_position > 0) THEN
        RAISE EXCEPTION 'AUDIT_CHAIN_DOWNGRADE_WOULD_DISCARD_EVIDENCE';
    END IF;
END
$$;
DROP TRIGGER audit_chain_append ON audit_events;
DROP FUNCTION audit_chain_append();
DROP FUNCTION audit_chain_projection(jsonb, bigint, timestamptz, bytea);
DROP FUNCTION audit_chain_metadata(jsonb);
DROP FUNCTION audit_chain_frame(text);
DROP TABLE audit_chain_head;
ALTER TABLE audit_events
    DROP CONSTRAINT audit_events_chain_shape,
    DROP COLUMN chain_position,
    DROP COLUMN prev_hash,
    DROP COLUMN hash;
