-- Each gateway answers repeated heartbeats from a view of the node held in memory (ARC-101, ADR 0047).
-- These triggers tell it what changed, in the transaction of the change, so no writer can forget.
-- Only the columns of the view count: writes that repeat a value, as heartbeats do, append nothing.
CREATE FUNCTION node_view_changed() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN
  INSERT INTO control_outbox(topic,key) VALUES('node.view_changed',OLD.id::text);
 ELSIF OLD.status IS DISTINCT FROM NEW.status OR OLD.credential_hash IS DISTINCT FROM NEW.credential_hash
  OR OLD.protocol_version IS DISTINCT FROM NEW.protocol_version OR OLD.execution_profile IS DISTINCT FROM NEW.execution_profile
  OR OLD.capabilities IS DISTINCT FROM NEW.capabilities OR OLD.workspace_id IS DISTINCT FROM NEW.workspace_id THEN
  INSERT INTO control_outbox(topic,key) VALUES('node.view_changed',NEW.id::text);
 END IF;
 RETURN NULL;
END $$;

CREATE TRIGGER managed_nodes_view_changed AFTER DELETE OR UPDATE OF status,credential_hash,protocol_version,execution_profile,capabilities,workspace_id
 ON managed_nodes FOR EACH ROW EXECUTE FUNCTION node_view_changed();

-- A workspace or tenant holds many nodes and changes rarely, so its event drops every view.
CREATE FUNCTION node_views_changed() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.status IS DISTINCT FROM NEW.status THEN
  INSERT INTO control_outbox(topic,key) VALUES('node.views_changed',NEW.id::text);
 END IF;
 RETURN NULL;
END $$;

CREATE TRIGGER workspaces_views_changed AFTER UPDATE OF status ON workspaces FOR EACH ROW EXECUTE FUNCTION node_views_changed();
CREATE TRIGGER tenants_views_changed AFTER UPDATE OF status ON tenants FOR EACH ROW EXECUTE FUNCTION node_views_changed();
