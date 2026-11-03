CREATE INDEX private_cluster_history ON private_cluster_runs(tenant_id,workspace_id,created_at DESC,id DESC);
