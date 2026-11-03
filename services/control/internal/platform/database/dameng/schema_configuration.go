package dameng

import (
	"strings"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// SchemaConfiguration is owned by the optional native schema operator. It is
// not a serving selector and never reads policy from the ambient environment.
type SchemaConfiguration struct {
	FormatVersion        int    `json:"format_version"`
	Schema               string `json:"schema"`
	RuntimeUser          string `json:"runtime_user"`
	LockWaitSeconds      int    `json:"lock_wait_seconds"`
	StepSeconds          int    `json:"step_seconds"`
	TotalSeconds         int    `json:"total_seconds"`
	CleanupSeconds       int    `json:"cleanup_seconds"`
	MaximumMetadataRows  int    `json:"maximum_metadata_rows"`
	MaximumMetadataBytes int    `json:"maximum_metadata_bytes"`
}

func (SchemaConfiguration) String() string   { return "DAMENG_SCHEMA_CONFIGURATION_REDACTED" }
func (SchemaConfiguration) GoString() string { return "DAMENG_SCHEMA_CONFIGURATION_REDACTED" }
func (c SchemaConfiguration) Validate() error {
	if c.FormatVersion != 1 || !nativeIdentifier.MatchString(c.Schema) || !nativeIdentifier.MatchString(c.RuntimeUser) || c.Schema == c.RuntimeUser || strings.HasPrefix(c.Schema, "SYS") || strings.HasPrefix(c.RuntimeUser, "SYS") || c.LockWaitSeconds < 1 || c.LockWaitSeconds > 60 || c.StepSeconds < 1 || c.StepSeconds > 120 || c.TotalSeconds < c.LockWaitSeconds+c.StepSeconds || c.TotalSeconds > 1800 || c.CleanupSeconds < 1 || c.CleanupSeconds > 30 || c.MaximumMetadataRows < 512 || c.MaximumMetadataRows > maximumODBCRows || c.MaximumMetadataBytes < 16384 || c.MaximumMetadataBytes > 1<<20 {
		return fail("DAMENG_SCHEMA_CONFIGURATION_INVALID")
	}
	return nil
}
func LoadSchemaConfiguration(path, environmentFile string) (SchemaConfiguration, error) {
	load := func(name string) (SchemaConfiguration, error) {
		var c SchemaConfiguration
		raw, e := readODBCPrivateInput(name, MaximumConfigurationBytes)
		if e != nil || requiredObject(raw, 9) != nil || wire.StrictJSON(raw, &c) != nil || c.Validate() != nil {
			return SchemaConfiguration{}, fail("DAMENG_SCHEMA_CONFIGURATION_INVALID")
		}
		return c, nil
	}
	c, e := load(path)
	if e != nil || environmentFile == "" {
		return c, e
	}
	raw, e := readODBCPrivateInput(environmentFile, MaximumConfigurationBytes)
	if e != nil {
		return SchemaConfiguration{}, fail("DAMENG_SCHEMA_ENVIRONMENT_INVALID")
	}
	seen := false
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || k != "PROJECT_ONE_DAMENG_SCHEMA_CONFIGURATION_FILE" || seen || !canonicalPath(v) {
			return SchemaConfiguration{}, fail("DAMENG_SCHEMA_ENVIRONMENT_INVALID")
		}
		seen = true
		path = v
	}
	if !seen {
		return SchemaConfiguration{}, fail("DAMENG_SCHEMA_ENVIRONMENT_INVALID")
	}
	return load(path)
}
