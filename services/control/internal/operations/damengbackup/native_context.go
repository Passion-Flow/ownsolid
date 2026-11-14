package damengbackup

import "strings"

const NativeContextEnvironment = "PROJECT_ONE_DAMENG_RECOVERY_NATIVE_CONTEXT_FILE"

// SnapshotPolicy limits the independent every-row/LOB reader. Native statement,
// lock and cleanup timeouts remain owned by existing connector/schema settings;
// the recovery operation deadline is still Configuration.Policy.OperationSeconds.
type SnapshotPolicy struct {
	PageRows                 int   `json:"page_rows"`
	MaximumRows              int64 `json:"maximum_rows"`
	MaximumBytes             int64 `json:"maximum_bytes"`
	MaximumLOBBytes          int64 `json:"maximum_lob_bytes"`
	LOBChunkSize             int   `json:"lob_chunk_size"`
	MaximumTreeBytes         int64 `json:"maximum_tree_bytes"`
	MaximumTreeFiles         int   `json:"maximum_tree_files"`
	MaximumTreeMetadataBytes int64 `json:"maximum_tree_metadata_bytes"`
}

func (p SnapshotPolicy) Validate() error {
	if p.PageRows < 1 || p.PageRows > 1024 || p.MaximumRows < 1 || p.MaximumRows > 1<<40 || p.MaximumBytes < 1<<20 || p.MaximumBytes > 1<<50 || p.MaximumLOBBytes < 1 || p.MaximumLOBBytes > p.MaximumBytes || p.LOBChunkSize < 256 || p.LOBChunkSize > 4096 || p.MaximumTreeBytes < 1 || p.MaximumTreeBytes > 1<<50 || p.MaximumTreeFiles < 1 || p.MaximumTreeFiles > 10000000 || p.MaximumTreeMetadataBytes < 4096 || p.MaximumTreeMetadataBytes > 1<<30 {
		return fail("SNAPSHOT_POLICY_INVALID")
	}
	return nil
}

// NativeContextConfiguration references the existing native owners. It neither
// duplicates their settings nor reads ambient environment variables.
type NativeContextConfiguration struct {
	FormatVersion              int            `json:"format_version"`
	ConnectorConfigurationFile string         `json:"connector_configuration_file"`
	SchemaConfigurationFile    string         `json:"schema_configuration_file"`
	Snapshot                   SnapshotPolicy `json:"snapshot"`
}

func (NativeContextConfiguration) String() string   { return "DAMENG_RECOVERY_NATIVE_CONTEXT_REDACTED" }
func (NativeContextConfiguration) GoString() string { return "DAMENG_RECOVERY_NATIVE_CONTEXT_REDACTED" }
func (c NativeContextConfiguration) Validate() error {
	if c.FormatVersion != 1 || !canonical(c.ConnectorConfigurationFile) || !canonical(c.SchemaConfigurationFile) || c.ConnectorConfigurationFile == c.SchemaConfigurationFile || c.Snapshot.Validate() != nil {
		return fail("NATIVE_CONTEXT_INVALID")
	}
	return nil
}
func LoadNativeContext(path, environmentFile string) (NativeContextConfiguration, error) {
	load := func(path string) (NativeContextConfiguration, error) {
		var c NativeContextConfiguration
		raw, e := readPrivate(path, maximumDocumentBytes)
		if e != nil || strictJSON(raw, &c) != nil || c.Validate() != nil {
			return c, fail("NATIVE_CONTEXT_INVALID")
		}
		return c, nil
	}
	c, e := load(path)
	if e != nil || environmentFile == "" {
		return c, e
	}
	raw, e := readPrivate(environmentFile, maximumDocumentBytes)
	if e != nil {
		return NativeContextConfiguration{}, fail("NATIVE_CONTEXT_ENVIRONMENT_INVALID")
	}
	replacement := ""
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || k != NativeContextEnvironment || replacement != "" || !canonical(v) {
			return NativeContextConfiguration{}, fail("NATIVE_CONTEXT_ENVIRONMENT_INVALID")
		}
		replacement = v
	}
	if replacement == "" {
		return NativeContextConfiguration{}, fail("NATIVE_CONTEXT_ENVIRONMENT_INVALID")
	}
	return load(replacement)
}
