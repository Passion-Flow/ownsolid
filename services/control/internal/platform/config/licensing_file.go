package config

// Preserve the licensing boundary's established safe diagnostic.
func readLicenseFile(path string, maximum int64) ([]byte, error) {
	return readPrivateFile(path, maximum, "CONFIG_LICENSING_PRIVATE_FILE_INVALID")
}
