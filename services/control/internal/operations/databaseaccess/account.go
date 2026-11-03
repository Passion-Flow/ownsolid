package databaseaccess

// The account grant catalog has exactly the checksummed account schema owners.
func runtimeTables(input RuntimeInput) []string {
	if !input.configuration.AccountRuntime() {
		return postgreSQLTables()
	}
	tables := []string{"tenants", "workspaces", "users", "password_credentials", "workspace_memberships", "installation_state", "sessions", "invitations", "password_reset_tokens", "mail_deliveries", "audit_events", "rate_limit_buckets", "idempotency_records", "platform_license_state", "platform_license_artifacts"}
	if input.configuration.WorkspaceRuntime() {
		tables = append(tables, "workspace_notes")
	}
	if input.configuration.NodeRuntime() {
		tables = append(tables, "node_join_tokens", "managed_nodes", "node_tasks")
	}
	if input.configuration.ComponentRuntime() {
		tables = append(tables, "managed_components")
	}
	if input.configuration.MaintenanceRuntime() {
		tables = append(tables, "component_log_snapshots")
	}
	if input.configuration.ModelsRuntime() {
		tables = append(tables, "model_versions", "model_imports", "model_import_chunks", "inference_nodes", "deployments", "gpu_leases")
	}
	if input.configuration.GatewayRuntime() {
		tables = append(tables, "gateway_instances", "model_aliases", "model_alias_history", "api_keys", "gateway_requests", "gateway_recoveries", "model_alias_target_versions")
	}
	if input.configuration.DatasetRuntime() {
		tables = append(tables, "datasets", "dataset_drafts", "dataset_rows", "dataset_row_history", "dataset_jobs", "dataset_job_attempts", "dataset_versions")
	}
	if input.configuration.TrainingRuntime() {
		tables = append(tables, "training_jobs", "training_attempts", "training_events", "training_checkpoints", "training_artifacts", "training_recoveries", "training_uploads", "training_upload_chunks")
	}
	if input.configuration.LifecycleRuntime() {
		tables = append(tables, "model_holdouts", "model_holdout_sources", "model_holdout_prompts", "model_preparations", "model_preparation_attempts", "model_preparation_events", "model_preparation_recoveries", "model_preparation_uploads", "model_preparation_upload_chunks", "model_preparation_artifacts", "model_preparation_reviews", "model_derivations", "model_alias_cutover_intents")
	}
	if input.configuration.SSORuntime() {
		tables = append(tables, "sso_connectors", "sso_bindings", "sso_flows", "sso_mutation_intents")
	}
	if input.configuration.KnowledgeRuntime() {
		tables = append(tables, "knowledge_bases", "knowledge_generations", "knowledge_documents", "knowledge_source_versions", "knowledge_attempts", "knowledge_chunks", "knowledge_mutation_intents")
	}
	if input.configuration.WorkflowsRuntime() {
		tables = append(tables, "workflows", "workflow_versions", "workflow_conversations", "workflow_runs", "workflow_node_traces", "workflow_mutation_intents")
	}
	if input.configuration.PluginsRuntime() {
		tables = append(tables, "plugin_versions", "plugin_grants", "plugin_mutation_intents", "workflow_plugin_references")
	}
	if input.configuration.PortalRuntime() {
		tables = append(tables, "user_preferences", "terminal_notifications")
	}
	return tables
}
