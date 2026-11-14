-- Candidate cell-scoped table for the owner migration handoff.
CREATE TABLE scheduling_reservations (
  attempt_id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  workspace_id uuid NOT NULL,
  site_id varchar(32) NOT NULL DEFAULT local_site_id(),
  cell_id varchar(32) NOT NULL DEFAULT local_cell_id(),
  kind varchar(16) NOT NULL CHECK (kind::text = ANY (ARRAY['training'::text, 'inference'::text, 'notebook'::text, 'batch'::text])),
  source_id varchar(128) NOT NULL CHECK (length(source_id) > 0),
  attempt_number integer NOT NULL CHECK (attempt_number > 0),
  quota_revision bigint NOT NULL CHECK (quota_revision > 0),
  intent jsonb NOT NULL CHECK (jsonb_typeof(intent) = 'object'),
  demand jsonb NOT NULL CHECK (jsonb_typeof(demand) = 'object' AND demand <> '{}'::jsonb),
  reserved_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  released_at timestamptz,
  FOREIGN KEY (tenant_id, workspace_id) REFERENCES workspaces (tenant_id, id),
  FOREIGN KEY (site_id) REFERENCES sites (id),
  FOREIGN KEY (cell_id) REFERENCES cells (id),
  UNIQUE (tenant_id, workspace_id, kind, source_id, attempt_number)
);
CREATE UNIQUE INDEX scheduling_reservations_active_job
 ON scheduling_reservations (tenant_id, workspace_id, kind, source_id)
 WHERE released_at IS NULL;
