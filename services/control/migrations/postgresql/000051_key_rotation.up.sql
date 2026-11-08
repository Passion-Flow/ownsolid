-- A rotated key names the key it replaces, and a key is replaced at most once, so two people
-- rotating the same key at once cannot both win (GW-113, docs/research/gateway-keys.md).
ALTER TABLE api_keys ADD COLUMN rotated_from uuid;
ALTER TABLE api_keys ADD CONSTRAINT api_key_rotated_from_fk FOREIGN KEY(tenant_id,workspace_id,rotated_from) REFERENCES api_keys(tenant_id,workspace_id,id);
ALTER TABLE api_keys ADD CONSTRAINT api_key_not_its_own_successor CHECK(rotated_from IS NULL OR rotated_from<>id);
CREATE UNIQUE INDEX api_key_one_successor_idx ON api_keys(rotated_from) WHERE rotated_from IS NOT NULL;
-- One key's calls, newest first, for the key's call log.
CREATE INDEX gateway_request_key_idx ON gateway_requests(tenant_id,workspace_id,api_key_id,admitted_at DESC,id DESC);
