ALTER TABLE dataset_drafts
    ADD CONSTRAINT dataset_draft_import_scope_key
    UNIQUE (tenant_id, workspace_id, dataset_id, id, site_id, cell_id);

CREATE TABLE dataset_draft_import_candidates (
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    dataset_id uuid NOT NULL,
    draft_id uuid NOT NULL,
    site_id varchar(32) NOT NULL DEFAULT local_site_id(),
    cell_id varchar(32) NOT NULL DEFAULT local_cell_id(),
    source_format text NOT NULL CHECK (source_format IN ('csv', 'parquet')),
    report jsonb NOT NULL CHECK (jsonb_typeof(report) = 'object'),
    PRIMARY KEY (tenant_id, workspace_id, dataset_id, draft_id),
    CONSTRAINT dataset_draft_import_candidate_scope_fk
        FOREIGN KEY (tenant_id, workspace_id, dataset_id, draft_id, site_id, cell_id)
        REFERENCES dataset_drafts (tenant_id, workspace_id, dataset_id, id, site_id, cell_id)
);
