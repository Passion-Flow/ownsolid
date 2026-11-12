-- Roll back scope admission while preserving all V1 and V2 positions and hashes.
LOCK TABLE audit_events IN SHARE ROW EXCLUSIVE MODE;

CREATE OR REPLACE FUNCTION audit_chain_append_v2() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
DECLARE
    next_position bigint;
    predecessor bytea;
    projection jsonb;
    digest bytea;
    updated integer;
BEGIN
    IF NEW.chain_position IS NOT NULL OR NEW.prev_hash IS NOT NULL OR NEW.hash IS NOT NULL THEN
        RAISE EXCEPTION 'AUDIT_CHAIN_FIELDS_ASSIGNED_BY_DATABASE';
    END IF;
    IF NEW.occurred_at < '0001-01-01T00:00:00Z'::timestamptz
        OR NEW.occurred_at >= '10000-01-01T00:00:00Z'::timestamptz THEN
        RAISE EXCEPTION 'AUDIT_CHAIN_TIMESTAMP_OUT_OF_RANGE';
    END IF;

    -- Only the owner can bind this function as a trigger. Use the schema of
    -- that relation, never the writer's search path, for internal helpers.
    PERFORM set_config('search_path', format('pg_catalog, %I, pg_temp', TG_TABLE_SCHEMA), true);
    SELECT chain_position + 1, hash INTO STRICT next_position, predecessor
        FROM audit_chain_head WHERE singleton FOR UPDATE;
    projection := audit_chain_projection_v2(to_jsonb(NEW), next_position, NEW.occurred_at, NEW.identifier_digest);

    SELECT sha256(convert_to('ownsolid.audit.v2', 'UTF8') || decode('00', 'hex') || predecessor
        || string_agg(audit_chain_frame(projection ->> field), ''::bytea ORDER BY ordinal))
    INTO STRICT digest
    FROM unnest(ARRAY[
        'position', 'id', 'occurred_at', 'actor_user_id', 'subject_user_id', 'tenant_id', 'workspace_id',
        'audience', 'action', 'outcome', 'target_type', 'target_id', 'request_id',
        'reason_sha256', 'identifier_digest_sha256', 'metadata_sha256', 'site_id', 'cell_id'
    ]) WITH ORDINALITY AS fields(field, ordinal);

    UPDATE audit_events SET chain_position = next_position, prev_hash = predecessor, hash = digest
        WHERE id = NEW.id AND occurred_at = NEW.occurred_at AND chain_position IS NULL;
    GET DIAGNOSTICS updated = ROW_COUNT;
    IF updated <> 1 THEN
        RAISE EXCEPTION 'AUDIT_CHAIN_WRITE_UNCONFIRMED';
    END IF;
    UPDATE audit_chain_head SET chain_position = next_position, hash = digest,
        scoped_from = coalesce(scoped_from, next_position) WHERE singleton;
    RETURN NULL;
END
$$;

ALTER TABLE audit_chain_head DROP COLUMN site_id, DROP COLUMN cell_id;
