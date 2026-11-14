-- Existing rows precede the chain. Keeping their columns null avoids rewriting
-- every retained partition and makes the coverage boundary explicit.
ALTER TABLE audit_events
    ADD COLUMN chain_position bigint,
    ADD COLUMN prev_hash bytea,
    ADD COLUMN hash bytea,
    ADD CONSTRAINT audit_events_chain_shape CHECK (
        (chain_position IS NULL AND prev_hash IS NULL AND hash IS NULL)
        OR (chain_position IS NOT NULL AND chain_position > 0
            AND prev_hash IS NOT NULL AND octet_length(prev_hash) = 32
            AND hash IS NOT NULL AND octet_length(hash) = 32)
    );

CREATE TABLE audit_chain_head (
    singleton boolean PRIMARY KEY CHECK (singleton),
    chain_position bigint NOT NULL CHECK (chain_position >= 0),
    hash bytea NOT NULL CHECK (octet_length(hash) = 32)
) WITH (fillfactor = 70);

INSERT INTO audit_chain_head(singleton, chain_position, hash)
VALUES (true, 0, decode(repeat('00', 32), 'hex'));

CREATE FUNCTION audit_chain_frame(value text) RETURNS bytea
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT CASE WHEN value IS NULL THEN int4send(-1)
        ELSE int4send(octet_length(convert_to(value, 'UTF8'))) || convert_to(value, 'UTF8')
    END
$$;

CREATE FUNCTION audit_chain_metadata(value jsonb) RETURNS bytea
LANGUAGE plpgsql STABLE STRICT PARALLEL SAFE AS $$
BEGIN
    CASE jsonb_typeof(value)
        WHEN 'null' THEN RETURN convert_to('n', 'UTF8');
        WHEN 'boolean' THEN
            RETURN convert_to(CASE WHEN value = 'true'::jsonb THEN 't' ELSE 'f' END, 'UTF8');
        WHEN 'string' THEN
            RETURN convert_to('s', 'UTF8') || audit_chain_frame(value #>> '{}');
        WHEN 'number' THEN
            RETURN convert_to('d', 'UTF8') || audit_chain_frame(trim_scale(value::numeric)::text);
        WHEN 'array' THEN
            RETURN (
                SELECT convert_to('a', 'UTF8') || int4send(count(*)::integer)
                    || coalesce(string_agg(audit_chain_metadata(item), ''::bytea ORDER BY ordinal), ''::bytea)
                FROM jsonb_array_elements(value) WITH ORDINALITY AS entries(item, ordinal)
            );
        WHEN 'object' THEN
            RETURN (
                SELECT convert_to('o', 'UTF8') || int4send(count(*)::integer)
                    || coalesce(string_agg(audit_chain_frame(key) || audit_chain_metadata(item), ''::bytea
                        ORDER BY convert_to(key, 'UTF8')), ''::bytea)
                FROM jsonb_each(value) AS entries(key, item)
            );
    END CASE;
END
$$;

-- The trigger and operator export share this projection. It commits to existing
-- diagnostic columns without putting their contents in the exported document.
CREATE FUNCTION audit_chain_projection(event jsonb, event_position bigint, occurred timestamptz, identifier bytea)
RETURNS jsonb LANGUAGE sql STABLE PARALLEL SAFE AS $$
    SELECT jsonb_build_object(
        'format', 'ownsolid.audit.v1',
        'position', event_position::text,
        'id', event ->> 'id',
        'occurred_at', to_char(occurred AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
        'actor_user_id', event ->> 'actor_user_id',
        'subject_user_id', event ->> 'subject_user_id',
        'tenant_id', event ->> 'tenant_id',
        'workspace_id', event ->> 'workspace_id',
        'audience', event ->> 'audience',
        'action', event ->> 'action',
        'outcome', event ->> 'outcome',
        'target_type', event ->> 'target_type',
        'target_id', event ->> 'target_id',
        'request_id', event ->> 'request_id',
        'reason_sha256', encode(sha256(convert_to(event ->> 'reason', 'UTF8')), 'hex'),
        'identifier_digest_sha256', encode(sha256(identifier), 'hex'),
        'metadata_sha256', encode(sha256(audit_chain_metadata(event -> 'metadata')), 'hex')
    )
$$;

CREATE FUNCTION audit_chain_append() RETURNS trigger
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
    projection := audit_chain_projection(to_jsonb(NEW), next_position, NEW.occurred_at, NEW.identifier_digest);

    SELECT sha256(convert_to('ownsolid.audit.v1', 'UTF8') || decode('00', 'hex') || predecessor
        || string_agg(audit_chain_frame(projection ->> field), ''::bytea ORDER BY ordinal))
    INTO STRICT digest
    FROM unnest(ARRAY[
        'position', 'id', 'occurred_at', 'actor_user_id', 'subject_user_id', 'tenant_id', 'workspace_id',
        'audience', 'action', 'outcome', 'target_type', 'target_id', 'request_id',
        'reason_sha256', 'identifier_digest_sha256', 'metadata_sha256'
    ]) WITH ORDINALITY AS fields(field, ordinal);

    UPDATE audit_events SET chain_position = next_position, prev_hash = predecessor, hash = digest
        WHERE id = NEW.id AND occurred_at = NEW.occurred_at AND chain_position IS NULL;
    GET DIAGNOSTICS updated = ROW_COUNT;
    IF updated <> 1 THEN
        RAISE EXCEPTION 'AUDIT_CHAIN_WRITE_UNCONFIRMED';
    END IF;
    UPDATE audit_chain_head SET chain_position = next_position, hash = digest WHERE singleton;
    RETURN NULL;
END
$$;

REVOKE ALL ON FUNCTION audit_chain_append() FROM PUBLIC;
REVOKE ALL ON TABLE audit_chain_head FROM PUBLIC;

-- AFTER INSERT only processes rows that survived ON CONFLICT DO NOTHING.
CREATE TRIGGER audit_chain_append AFTER INSERT ON audit_events
    FOR EACH ROW EXECUTE FUNCTION audit_chain_append();
