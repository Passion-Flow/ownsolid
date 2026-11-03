package databaseaccess

// mysqlVariantTables is the independent MySQL204–265 active schema extension.
// Keep PostgreSQL's catalog and the shared M01–M19 table set unchanged. Runtime
// grants remain DML-only; registering these tables does not prove full serving.
var mysqlVariantTables = [...]string{
	"artifact_object_grants",
	"artifact_object_ingestions",
	"artifact_object_parts",
	"artifact_objects",
	"aws_discovered_resources",
	"aws_import_receipts",
	"image_registry_blobs",
	"image_registry_deployment_refs",
	"image_registry_grants",
	"image_registry_imports",
	"image_registry_mutations",
	"image_registry_offline_refs",
	"image_registry_pins",
	"middleware_instances",
	"middleware_operations",
	"mongodb_dataset_imports",
	"private_cluster_connections",
	"private_cluster_intents",
	"private_cluster_runs",
	"task_event_dead_letters",
	"task_event_outbox",
	"task_event_receipts",
}
