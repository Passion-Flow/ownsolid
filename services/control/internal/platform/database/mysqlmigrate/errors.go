package mysqlmigrate

// Fixed safe operator error codes never include SQL,credentials or vendor text.
const (
	CodeDataOutcomeUnknown       = "MYSQL_MIGRATION_DATA_OUTCOME_UNKNOWN"
	CodeCatalogInvalid           = "MYSQL_MIGRATION_CATALOG_INVALID"
	CodeChecksumMismatch         = "MYSQL_MIGRATION_CHECKSUM_MISMATCH"
	CodeConfigurationInvalid     = "MYSQL_MIGRATION_CONFIGURATION_INVALID"
	CodeDatabaseInvalid          = "MYSQL_MIGRATION_DATABASE_INVALID"
	CodeDdlOutcomeUnknown        = "MYSQL_MIGRATION_DDL_OUTCOME_UNKNOWN"
	CodeDefinitionDrift          = "MYSQL_MIGRATION_DEFINITION_DRIFT"
	CodeJournalDefinitionInvalid = "MYSQL_MIGRATION_JOURNAL_DEFINITION_INVALID"
	CodeJournalInvalid           = "MYSQL_MIGRATION_JOURNAL_INVALID"
	CodeJournalOutcomeUnknown    = "MYSQL_MIGRATION_JOURNAL_OUTCOME_UNKNOWN"
	CodeLockOutcomeUnknown       = "MYSQL_MIGRATION_LOCK_OUTCOME_UNKNOWN"
	CodeLockReleaseUnconfirmed   = "MYSQL_MIGRATION_LOCK_RELEASE_UNCONFIRMED"
	CodeLockTimeout              = "MYSQL_MIGRATION_LOCK_TIMEOUT"
	CodeMetadataInvalid          = "MYSQL_MIGRATION_METADATA_INVALID"
	CodeOptionsInvalid           = "MYSQL_MIGRATION_OPTIONS_INVALID"
	CodeReconciliationRequired   = "MYSQL_MIGRATION_RECONCILIATION_REQUIRED"
	CodeSchemaNotReady           = "MYSQL_MIGRATION_SCHEMA_NOT_READY"
	CodeServerContextInvalid     = "MYSQL_MIGRATION_SERVER_CONTEXT_INVALID"
	CodeSessionInvalid           = "MYSQL_MIGRATION_SESSION_INVALID"
	CodeUnjournaledObject        = "MYSQL_MIGRATION_UNJOURNALED_OBJECT"
	CodeTriggerContextInvalid    = "MYSQL_MIGRATION_TRIGGER_CONTEXT_INVALID"
)
