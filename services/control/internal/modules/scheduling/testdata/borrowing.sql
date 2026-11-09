-- Candidate follow-on to quota.sql and reservations.sql for SCH-109.
ALTER TABLE scheduling_reservations
 ADD COLUMN loan_allocations jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(loan_allocations) = 'object'),
 ADD COLUMN borrowing_order bigint GENERATED ALWAYS AS IDENTITY UNIQUE;

CREATE TABLE scheduling_quota_reclaims (
  attempt_id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  workspace_id uuid NOT NULL,
  site_id varchar(32) NOT NULL DEFAULT local_site_id(),
  cell_id varchar(32) NOT NULL DEFAULT local_cell_id(),
  cohort_id text NOT NULL CHECK (length(cohort_id) > 0),
  kind varchar(16) NOT NULL CHECK (kind::text = ANY (ARRAY['training'::text, 'inference'::text, 'notebook'::text, 'batch'::text])),
  source_id varchar(128) NOT NULL CHECK (length(source_id) > 0),
  attempt_number integer NOT NULL CHECK (attempt_number > 0),
  intent jsonb NOT NULL CHECK (jsonb_typeof(intent) = 'object'),
  victims uuid[] NOT NULL CHECK (cardinality(victims) > 0),
  state varchar(16) NOT NULL DEFAULT 'pending' CHECK (state::text = ANY (ARRAY['pending'::text, 'admitted'::text, 'cancelled'::text])),
  FOREIGN KEY (tenant_id, workspace_id) REFERENCES workspaces (tenant_id, id),
  FOREIGN KEY (site_id) REFERENCES sites (id),
  FOREIGN KEY (cell_id) REFERENCES cells (id),
  UNIQUE (tenant_id, workspace_id, kind, source_id, attempt_number)
);
CREATE UNIQUE INDEX scheduling_quota_reclaims_pending_workspace
 ON scheduling_quota_reclaims (tenant_id, workspace_id) WHERE state='pending';
