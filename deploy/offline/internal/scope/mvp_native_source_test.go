package scope

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	view "github.com/Passion-Flow/ownsolid/api/release/view"
)

// Explicit structural fixtures only. No SQL, native process or signed release
// qualification is fabricated by these tests.
func mvpNativeSourcePlanFixture() mvpNativeBackupPlan {
	h := strings.Repeat("a", 64)
	scope := strings.Repeat("b", 32)
	id := strings.Repeat("c", 32)
	r := configuration.BackupDefaults()
	r.DestinationRoot = "/synthetic/outer"
	r.Reason = "Synthetic original backup"
	p := wire.PostgreSQLBackupPolicy{DumpFile: "/usr/local/bin/pg_dump", DumpSHA256: h, RestoreFile: "/usr/local/bin/pg_restore", RestoreSHA256: h, ControlFile: "/usr/local/bin/control", ControlSHA256: h, ExpectedVersion: "18.6", ScratchRoot: "/synthetic/scratch", OperationSeconds: 30, TerminationGraceSeconds: 1, MaximumStoreBytes: 1 << 20, MaximumStoreFiles: 100, MaximumSQLBytes: 1 << 20, MaximumConfigurationBytes: 4096, MaximumManifestBytes: 1 << 20, MinimumFreeBytes: 1 << 20}
	plan := mvpNativeBackupPlan{ExecutionSHA256: h, NativeUID: 1000, NativeGID: 1000, FormatVersion: 2, ScopeID: scope, AttemptID: id, ReleaseID: "synthetic", Profile: wire.MVPPostgreSQLCPUProfile, ConfigurationSHA256: h, ReleaseManifestSHA256: h, ReleaseArchiveSHA256: h, DescriptorSHA256: h, RuntimeSHA256: h, Request: r, NativeRequest: wire.PostgreSQLBackupRequest{FormatVersion: 1, BackupID: id, SourceScopeID: scope, MaintenanceSHA256: h, SourceManifestSHA256: h, ControlImageSHA256: h, PostgresImageSHA256: h, TenantID: "11111111-1111-4111-8111-111111111111", Database: "source_database", Operator: "source_operator", RuntimeConfigurationFile: "/synthetic/config/runtime.yaml", ConfigurationRoot: "/synthetic/config", BackupRoot: "/synthetic/backups/" + id, Reason: r.Reason, Policy: p, ApplicationProfile: wire.MVPPostgreSQLCPUApplicationProfile}, ConfigurationFiles: []BackupConfigurationFile{{Role: "control-config", File: InputFile{Name: "runtime.yaml", Bytes: 2, SHA256: h}}}, Steps: []BackupStep{{Phase: "reserve"}, {Phase: "complete"}}}
	plan.ReleaseViewBinding = &view.Configuration{Directory: view.ContainerDirectory, ScopeID: scope, ManifestSHA256: h, ConfigurationSHA256: h, PublisherUID: 0, PageSize: 47, OperationSeconds: 7}
	plan.Storage, _ = makeMVPNativeBackupStorage(plan, 4096, 1024, 1<<20, 1<<20, 1<<20, 1<<20)
	return plan
}

func TestMVPNativeSourcePlanKeepsOriginalAuthoritiesDistinct(t *testing.T) {
	if !validMVPNativeBackupPlan(mvpNativeSourcePlanFixture()) {
		t.Fatal("fixture")
	}
	changes := map[string]func(*mvpNativeBackupPlan){
		"legacy format":                   func(p *mvpNativeBackupPlan) { p.FormatVersion = 1 },
		"legacy signed profile":           func(p *mvpNativeBackupPlan) { p.Profile = wire.CumulativeCPUProfile },
		"unknown native profile":          func(p *mvpNativeBackupPlan) { p.NativeRequest.ApplicationProfile = "" },
		"borrowed source scope":           func(p *mvpNativeBackupPlan) { p.NativeRequest.SourceScopeID = strings.Repeat("d", 32) },
		"invented native attempt":         func(p *mvpNativeBackupPlan) { p.NativeRequest.BackupID = strings.Repeat("d", 32) },
		"candidate as signed source":      func(p *mvpNativeBackupPlan) { p.NativeRequest.SourceManifestSHA256 = strings.Repeat("e", 64) },
		"reason changed":                  func(p *mvpNativeBackupPlan) { p.NativeRequest.Reason = "changed" },
		"missing reservation":             func(p *mvpNativeBackupPlan) { p.Steps = p.Steps[1:] },
		"unretained source configuration": func(p *mvpNativeBackupPlan) { p.ConfigurationFiles = nil },
		"config traversal":                func(p *mvpNativeBackupPlan) { p.ConfigurationFiles[0].File.Name = "../secret" },
		"duplicate config": func(p *mvpNativeBackupPlan) {
			p.ConfigurationFiles = append(p.ConfigurationFiles, p.ConfigurationFiles[0])
		},
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			p := mvpNativeSourcePlanFixture()
			change(&p)
			if validMVPNativeBackupPlan(p) {
				t.Fatal("changed authority admitted")
			}
		})
	}
}

