package release

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPostgresBackupRequestRejectsIncompleteAuthorityAndPolicy(t *testing.T) {
	p := PostgreSQLBackupPolicy{DumpFile: "/tools/pg_dump", DumpSHA256: strings.Repeat("a", 64), RestoreFile: "/tools/pg_restore", RestoreSHA256: strings.Repeat("b", 64), ControlFile: "/tools/control", ControlSHA256: strings.Repeat("c", 64), ExpectedVersion: "18.6", ScratchRoot: "/private/scratch", OperationSeconds: 300, TerminationGraceSeconds: 2, MaximumStoreBytes: 1 << 30, MaximumStoreFiles: 1000, MaximumSQLBytes: 1 << 20, MaximumConfigurationBytes: 1 << 20, MaximumManifestBytes: 1 << 20, MinimumFreeBytes: 32 << 30}
	r := PostgreSQLBackupRequest{FormatVersion: 1, BackupID: strings.Repeat("a", 32), SourceScopeID: strings.Repeat("b", 32), MaintenanceSHA256: strings.Repeat("a", 64), SourceManifestSHA256: strings.Repeat("b", 64), ControlImageSHA256: strings.Repeat("c", 64), PostgresImageSHA256: strings.Repeat("d", 64), TenantID: "11111111-1111-4111-8111-111111111111", Database: "synthetic_database", Operator: "synthetic_operator", RuntimeConfigurationFile: "/private/config/runtime.yaml", ConfigurationRoot: "/private/config", BackupRoot: "/private/backups/synthetic", Reason: "Synthetic matched backup", Policy: p}
	if !r.Valid() {
		t.Fatal("valid request refused")
	}
	for name, change := range map[string]func(*PostgreSQLBackupRequest){
		"version": func(v *PostgreSQLBackupRequest) { v.FormatVersion = 2 }, "same-scope": func(v *PostgreSQLBackupRequest) { v.SourceScopeID = v.BackupID }, "missing-maintenance": func(v *PostgreSQLBackupRequest) { v.MaintenanceSHA256 = "" }, "nil-tenant": func(v *PostgreSQLBackupRequest) { v.TenantID = "00000000-0000-0000-0000-000000000000" }, "sql-injection": func(v *PostgreSQLBackupRequest) { v.Database = "db';DROP DATABASE db" }, "reason-controls": func(v *PostgreSQLBackupRequest) { v.Reason = "not\na reason" }, "path-escape": func(v *PostgreSQLBackupRequest) { v.BackupRoot = "/private/../other" }, "arbitrary-tool": func(v *PostgreSQLBackupRequest) { v.Policy.DumpFile = "/bin/sh" }, "wrong-version": func(v *PostgreSQLBackupRequest) { v.Policy.ExpectedVersion = "19" }, "zero-deadline": func(v *PostgreSQLBackupRequest) { v.Policy.OperationSeconds = 0 }, "zero-reserve": func(v *PostgreSQLBackupRequest) { v.Policy.MinimumFreeBytes = 0 }, "unbounded": func(v *PostgreSQLBackupRequest) { v.Policy.MaximumSQLBytes = 1 << 50 },
	} {
		t.Run(name, func(t *testing.T) {
			v := r
			change(&v)
			if v.Valid() {
				t.Fatal("invalid request accepted")
			}
		})
	}
}

func TestPostgresClientDistributionKeepsFixedReleaseAndLegacyOmission(t *testing.T) {
	p := PostgreSQLBackupPolicy{DumpFile: "/tools/pg_dump", DumpSHA256: strings.Repeat("a", 64), RestoreFile: "/tools/pg_restore", RestoreSHA256: strings.Repeat("b", 64), ControlFile: "/tools/control", ControlSHA256: strings.Repeat("c", 64), ExpectedVersion: "18.6", ScratchRoot: "/private/scratch", OperationSeconds: 300, TerminationGraceSeconds: 2, MaximumStoreBytes: 1 << 30, MaximumStoreFiles: 1000, MaximumSQLBytes: 1 << 20, MaximumConfigurationBytes: 1 << 20, MaximumManifestBytes: 1 << 20, MinimumFreeBytes: 32 << 30}
	raw, e := json.Marshal(p)
	if e != nil || strings.Contains(string(raw), "client_distribution") || p.NativeClientVersion() != "18.6" {
		t.Fatal("absent selector changes legacy bytes or descriptor", e)
	}
	for _, distribution := range []string{"", PostgreSQLClientUpstream, PostgreSQLClientPGDGBookworm} {
		p.ClientDistribution = distribution
		if !p.Valid() || p.ExpectedVersion != "18.6" {
			t.Fatal("approved build changed release gate")
		}
		want := "18.6"
		if distribution == PostgreSQLClientPGDGBookworm {
			want = "18.6 (Debian 18.6-1.pgdg12+2)"
		}
		if p.NativeClientVersion() != want {
			t.Fatal("full exact descriptor")
		}
	}
	for _, distribution := range []string{"false", "pgdg", "pgdg-bookworm-18.6-3.pgdg12+1", "upstream ", "skip"} {
		p.ClientDistribution = distribution
		if p.Valid() || p.NativeClientVersion() != "" {
			t.Fatal("unknown artifact build accepted")
		}
	}
	p.ClientDistribution = PostgreSQLClientPGDGBookworm
	p.ExpectedVersion = "19"
	if p.Valid() || p.NativeClientVersion() != "" {
		t.Fatal("packaging selector weakened semantic release")
	}
	for _, raw := range []string{`{"client_distribution":""}`, `{"client_distribution":false}`, `{"client_distribution":null}`, `{"CLIENT_DISTRIBUTION":"upstream"}`, `{"client_distribution":"upstream","CLIENT_DISTRIBUTION":"pgdg-bookworm-18.6-1.pgdg12+2"}`, `{"client_distribution":"upstream","client_distribution":"upstream"}`, `{"unknown":"upstream"}`} {
		var value PostgreSQLBackupPolicy
		if StrictJSON([]byte(raw), &value) == nil {
			t.Fatal("invalid/ambiguous policy field accepted", raw)
		}
	}
}
