package release

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// PostgreSQLBackupPolicy is the one native matched-backup execution policy.
// Tool paths identify fixed PostgreSQL programs, never arbitrary commands.
type PostgreSQLBackupPolicy struct {
	DumpFile                  string `json:"dump_file"`
	DumpSHA256                string `json:"dump_sha256"`
	RestoreFile               string `json:"restore_file"`
	RestoreSHA256             string `json:"restore_sha256"`
	ControlFile               string `json:"control_file"`
	ControlSHA256             string `json:"control_sha256"`
	ExpectedVersion           string `json:"expected_version"`
	ClientDistribution        string `json:"client_distribution,omitempty"`
	ScratchRoot               string `json:"scratch_root"`
	OperationSeconds          int    `json:"operation_seconds"`
	TerminationGraceSeconds   int    `json:"termination_grace_seconds"`
	MaximumStoreBytes         int64  `json:"maximum_store_bytes"`
	MaximumStoreFiles         int    `json:"maximum_store_files"`
	MaximumSQLBytes           int64  `json:"maximum_sql_bytes"`
	MaximumConfigurationBytes int64  `json:"maximum_configuration_bytes"`
	MaximumManifestBytes      int64  `json:"maximum_manifest_bytes"`
	MinimumFreeBytes          int64  `json:"minimum_free_bytes"`
}

// Preserve the existing scalar/optional policy shape while refusing case aliases
// that encoding/json would otherwise combine into contradictory typed settings.
func (p *PostgreSQLBackupPolicy) UnmarshalJSON(raw []byte) error {
	bad := errors.New("POSTGRES_BACKUP_POLICY_JSON_INVALID")
	if p == nil {
		return bad
	}
	type plain PostgreSQLBackupPolicy
	var value plain
	var fields map[string]json.RawMessage
	if StrictJSON(raw, &value) != nil || json.Unmarshal(raw, &fields) != nil || fields == nil {
		return bad
	}
	allowed := map[string]bool{}
	shape := reflect.TypeOf(value)
	for i := 0; i < shape.NumField(); i++ {
		name, _, _ := strings.Cut(shape.Field(i).Tag.Get("json"), ",")
		allowed[name] = true
	}
	for name := range fields {
		if !allowed[name] {
			return bad
		}
	}
	if _, present := fields["client_distribution"]; present && value.ClientDistribution == "" {
		return bad
	}
	*p = PostgreSQLBackupPolicy(value)
	return nil
}

func PostgreSQLBackupPath(p string) bool {
	return filepath.IsAbs(p) && filepath.Clean(p) == p && p != string(filepath.Separator) && !strings.ContainsFunc(p, unicode.IsControl)
}

func (p PostgreSQLBackupPolicy) Valid() bool {
	return PostgreSQLBackupPath(p.DumpFile) && filepath.Base(p.DumpFile) == "pg_dump" && ValidSHA256(p.DumpSHA256) &&
		PostgreSQLBackupPath(p.RestoreFile) && filepath.Base(p.RestoreFile) == "pg_restore" && ValidSHA256(p.RestoreSHA256) &&
		PostgreSQLBackupPath(p.ControlFile) && filepath.Base(p.ControlFile) == "control" && ValidSHA256(p.ControlSHA256) &&
		p.ExpectedVersion == "18.6" && p.validClientDistribution() && PostgreSQLBackupPath(p.ScratchRoot) &&
		p.OperationSeconds >= 30 && p.OperationSeconds <= 86400 && p.TerminationGraceSeconds >= 1 && p.TerminationGraceSeconds <= 60 &&
		p.MaximumStoreBytes >= 1<<20 && p.MaximumStoreBytes <= 1<<40 && p.MaximumStoreFiles >= 4 && p.MaximumStoreFiles <= 1000000 &&
		p.MaximumSQLBytes >= 1<<20 && p.MaximumSQLBytes <= 1<<40 && p.MaximumConfigurationBytes >= 4096 && p.MaximumConfigurationBytes <= 1<<30 &&
		p.MaximumManifestBytes >= 4096 && p.MaximumManifestBytes <= 16<<20 && p.MinimumFreeBytes >= 1<<20 && p.MinimumFreeBytes <= 1<<40
}

