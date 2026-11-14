package release

import (
	"errors"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var restoreGeneration = regexp.MustCompile(`^[a-f0-9]{32}$`)
var restoreTenant = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
var restoreSQLIdentity = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// RestoredRuntimeContext is protected local operator input. The scope operator
// independently binds its backup/SQL facts and exact new target before writing
// it. It is neither a public request nor source-signature authenticity proof.
type RestoredRuntimeContext struct {
	FormatVersion        int    `json:"format_version"`
	RestoreID            string `json:"restore_id"`
	SourceScopeID        string `json:"source_scope_id"`
	TargetScopeID        string `json:"target_scope_id"`
	BackupManifestSHA256 string `json:"backup_manifest_sha256"`
	SQLArchiveSHA256     string `json:"sql_archive_sha256"`
	TenantID             string `json:"tenant_id"`
	SchemaVersion        int    `json:"schema_version"`
	TargetDatabase       string `json:"target_database"`
	TargetOperator       string `json:"target_operator"`
	Reason               string `json:"reason"`
	ApplicationProfile   string `json:"application_profile,omitempty"`
}

func (r *RestoredRuntimeContext) UnmarshalJSON(raw []byte) error {
	type plain RestoredRuntimeContext
	var value plain
	if r == nil || canonicalRestoreObject(raw, &value, false) != nil || !validPostgreSQLApplicationJSON(raw, value.ApplicationProfile) {
		return errors.New("RESTORED_RUNTIME_CONTEXT_JSON_INVALID")
	}
	*r = RestoredRuntimeContext(value)
	return nil
}

func (r RestoredRuntimeContext) Valid() bool {
	return r.EffectiveApplicationProfile() != "" && (r.EffectiveApplicationProfile() != MVPPostgreSQLCPUApplicationProfile || r.SchemaVersion == MVPPostgreSQLCPUV1SchemaVersion) && r.FormatVersion == 1 && restoreGeneration.MatchString(r.RestoreID) && restoreGeneration.MatchString(r.SourceScopeID) && restoreGeneration.MatchString(r.TargetScopeID) && r.SourceScopeID != r.TargetScopeID && ValidSHA256(r.BackupManifestSHA256) && ValidSHA256(r.SQLArchiveSHA256) && restoreTenant.MatchString(r.TenantID) && r.TenantID != "00000000-0000-0000-0000-000000000000" && r.SchemaVersion > 0 && restoreSQLIdentity.MatchString(r.TargetDatabase) && restoreSQLIdentity.MatchString(r.TargetOperator) && utf8.ValidString(r.Reason) && strings.TrimSpace(r.Reason) != "" && utf8.RuneCountInString(r.Reason) <= 500 && !strings.ContainsFunc(r.Reason, unicode.IsControl)
}
