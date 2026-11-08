-- Run with psql --no-psqlrc --tuples-only --no-align --set ON_ERROR_STOP=1.
SELECT CASE WHEN event.chain_position >= head.scoped_from
        THEN audit_chain_projection_v2(to_jsonb(event), event.chain_position, occurred_at, identifier_digest)
        ELSE audit_chain_projection(to_jsonb(event), event.chain_position, occurred_at, identifier_digest)
    END || jsonb_build_object('prev_hash', encode(event.prev_hash, 'hex'), 'hash', encode(event.hash, 'hex'))
FROM audit_events AS event CROSS JOIN audit_chain_head AS head
WHERE event.chain_position IS NOT NULL
ORDER BY event.chain_position;
