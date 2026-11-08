-- Run in a single transaction. Existing V2 checkpoints must remain verifiable.
LOCK TABLE audit_events IN ACCESS EXCLUSIVE MODE;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM audit_chain_head WHERE scoped_from IS NOT NULL) THEN
        RAISE EXCEPTION 'AUDIT_CHAIN_DOWNGRADE_WOULD_DISCARD_EVIDENCE';
    END IF;
END
$$;
DROP TRIGGER audit_chain_append ON audit_events;
CREATE TRIGGER audit_chain_append AFTER INSERT ON audit_events
    FOR EACH ROW EXECUTE FUNCTION audit_chain_append();
DROP FUNCTION audit_chain_append_v2();
DROP FUNCTION audit_chain_projection_v2(jsonb, bigint, timestamptz, bytea);
ALTER TABLE audit_chain_head DROP COLUMN scoped_from;