func mvpNativeInventoryFixture(t *testing.T) (string, *os.Root) {
	t.Helper()
	dir := t.TempDir()
	if e := os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{mvpNativeDirectory, mvpNativeDirectory + "/stores", mvpNativeDirectory + "/stores/models", mvpNativeDirectory + "/stores/models/sealed"} {
		if e := os.Mkdir(filepath.Join(dir, name), 0700); e != nil {
			t.Fatal(e)
		}
	}
	for name, raw := range map[string][]byte{backupPlanFile: []byte("original plan\n"), mvpNativeDirectory + "/candidate.json": []byte("original native\n"), mvpNativeDirectory + "/stores/models/sealed/empty": {}} {
		if e := os.WriteFile(filepath.Join(dir, name), raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	if e := os.Chmod(filepath.Join(dir, mvpNativeDirectory, "stores/models/sealed/empty"), 0444); e != nil {
		t.Fatal(e)
	}
	if e := os.Chmod(filepath.Join(dir, mvpNativeDirectory, "stores/models/sealed"), 0555); e != nil {
		t.Fatal(e)
	}
	root, e := os.OpenRoot(dir)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { root.Close() })
	return dir, root
}

func TestMVPNativeInventoryPreservesSealedModesEmptyFilesAndRelocation(t *testing.T) {
	dir, root := mvpNativeInventoryFixture(t)
	a, n, e := inventoryMVPNativeSource(context.Background(), root, 1<<20, 100, 1<<20)
	if e != nil || n != 30 {
		t.Fatal(n, e)
	}
	found := false
	for _, v := range a {
		if strings.HasSuffix(v.Name, "/empty") {
			found = v.Bytes == 0 && v.Mode == 0444 && v.SHA256 == wire.Digest(nil)
		}
	}
	if !found {
		t.Fatal(a)
	}
	moved := dir + "-moved"
	if e = os.Rename(dir, moved); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.Rename(moved, dir) })
	if _, _, e = inventoryMVPNativeSource(context.Background(), root, 1<<20, 100, 1<<20); e == nil {
		t.Fatal("replaced named root admitted through old descriptor")
	}
	reopened, e := os.OpenRoot(moved)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	b, m, e := inventoryMVPNativeSource(context.Background(), reopened, 1<<20, 100, 1<<20)
	if e != nil || m != n || !reflect.DeepEqual(a, b) {
		t.Fatal("portable identity changed", e)
	}
}

