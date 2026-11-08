package release

import "encoding/json"

const PostgreSQLLegacyApplicationProfile = "releases"
const MVPPostgreSQLCPUApplicationProfile = "mvp-postgresql-cpu-v1"
const MVPPostgreSQLCPUV1SchemaVersion = 56
const MVPPostgreSQLCPUV1TableCount = 116

func effectivePostgreSQLApplicationProfile(profile string) string {
	switch profile {
	case "", PostgreSQLLegacyApplicationProfile:
		return PostgreSQLLegacyApplicationProfile
	case MVPPostgreSQLCPUApplicationProfile:
		return MVPPostgreSQLCPUApplicationProfile
	default:
		return ""
	}
}

// An absent selector preserves legacy bytes. An explicitly supplied empty value
// must not be confused with omission and silently select another application.
func validPostgreSQLApplicationJSON(raw []byte, profile string) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil || effectivePostgreSQLApplicationProfile(profile) == "" {
		return false
	}
	_, present := fields["application_profile"]
	return !present || profile != ""
}

func (r PostgreSQLBackupRequest) EffectiveApplicationProfile() string {
	return effectivePostgreSQLApplicationProfile(r.ApplicationProfile)
}

func (r PostgreSQLRestoreRequest) EffectiveApplicationProfile() string {
	return effectivePostgreSQLApplicationProfile(r.ApplicationProfile)
}

func (r RestoredRuntimeContext) EffectiveApplicationProfile() string {
	return effectivePostgreSQLApplicationProfile(r.ApplicationProfile)
}
