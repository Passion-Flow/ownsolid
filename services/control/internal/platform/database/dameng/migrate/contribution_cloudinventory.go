package migrate

// Cloud inventory is an append-only addition after the retained 454-step
// catalog. Provider access remains with cloudinventory; these objects grant no
// AWS authority and contain only the selected Workspace's SQL observations.
func cloudInventoryFragment(current map[string]table) schemaFragment {
	b := newInfrastructureFragment("cloudinventory_v2", current, "foundation_v1", "account_license_v2")
	b.create(nativeTable("AWS_DISCOVERED_RESOURCES", []column{
		uuid("ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false),
		text("ACCOUNT_ID", 12, false), text("REGION", 64, false), text("INSTANCE_ID", 32, false),
		col("OBSERVATION", "CLOB", 0, false, ""), stamp("OBSERVED_AT", false, false), col("MISSING", "BIT", 0, false, "0"),
	}, pk("AWS_RESOURCES_PK", "ID"), b.scope("AWS_RESOURCES_WORKSPACE_FK"),
		unique("AWS_RESOURCES_INSTANCE_UK", "TENANT_ID", "WORKSPACE_ID", "ACCOUNT_ID", "REGION", "INSTANCE_ID"),
		check("AWS_RESOURCE_ACCOUNT", `REGEXP_LIKE("ACCOUNT_ID",'^[0-9]{12}$','c')`),
		check("AWS_RESOURCE_OBSERVATION", `JSON_TYPE("OBSERVATION") IS NOT NULL AND JSON_TYPE("OBSERVATION")='OBJECT'`)))
	b.index("AWS_RESOURCE_SCOPE", "AWS_DISCOVERED_RESOURCES", []string{"TENANT_ID", "WORKSPACE_ID", "ID"}, nil, false)
	b.create(nativeTable("AWS_IMPORT_RECEIPTS", []column{
		uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("IDEMPOTENCY_KEY", false), uuid("ACTOR_ID", false),
		text("CONNECTION_NAME", 48, false), text("ACCOUNT_ID", 12, false), text("REGION", 64, false),
		stamp("STARTED_AT", false, false), stamp("COMPLETED_AT", false, false), col("IMPORTED_COUNT", "INTEGER", 0, false, ""),
	}, pk("AWS_RECEIPTS_PK", "TENANT_ID", "WORKSPACE_ID", "IDEMPOTENCY_KEY"),
		b.scope("AWS_RECEIPTS_WORKSPACE_FK"), b.user("AWS_RECEIPTS_ACTOR_FK", "ACTOR_ID"),
		check("AWS_RECEIPT_ACCOUNT", `REGEXP_LIKE("ACCOUNT_ID",'^[0-9]{12}$','c')`),
		check("AWS_RECEIPT_COMPLETION", `"COMPLETED_AT">="STARTED_AT"`),
		check("AWS_RECEIPT_COUNT", `"IMPORTED_COUNT">=0`)))
	b.index("AWS_RECEIPT_SNAPSHOT", "AWS_IMPORT_RECEIPTS", []string{"TENANT_ID", "WORKSPACE_ID", "ACCOUNT_ID", "REGION", "STARTED_AT"}, nil, false)
	b.guard("AWS_RECEIPT_IMMUTABLE", "AWS_IMPORT_RECEIPTS", "UPDATE OR DELETE", "1=1", "AWS_RECEIPT_IMMUTABLE")
	return b.fragment
}