func TestMVPNativeInventoryRejectsLinksSpecialFilesAndLimits(t *testing.T) {
	for _, fault := range []string{"symlink", "directory-symlink", "hardlink", "fifo", "public-wrapper", "writable-native", "foreign-empty", "byte-limit", "entry-limit", "metadata-limit", "cancelled"} {
		t.Run(fault, func(t *testing.T) {
			dir, root := mvpNativeInventoryFixture(t)
			ctx := context.Background()
			maximum := int64(1 << 20)
			entries := 100
			metadata := int64(1 << 20)
			file := filepath.Join(dir, mvpNativeDirectory, "candidate.json")
			switch fault {
			case "symlink":
				if e := os.Symlink(file, filepath.Join(dir, mvpNativeDirectory, "alias")); e != nil {
					t.Fatal(e)
				}
			case "directory-symlink":
				if e := os.Symlink(filepath.Join(dir, mvpNativeDirectory, "stores"), filepath.Join(dir, "aliased")); e != nil {
					t.Fatal(e)
				}
			case "hardlink":
				if e := os.Link(file, filepath.Join(dir, mvpNativeDirectory, "second")); e != nil {
					t.Fatal(e)
				}
			case "fifo":
				if e := syscall.Mkfifo(filepath.Join(dir, mvpNativeDirectory, "fifo"), 0600); e != nil {
					t.Fatal(e)
				}
			case "public-wrapper":
				os.Chmod(filepath.Join(dir, backupPlanFile), 0644)
			case "writable-native":
				os.Chmod(file, 0666)
			case "foreign-empty":
				os.WriteFile(filepath.Join(dir, "empty"), nil, 0600)
			case "byte-limit":
				maximum = 1
			case "entry-limit":
				entries = 2
			case "metadata-limit":
				metadata = 100
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if _, _, e := inventoryMVPNativeSource(ctx, root, maximum, entries, metadata); e == nil {
				t.Fatal("unsafe inventory admitted")
			}
		})
	}
}

func mvpNativeCandidateFixture(t *testing.T) (*os.Root, configuration.Config, mvpNativeBackupPlan, wire.NativePostgreSQLBackupReference) {
	t.Helper()
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	os.Mkdir(filepath.Join(dir, mvpNativeDirectory), 0700)
	root, e := os.OpenRoot(dir)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { root.Close() })
	c := configuration.Defaults()
	p := mvpNativeSourcePlanFixture()
	h := strings.Repeat("d", 64)
	at := "2026-11-03T00:00:00Z"
	receipt := map[string]any{"manifest_sha256": h, "pins": 2, "files": 3, "bytes": int64(9)}
	binding := map[string]any{"source_metadata": map[string]any{"server_version_number": 180006, "vector_version": "0.8.7"}, "registry_configured": true, "registry": map[string]any{"receipt": receipt, "tree": map[string]any{"sha256": h}}}
	braw := encode(binding)
	rraw := encode(receipt)
	m := map[string]any{"format": "projectone.postgres.matched-backup.v1", "request": p.NativeRequest, "request_sha256": wire.Digest(encode(p.NativeRequest)), "created_at": at, "database": map[string]any{"tenant_id": p.NativeRequest.TenantID, "database": p.NativeRequest.Database, "operator": p.NativeRequest.Operator, "schema_version": 29, "tables": 108, "server_version_number": 180006, "vector_version": "0.8.7"}, "database_snapshot": "00000001-00000001-1", "sources": []any{map[string]any{}, map[string]any{}, map[string]any{}, map[string]any{}}, "stores": []any{map[string]any{"kind": "models", "tree": map[string]any{}}, map[string]any{"kind": "datasets", "tree": map[string]any{}}, map[string]any{"kind": "training", "tree": map[string]any{}}, map[string]any{"kind": "preparations", "tree": map[string]any{}}}, "archive": map[string]any{"sha256": h, "bytes": 10}, "configuration": map[string]any{}, "effective_configuration": map[string]any{}, "mvp_source": binding}
	candidate := encode(m)
	prior := encode(map[string]any{"format": "projectone.postgres.independent-readback.v1", "backup_id": p.AttemptID, "request_sha256": wire.Digest(encode(p.NativeRequest)), "manifest_sha256": wire.Digest(candidate), "archive_sha256": h, "archive_directory_sha256": h, "observed_at": at, "current_lock_held": false})
	complete := encode(map[string]any{"format": "projectone.postgres.backup-complete.v1", "backup_id": p.AttemptID, "manifest_sha256": wire.Digest(candidate), "readback_sha256": wire.Digest(prior), "completed_at": at, "qualification": "synthetic structural fixture only"})
	for name, raw := range map[string][]byte{"candidate.json": candidate, "independent-readback.json": prior, "complete.json": complete} {
		if e := os.WriteFile(filepath.Join(dir, mvpNativeDirectory, name), raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	ref := wire.NativePostgreSQLBackupReference{FormatVersion: 1, ApplicationProfile: wire.MVPPostgreSQLCPUApplicationProfile, SourceManifestSHA256: p.ReleaseManifestSHA256, SchemaVersion: 29, TableCount: 108, RequestSHA256: wire.Digest(encode(p.NativeRequest)), CandidateSHA256: wire.Digest(candidate), ReadbackSHA256: wire.Digest(prior), CompletionSHA256: wire.Digest(complete), MVPSourceSHA256: wire.Digest(braw), RegistryConfigured: true, Registry: &wire.NativePostgreSQLRegistryReference{ReceiptSHA256: wire.Digest(rraw), ManifestSHA256: h, PayloadTreeSHA256: h, Pins: 2, Files: 3, Bytes: 9}}
	return root, c, p, ref
}

func TestMVPNativeCandidateBindsOriginalRequestCompletionAndRegistry(t *testing.T) {
	root, c, p, ref := mvpNativeCandidateFixture(t)
	source, archive, e := readMVPNativeCandidate(root, c, p, ref)
	if e != nil || !source.Valid() || archive != strings.Repeat("d", 64) {
		t.Fatal(source, archive, e)
	}
	for _, fault := range []string{"candidate", "native-request", "signed-source", "completion", "readback", "MVP-source", "registry-receipt", "registry-tree", "registry-pins", "schema", "tables"} {
		t.Run(fault, func(t *testing.T) {
			root, c, p, ref := mvpNativeCandidateFixture(t)
			switch fault {
			case "candidate":
				ref.CandidateSHA256 = strings.Repeat("e", 64)
			case "native-request":
				p.NativeRequest.BackupRoot = "/synthetic/relocated"
			case "signed-source":
				p.ReleaseManifestSHA256 = strings.Repeat("e", 64)
			case "completion":
				ref.CompletionSHA256 = strings.Repeat("e", 64)
			case "readback":
				ref.ReadbackSHA256 = strings.Repeat("e", 64)
			case "MVP-source":
				ref.MVPSourceSHA256 = strings.Repeat("e", 64)
			case "registry-receipt":
				ref.Registry.ReceiptSHA256 = strings.Repeat("e", 64)
			case "registry-tree":
				ref.Registry.PayloadTreeSHA256 = strings.Repeat("e", 64)
			case "registry-pins":
				ref.Registry.Pins++
			case "schema":
				ref.SchemaVersion = 22
			case "tables":
				ref.TableCount = 86
			}
			if _, _, e := readMVPNativeCandidate(root, c, p, ref); e == nil {
				t.Fatal("changed original binding admitted")
			}
		})
	}
}

func TestMVPNativeSourceMarkerNeverFallsBackToLegacy(t *testing.T) {
	for name, raw := range map[string]string{"legacy": `{"format_version":1,"profile":"synthetic"}`, "native-null": `{"native_postgresql":null}`, "native-case": `{"Native_PostgreSQL":null}`, "MVP-case": `{"PROFILE":"mvp-postgresql-cpu-v1-linux-amd64"}`, "schema-case": `{"SOURCE":{"SCHEMA_VERSION":29}}`, "native-request-null": `{"native_request":null}`, "MVP": `{"profile":"mvp-postgresql-cpu-v1-linux-amd64"}`, "schema29": `{"source":{"schema_version":29}}`, "duplicate": `{"profile":"x","profile":"x"}`} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			os.Chmod(dir, 0700)
			os.WriteFile(filepath.Join(dir, backupPlanFile), []byte(raw), 0600)
			os.WriteFile(filepath.Join(dir, backupManifestFile), []byte(raw), 0600)
			r := configuration.RestoreSourceDefaults()
			r.BackupRoot = dir
			r.ManifestSHA256 = strings.Repeat("a", 64)
			c := configuration.Defaults()
			handled, e := hasMVPNativeSource(c, r)
			if name == "legacy" {
				if handled || e != nil {
					t.Fatal(handled, e)
				}
			} else if !handled {
				t.Fatal("native/invalid marker fell through")
			}
		})
	}
	raw, e := json.Marshal(mvpNativeRestoreSource{Request: mvpNativeSourcePlanFixture().NativeRequest, NativeDirectory: "/private/source", ArchiveSHA256: strings.Repeat("a", 64)})
	if e != nil || string(raw) != "{}" {
		t.Fatal("private accessor serialized", string(raw), e)
	}
}

