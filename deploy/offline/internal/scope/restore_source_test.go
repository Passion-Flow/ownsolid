package scope

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func restoreSourcePlanFixture() BackupPlan {
	id, digest := strings.Repeat("a", 32), strings.Repeat("b", 64)
	r := configuration.BackupDefaults()
	r.DestinationRoot = "/synthetic/original/backup"
	r.Reason = "Synthetic bounded plan"
	return BackupPlan{FormatVersion: 1, AttemptID: id, ScopeID: id, ReleaseID: "synthetic", Profile: "synthetic", ConfigurationSHA256: digest, ManifestSHA256: digest, DescriptorSHA256: digest, RuntimeSHA256: digest, Request: r, Jobs: map[string]BackupJob{
		"postgres": {Postgres: &engine.PostgresBackupTask{Owner: engine.Ownership{ScopeID: id, OperationID: id, Role: "database-" + engine.PostgresBackupKind}}},
		"models":   {Store: &engine.DatabaseTask{Kind: engine.ModelFilesExport, Owner: engine.Ownership{ScopeID: id, OperationID: id, Role: "database-" + engine.ModelFilesExport}}},
	}, ConfigurationFiles: []BackupConfigurationFile{{Role: "control-config", File: InputFile{Name: "synthetic-key", Bytes: 9, SHA256: digest}}}}
}

func TestRestoreSourcePlanRoleAndReferenceGuards(t *testing.T) {
	if !validRestoreSourcePlan(restoreSourcePlanFixture()) {
		t.Fatal("valid fixed source context refused")
	}
	cases := map[string]func(*BackupPlan){
		"format":             func(p *BackupPlan) { p.FormatVersion = 2 },
		"scope":              func(p *BackupPlan) { p.ScopeID = "outside" },
		"missing-pg":         func(p *BackupPlan) { delete(p.Jobs, "postgres") },
		"foreign-role":       func(p *BackupPlan) { p.Jobs["../escape"] = p.Jobs["models"] },
		"kind":               func(p *BackupPlan) { p.Jobs["models"].Store.Kind = engine.DatabaseMigrate },
		"foreign-generation": func(p *BackupPlan) { p.Jobs["postgres"].Postgres.Owner.OperationID = strings.Repeat("c", 32) },
		"restore-as-backup":  func(p *BackupPlan) { p.Jobs["postgres"].Postgres.Restore = &engine.PostgresRestoreProfile{} },
		"file-parent":        func(p *BackupPlan) { p.ConfigurationFiles[0].File.Name = "../escape" },
		"file-nested":        func(p *BackupPlan) { p.ConfigurationFiles[0].File.Name = "nested/key" },
		"file-role":          func(p *BackupPlan) { p.ConfigurationFiles[0].Role = "models" },
		"file-duplicate":     func(p *BackupPlan) { p.ConfigurationFiles = append(p.ConfigurationFiles, p.ConfigurationFiles[0]) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			p := restoreSourcePlanFixture()
			mutate(&p)
			if validRestoreSourcePlan(p) {
				t.Fatal("unsafe source context admitted")
			}
		})
	}
}

func TestRestoreSourceRestoredBackupProvenanceNeverOverridesTargetAuthority(t *testing.T) {
	p := restoreSourcePlanFixture()
	digest := strings.Repeat("b", 64)
	a := RestoredApplicationAdmission{Dependency: RestoredDependencyAdmission{ReplacementProofSHA256: digest, TLSProofSHA256: digest, EffectiveRuntimeSHA256: digest, ConfigurationPlanSHA256: digest}, DatabaseProofSHA256: digest, DatabasePlanSHA256: digest, StoresProofSHA256: digest, StoresPlanSHA256: digest, PayloadSHA256: digest, ContextSHA256: digest, PostgresPID: 123, PostgresStartedAt: "2026-10-18T00:00:00Z"}
	p.EffectiveControlPayloadSHA256 = wire.Digest(encode([]InputFile{p.ConfigurationFiles[0].File}))
	p.RestoredAdmission = &a
	if !validRestoreSourcePlan(p) {
		t.Fatal("well-formed source provenance refused")
	}
	for _, fault := range []string{"runtime", "PID", "time", "proof", "payload-index"} {
		t.Run(fault, func(t *testing.T) {
			copy := p
			changed := a
			copy.RestoredAdmission = &changed
			switch fault {
			case "runtime":
				copy.RuntimeSHA256 = strings.Repeat("c", 64)
			case "PID":
				changed.PostgresPID = 0
			case "time":
				changed.PostgresStartedAt = "invalid"
			case "proof":
				changed.StoresProofSHA256 = "invalid"
			case "payload-index":
				copy.EffectiveControlPayloadSHA256 = strings.Repeat("c", 64)
			}
			if validRestoreSourcePlan(copy) {
				t.Fatal("invalid source provenance accepted")
			}
		})
	}
}

func TestRestoreSourceTreePrivacyLinksAndAggregateLimits(t *testing.T) {
	dir := t.TempDir()
	if os.Chmod(dir, 0700) != nil {
		t.Fatal("private fixture")
	}
	root, e := os.OpenRoot(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	r := configuration.RestoreSourceDefaults()
	file := filepath.Join(dir, "payload")
	if os.WriteFile(file, []byte("verified"), 0600) != nil {
		t.Fatal("fixture")
	}
	if n, b, e := admitRestoreSourceTree(context.Background(), root, r); e != nil || n != 1 || b != 8 {
		t.Fatal(n, b, e)
	}
	if os.Link(file, filepath.Join(dir, "second")) != nil {
		t.Fatal("hardlink fixture")
	}
	if _, _, e := admitRestoreSourceTree(context.Background(), root, r); e == nil {
		t.Fatal("hardlink admitted")
	}
	os.Remove(filepath.Join(dir, "second"))
	if os.Symlink(file, filepath.Join(dir, "linked")) != nil {
		t.Fatal("symlink fixture")
	}
	if _, _, e := admitRestoreSourceTree(context.Background(), root, r); e == nil {
		t.Fatal("symlink admitted")
	}
	os.Remove(filepath.Join(dir, "linked"))
	os.Chmod(file, 0644)
	if _, _, e := admitRestoreSourceTree(context.Background(), root, r); e == nil {
		t.Fatal("public source admitted")
	}
	os.Chmod(file, 0600)
	r.MaximumBytes = 7
	if _, _, e := admitRestoreSourceTree(context.Background(), root, r); e == nil {
		t.Fatal("byte bound bypass")
	}
	r.MaximumBytes = 8
	r.MaximumEntries = 1
	if _, _, e := admitRestoreSourceTree(context.Background(), root, r); e == nil {
		t.Fatal("entry bound bypass")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, e := admitRestoreSourceTree(ctx, root, configuration.RestoreSourceDefaults()); e == nil {
		t.Fatal("cancellation bypass")
	}
}
