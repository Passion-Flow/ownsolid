package config

// MVPPostgreSQLCPUV1Profile selects the cumulative full graph and its independent
// account22-derived PostgreSQL lineage. It never selects the bounded account graph.
const MVPPostgreSQLCPUV1Profile = "mvp-postgresql-cpu-v1"

func (c Config) MVPPostgreSQLCPUV1Only() bool { return c.Server.Profile() == MVPPostgreSQLCPUV1Profile }

// FullPlatformRuntime is an owner requirement, not a schema alias. Optional
// integrations still require their existing explicit typed enable/configuration.
func (c Config) FullPlatformRuntime() bool {
	return c.Server.Profile() == "platform" || c.MVPPostgreSQLCPUV1Only()
}

// DecodeWithEnvironment validates an immutable configuration snapshot and the
// same explicit protected policy file used by LoadProtectedWithEnvironment.
// The caller owns reading/protecting the snapshot; ambient values have no authority.
func DecodeWithEnvironment(raw []byte, environmentFile string) (Config, error) {
	return decodeWithEnvironment(raw, environmentFile)
}
