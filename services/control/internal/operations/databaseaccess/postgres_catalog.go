package databaseaccess

import "sort"

// These first-variant tables have real PostgreSQL migrations. Their later MySQL
// combinations remain unsupported; no grant or table support is fabricated.
var postgreSQLVariantTables = [...]string{
	"artifact_object_grants", "artifact_object_ingestions", "artifact_object_parts", "artifact_objects",
	"aws_discovered_resources", "aws_import_receipts", "mongodb_dataset_imports",
	"image_registry_blobs", "image_registry_deployment_refs", "image_registry_grants", "image_registry_imports",
	"image_registry_mutations", "image_registry_offline_refs", "image_registry_pins",
	"task_event_dead_letters", "task_event_outbox", "task_event_receipts",
	"middleware_instances", "middleware_operations",
	"private_cluster_connections", "private_cluster_runs", "private_cluster_intents",
}

func postgreSQLTables() []string {
	names := append([]string{}, applicationTables[:]...)
	names = append(names, postgreSQLVariantTables[:]...)
	names = append(names, "cells", "sites")
	sort.Strings(names)
	return names
}
