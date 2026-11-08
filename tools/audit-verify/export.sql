-- Run with psql --no-psqlrc --tuples-only --no-align --set ON_ERROR_STOP=1.
SELECT CASE WHEN event.chain_position >= head.scoped_from
        THEN audit_chain_projection_v2(to_jsonb(event), event.chain_position, occurred_at, identifier_digest)
        ELSE audit_chain_projection(to_jsonb(event), event.chain_position, occurred_at, identifier_digest)
    END || jsonb_build_object('prev_hash', encode(event.prev_hash, 'hex'), 'hash', encode(event.hash, 'hex'))
    || CASE WHEN current_setting('ownsolid.audit_disclose_assistant', true) = 'on'
        AND event.chain_position >= head.scoped_from
        AND event.actor_user_id IS NULL AND event.subject_user_id IS NOT NULL
        -- The exact object is an allowlist and the full preimage of the commitment.
        AND event.metadata = '{"actor_id":"ownsolid.product-assistant","actor_kind":"product_assistant"}'::jsonb
        THEN event.metadata
        ELSE '{}'::jsonb
    END
FROM audit_events AS event CROSS JOIN audit_chain_head AS head
WHERE event.chain_position IS NOT NULL
ORDER BY event.chain_position;
