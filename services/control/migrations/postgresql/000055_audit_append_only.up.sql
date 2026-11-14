-- The effective owner also executes the chain trigger's same-transaction
-- update. The serving role can append events and read the resulting hashes.
CREATE FUNCTION audit_events_require_owner() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog, pg_temp AS $$
BEGIN
    IF current_user <> (SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid = TG_RELID) THEN
        RAISE EXCEPTION USING ERRCODE = '42501', MESSAGE = 'AUDIT_EVENTS_APPEND_ONLY';
    END IF;
    IF TG_OP = 'UPDATE' THEN
        RETURN NEW;
    END IF;
    RETURN OLD;
END
$$;

REVOKE ALL ON FUNCTION audit_events_require_owner() FROM PUBLIC;
CREATE TRIGGER audit_events_require_owner_for_mutation
    BEFORE UPDATE OR DELETE ON audit_events
    FOR EACH ROW EXECUTE FUNCTION audit_events_require_owner();
CREATE TRIGGER audit_events_require_owner_for_truncate
    BEFORE TRUNCATE ON audit_events
    FOR EACH STATEMENT EXECUTE FUNCTION audit_events_require_owner();
