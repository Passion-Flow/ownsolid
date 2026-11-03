package migrate

// InstallationLockID preserves the migration owner's validated lock namespace.
// Callers must validate SchemaConfiguration before borrowing a native session.
// Invalid input returns -1, outside every valid nonnegative installation ID;
// callers needing an error should use SchemaLockID directly.
func InstallationLockID(schema string) int64 {
	id, err := SchemaLockID(schema)
	if err != nil {
		return -1
	}
	return id
}
