package release

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// PostgreSQLRestoreRequest selects only a newly provisioned isolated target.
// The caller separately proves current namespace/Lease/claim/image authority.
type PostgreSQLRestoreRequest struct {
	FormatVersion            int                     `json:"format_version"`
	RestoreID                string                  `json:"restore_id"`
	TargetScopeID            string                  `json:"target_scope_id"`
	SourceManifestSHA256     string                  `json:"source_manifest_sha256"`
	BackupRoot               string                  `json:"backup_root"`
	RestoreRoot              string                  `json:"restore_root"`
	TargetDatabase           string                  `json:"target_database"`
	TargetOperator           string                  `json:"target_operator"`
	RuntimeConfigurationFile string                  `json:"runtime_configuration_file"`
	RuntimeEnvironmentFile   string                  `json:"runtime_environment_file,omitempty"`
	ConfigurationRoot        string                  `json:"configuration_root"`
	Reason                   string                  `json:"reason"`
	Policy                   PostgreSQLBackupPolicy  `json:"policy"`
	TargetRoots              []PostgreSQLRestoreRoot `json:"target_roots,omitempty"`
	ApplicationProfile       string                  `json:"application_profile,omitempty"`
}

func (r PostgreSQLRestoreRequest) Valid() bool {
	return r.EffectiveApplicationProfile() != "" && r.FormatVersion == 1 && restoreGeneration.MatchString(r.RestoreID) && restoreGeneration.MatchString(r.TargetScopeID) && r.RestoreID != r.TargetScopeID &&
		ValidSHA256(r.SourceManifestSHA256) && PostgreSQLBackupPath(r.BackupRoot) && PostgreSQLBackupPath(r.RestoreRoot) && r.BackupRoot != r.RestoreRoot &&
		restoreSQLIdentity.MatchString(r.TargetDatabase) && restoreSQLIdentity.MatchString(r.TargetOperator) && PostgreSQLBackupPath(r.RuntimeConfigurationFile) &&
		(r.RuntimeEnvironmentFile == "" || PostgreSQLBackupPath(r.RuntimeEnvironmentFile)) && PostgreSQLBackupPath(r.ConfigurationRoot) &&
		utf8.ValidString(r.Reason) && strings.TrimSpace(r.Reason) != "" && utf8.RuneCountInString(r.Reason) <= 500 && !strings.ContainsFunc(r.Reason, unicode.IsControl) && r.Policy.Valid() && ValidPostgreSQLRestoreRoots(r.TargetRoots)
}