func TestMVPNativeOriginalTreeDigestUsesModesPathsAndEveryPayload(t *testing.T) {
	entries := []mvpNativeInventoryEntry{{Name: "native-postgresql/stores/models", Kind: "directory", Mode: 0700}, {Name: "native-postgresql/stores/models/sealed", Kind: "directory", Mode: 0555}, {Name: "native-postgresql/stores/models/sealed/empty", Kind: "file", Mode: 0444, SHA256: wire.Digest(nil)}, {Name: "native-postgresql/stores/models/z", Kind: "file", Mode: 0600, Bytes: 3, SHA256: wire.Digest([]byte("one"))}}
	p, e := mvpNativeTreeFromInventory(entries, "native-postgresql/stores/models")
	if e != nil || p.Files != 2 || p.Bytes != 3 || !wire.ValidSHA256(p.SHA256) {
		t.Fatal(p, e)
	}
	for _, fault := range []string{"mode", "name", "bytes", "content", "missing"} {
		t.Run(fault, func(t *testing.T) {
			changed := append([]mvpNativeInventoryEntry(nil), entries...)
			switch fault {
			case "mode":
				changed[1].Mode = 0700
			case "name":
				changed[2].Name += "changed"
			case "bytes":
				changed[3].Bytes++
			case "content":
				changed[3].SHA256 = wire.Digest([]byte("two"))
			case "missing":
				changed = changed[1:]
			}
			v, e := mvpNativeTreeFromInventory(changed, "native-postgresql/stores/models")
			if e == nil && v == p {
				t.Fatal("changed original tree identity")
			}
		})
	}
}

func TestMVPNativeInventoryRetainsOriginalUnicodeLongNamesAndPublicReadModes(t *testing.T) {
	dir, root := mvpNativeInventoryFixture(t)
	name := "\u539f\u59cb \u6587\u4ef6-" + strings.Repeat("a", 160)
	file := filepath.Join(dir, mvpNativeDirectory, "stores/models", name)
	if e := os.WriteFile(file, []byte("immutable native bytes"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Chmod(file, 0644); e != nil {
		t.Fatal(e)
	}
	entries, _, e := inventoryMVPNativeSource(context.Background(), root, 1<<20, 100, 1<<20)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name, "/"+name) {
			found = entry.Mode == 0644 && entry.SHA256 == wire.Digest([]byte("immutable native bytes"))
		}
	}
	if !found {
		t.Fatal("original native name/mode rewritten")
	}
}
