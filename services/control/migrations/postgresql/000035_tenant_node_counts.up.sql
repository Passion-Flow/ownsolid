-- Live node counts (ARC-009, ADR 0020): enrollment reads the limit from one row instead of
-- counting managed_nodes, so its cost does not grow with the fleet. The trigger keeps the
-- count for every write path, and enrollment locks the row to serialize against the limit.
CREATE TABLE tenant_node_counts (
 site_id varchar(32) NOT NULL DEFAULT local_site_id(),
 cell_id varchar(32) NOT NULL DEFAULT local_cell_id(),
 tenant_id uuid NOT NULL REFERENCES tenants(id),
 live_nodes bigint NOT NULL CHECK(live_nodes>=0),
 PRIMARY KEY(site_id,cell_id,tenant_id)
);

INSERT INTO tenant_node_counts(site_id,cell_id,tenant_id,live_nodes)
 SELECT site_id,cell_id,tenant_id,count(*) FROM managed_nodes WHERE status<>'removed' GROUP BY site_id,cell_id,tenant_id;

CREATE FUNCTION tenant_node_count() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
 row_change managed_nodes := CASE WHEN TG_OP='DELETE' THEN OLD ELSE NEW END;
 change bigint := CASE WHEN TG_OP<>'DELETE' AND NEW.status<>'removed' THEN 1 ELSE 0 END
  - CASE WHEN TG_OP<>'INSERT' AND OLD.status<>'removed' THEN 1 ELSE 0 END;
BEGIN
 -- A decrease must not go through INSERT: its CHECK sees the proposed row first. The row
 -- exists, because the node being counted out was counted in.
 IF change>0 THEN
  INSERT INTO tenant_node_counts(site_id,cell_id,tenant_id,live_nodes) VALUES(row_change.site_id,row_change.cell_id,row_change.tenant_id,change)
   ON CONFLICT(site_id,cell_id,tenant_id) DO UPDATE SET live_nodes=tenant_node_counts.live_nodes+EXCLUDED.live_nodes;
 ELSIF change<0 THEN
  UPDATE tenant_node_counts SET live_nodes=live_nodes+change
   WHERE site_id=row_change.site_id AND cell_id=row_change.cell_id AND tenant_id=row_change.tenant_id;
 END IF;
 RETURN NULL;
END $$;

CREATE TRIGGER managed_nodes_live_count AFTER INSERT OR DELETE OR UPDATE OF status ON managed_nodes
 FOR EACH ROW EXECUTE FUNCTION tenant_node_count();