// These name exact approved artifact builds, not an arbitrary version policy.
// The archive/server release stays18.6; full client output remains exact.
const PostgreSQLClientUpstream = "upstream"
const PostgreSQLClientPGDGBookworm = "pgdg-bookworm-18.6-1.pgdg12+2"

func (p PostgreSQLBackupPolicy) validClientDistribution() bool {
	return p.ClientDistribution == "" || p.ClientDistribution == PostgreSQLClientUpstream || p.ClientDistribution == PostgreSQLClientPGDGBookworm
}

func (p PostgreSQLBackupPolicy) NativeClientVersion() string {
	if !p.Valid() {
		return ""
	}
	if p.ClientDistribution == PostgreSQLClientPGDGBookworm {
		return "18.6 (Debian 18.6-1.pgdg12+2)"
	}
	return p.ExpectedVersion
}

// PostgreSQLBackupRequest is a protected native input. A matching Kubernetes
// controller must independently bind these identifiers to its durable scope.
// A request or historical native receipt is never current cluster authority.
type PostgreSQLBackupRequest struct {
	FormatVersion            int                    `json:"format_version"`
	BackupID                 string                 `json:"backup_id"`
	SourceScopeID            string                 `json:"source_scope_id"`
	MaintenanceSHA256        string                 `json:"maintenance_sha256"`
	SourceManifestSHA256     string                 `json:"source_manifest_sha256"`
	ControlImageSHA256       string                 `json:"control_image_sha256"`
	PostgresImageSHA256      string                 `json:"postgres_image_sha256"`
	TenantID                 string                 `json:"tenant_id"`
	Database                 string                 `json:"database"`
	Operator                 string                 `json:"operator"`
	RuntimeConfigurationFile string                 `json:"runtime_configuration_file"`
	RuntimeEnvironmentFile   string                 `json:"runtime_environment_file,omitempty"`
	ConfigurationRoot        string                 `json:"configuration_root"`
	BackupRoot               string                 `json:"backup_root"`
	Reason                   string                 `json:"reason"`
	Policy                   PostgreSQLBackupPolicy `json:"policy"`
	ApplicationProfile       string                 `json:"application_profile,omitempty"`
}

func (r *PostgreSQLBackupRequest) UnmarshalJSON(raw []byte) error {
	type plain PostgreSQLBackupRequest
	var value plain
	bad := errors.New("POSTGRES_BACKUP_REQUEST_JSON_INVALID")
	if r == nil || canonicalRestoreObject(raw, &value, false) != nil || !validPostgreSQLApplicationJSON(raw, value.ApplicationProfile) {
		return bad
	}
	*r = PostgreSQLBackupRequest(value)
	return nil
}

var postgresBackupSQLName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

func (r PostgreSQLBackupRequest) Valid() bool {
	return r.EffectiveApplicationProfile() != "" && r.FormatVersion == 1 && restoreGeneration.MatchString(r.BackupID) && restoreGeneration.MatchString(r.SourceScopeID) && r.BackupID != r.SourceScopeID &&
		ValidSHA256(r.MaintenanceSHA256) && ValidSHA256(r.SourceManifestSHA256) && ValidSHA256(r.ControlImageSHA256) && ValidSHA256(r.PostgresImageSHA256) &&
		restoreTenant.MatchString(r.TenantID) && r.TenantID != "00000000-0000-0000-0000-000000000000" && postgresBackupSQLName.MatchString(r.Database) && postgresBackupSQLName.MatchString(r.Operator) &&
		PostgreSQLBackupPath(r.RuntimeConfigurationFile) && (r.RuntimeEnvironmentFile == "" || PostgreSQLBackupPath(r.RuntimeEnvironmentFile)) && PostgreSQLBackupPath(r.ConfigurationRoot) && PostgreSQLBackupPath(r.BackupRoot) &&
		utf8.ValidString(r.Reason) && strings.TrimSpace(r.Reason) != "" && utf8.RuneCountInString(r.Reason) <= 500 && !strings.ContainsFunc(r.Reason, unicode.IsControl) && r.Policy.Valid()
}
