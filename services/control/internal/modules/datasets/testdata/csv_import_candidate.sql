CREATE TABLE dataset_csv_import_candidates (
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    dataset_id uuid NOT NULL,
    draft_id uuid NOT NULL,
    report jsonb NOT NULL CHECK (jsonb_typeof(report) = 'object'),
    PRIMARY KEY (tenant_id, workspace_id, dataset_id, draft_id),
    FOREIGN KEY (tenant_id, workspace_id, dataset_id, draft_id)
        REFERENCES dataset_drafts(tenant_id, workspace_id, dataset_id, id)
);
