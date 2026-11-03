package engine

// ValidateRestoredDatabaseTask checks the fixed generated-input profile without
// contacting Docker. Scope journals use the same policy as SDK execution.
func (t DatabaseTask) ValidateRestoredDatabaseTask() error {
	if (!t.restoredQualificationInput() && !t.restoredAuthority()) || !t.valid() {
		return failure("OFFLINE_ENGINE_RESTORED_DATABASE_TASK_INVALID")
	}
	return nil
}

// Only these fixed qualification commands may consume the generated restore
// configuration. Fresh migration and bootstrap always retain their own input.
func (t DatabaseTask) restoredQualificationInput() bool {
	if t.VolumeOwner.Role != StoreRestoreConfigRole+"-"+t.Owner.OperationID {
		return false
	}
	switch t.Kind {
	case DatabaseSchemaCheck, DatabaseRuntimeRestorePrepare, DatabaseRuntimeCheck:
		return true
	default:
		return false
	}
}
