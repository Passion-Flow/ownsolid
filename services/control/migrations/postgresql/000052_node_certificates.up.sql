-- The keys of a node's client certificates (ARC-103, ADR 0050). A connection's key must be the
-- current one or, while a rotation has not been confirmed, the previous one.
ALTER TABLE managed_nodes
 ADD COLUMN certificate_key_sha256 bytea CHECK (certificate_key_sha256 IS NULL OR octet_length(certificate_key_sha256)=32),
 ADD COLUMN previous_certificate_key_sha256 bytea CHECK (previous_certificate_key_sha256 IS NULL OR octet_length(previous_certificate_key_sha256)=32),
 ADD COLUMN certificate_not_after timestamptz,
 ADD CONSTRAINT managed_nodes_previous_certificate_needs_current CHECK (previous_certificate_key_sha256 IS NULL OR certificate_key_sha256 IS NOT NULL);

-- Gateways hold the pins in their node views, so a change to them is a change to the view.
CREATE OR REPLACE FUNCTION node_view_changed() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN
  INSERT INTO control_outbox(topic,key) VALUES('node.view_changed',OLD.id::text);
 ELSIF OLD.status IS DISTINCT FROM NEW.status OR OLD.credential_hash IS DISTINCT FROM NEW.credential_hash
  OR OLD.protocol_version IS DISTINCT FROM NEW.protocol_version OR OLD.execution_profile IS DISTINCT FROM NEW.execution_profile
  OR OLD.capabilities IS DISTINCT FROM NEW.capabilities OR OLD.workspace_id IS DISTINCT FROM NEW.workspace_id
  OR OLD.certificate_key_sha256 IS DISTINCT FROM NEW.certificate_key_sha256
  OR OLD.previous_certificate_key_sha256 IS DISTINCT FROM NEW.previous_certificate_key_sha256 THEN
  INSERT INTO control_outbox(topic,key) VALUES('node.view_changed',NEW.id::text);
 END IF;
 RETURN NULL;
END $$;

DROP TRIGGER managed_nodes_view_changed ON managed_nodes;
CREATE TRIGGER managed_nodes_view_changed AFTER DELETE OR UPDATE OF status,credential_hash,protocol_version,execution_profile,capabilities,workspace_id,certificate_key_sha256,previous_certificate_key_sha256
 ON managed_nodes FOR EACH ROW EXECUTE FUNCTION node_view_changed();
