-- Run with psql --no-psqlrc --tuples-only --no-align --set ON_ERROR_STOP=1.
SELECT audit_chain_projection(to_jsonb(event), chain_position, occurred_at, identifier_digest)
    || jsonb_build_object('prev_hash', encode(prev_hash, 'hex'), 'hash', encode(hash, 'hex'))
FROM audit_events AS event
WHERE chain_position IS NOT NULL
ORDER BY chain_position;
