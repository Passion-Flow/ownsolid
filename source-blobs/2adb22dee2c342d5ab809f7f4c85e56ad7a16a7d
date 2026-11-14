package release

import (
	"strings"
	"testing"
)

func TestPostgresRestoreRequestRejectsAmbiguousFreshTarget(t *testing.T) {
	p := PostgreSQLBackupPolicy{DumpFile: "/tools/pg_dump", DumpSHA256: strings.Repeat("a", 64), RestoreFile: "/tools/pg_restore", RestoreSHA256: strings.Repeat("b", 64), ControlFile: "/tools/control", ControlSHA256: strings.Repeat("c", 64), ExpectedVersion: "18.6", ScratchRoot: "/private/scratch", OperationSeconds: 300, TerminationGraceSeconds: 2, MaximumStoreBytes: 1 << 30, MaximumStoreFiles: 1000, MaximumSQLBytes: 1 << 20, MaximumConfigurationBytes: 1 << 20, MaximumManifestBytes: 1 << 20, MinimumFreeBytes: 32 << 30}
	r := PostgreSQLRestoreRequest{FormatVersion: 1, RestoreID: strings.Repeat("c", 32), TargetScopeID: strings.Repeat("d", 32), SourceManifestSHA256: strings.Repeat("e", 64), BackupRoot: "/private/backup", RestoreRoot: "/private/restore", TargetDatabase: "synthetic_target", TargetOperator: "synthetic_operator", RuntimeConfigurationFile: "/private/config/runtime.yaml", ConfigurationRoot: "/private/config", Reason: "Synthetic new target restoration", Policy: p}
	if !r.Valid() {
		t.Fatal("valid target refused")
	}
	for name, change := range map[string]func(*PostgreSQLRestoreRequest){"same-record-scope": func(v *PostgreSQLRestoreRequest) { v.TargetScopeID = v.RestoreID }, "same-root": func(v *PostgreSQLRestoreRequest) { v.RestoreRoot = v.BackupRoot }, "missing-manifest": func(v *PostgreSQLRestoreRequest) { v.SourceManifestSHA256 = "" }, "unsafe-target": func(v *PostgreSQLRestoreRequest) { v.TargetDatabase = "target;DROP" }, "relative-config": func(v *PostgreSQLRestoreRequest) { v.RuntimeConfigurationFile = "runtime.yaml" }, "invalid-bool-timeout": func(v *PostgreSQLRestoreRequest) { v.Policy.OperationSeconds = 0 }, "arbitrary-command": func(v *PostgreSQLRestoreRequest) { v.Policy.RestoreFile = "/bin/sh" }} {
		t.Run(name, func(t *testing.T) {
			v := r
			change(&v)
			if v.Valid() {
				t.Fatal("invalid target accepted")
			}
		})
	}
}
