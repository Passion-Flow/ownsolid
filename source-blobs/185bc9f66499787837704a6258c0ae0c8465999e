package mysqlbackup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func parserRestore() (config.Config, BackupManifest, RestoreIntent, RestoredFiles) {
	// Pure synthetic metadata. No SQL, native process, artifact or accepted restore
	// is manufactured by this parser fixture.
	c := config.Config{}
	p := parserBackupPolicy()
	c.Database.MySQLBackup = &p
	c.Models.ArtifactRoot, c.Datasets.ArtifactRoot, c.Training.ArtifactRoot, c.ModelPreparation.ArtifactRoot = "/synthetic/models", "/synthetic/datasets", "/synthetic/training", "/synthetic/preparations"
	m := parserManifest()
	sha := strings.Repeat("a", 64)
	i := RestoreIntent{Format: "projectone.mysql.restore-intent.v1", RestoreID: uuid.NewString(), BackupID: m.BackupID, CreatedAt: time.Now().UTC(), Reason: "Synthetic explicit isolated restore", BackupRoot: "/synthetic/backup", ManifestSHA256: sha, SQLSHA256: m.SQLSHA256, SourceDataSHA256: m.Data.SHA256, TenantID: m.TenantID, Target: ConnectionTarget{m.Source.ServerUUID, "synthetic_new", m.Source.Principal, m.Source.Version}, TargetBindingSHA256: sha, SchemaSteps: m.SchemaSteps}
	for n, path := range storeRoots(c) {
		i.Roots = append(i.Roots, RootIdentity{path, 1, uint64(n + 1), uint32(os.Geteuid()), uint32(os.Getegid())})
	}
	f := RestoredFiles{Format: "projectone.mysql.restored-files.v1", RestoreID: i.RestoreID, IntentSHA256: sha, NativeArtifactManifestSHA256: sha, ClientSHA256: p.ClientSHA256, ImportExecution: NativeExecution{File: p.ClientFile, Argv: importArguments("/synthetic/private/client.cnf", i.Target.Database), GOOS: "linux", GOARCH: "arm64", PID: 123, StartedAt: time.Now().UTC(), CompletedAt: time.Now().UTC(), Joined: true}}
	for range 4 {
		f.Trees = append(f.Trees, TreeReadback{sha, 0, 0})
	}
	return c, m, i, f
}
func TestRestoreMetadataRefusesForeignSourceTargetBindingsAndFailedProcess(t *testing.T) {
	c, m, i, f := parserRestore()
	sha := strings.Repeat("a", 64)
	if e := i.validate(c, m, sha); e != nil {
		t.Fatal(e)
	}
	if e := f.validate(i, sha, *c.Database.MySQLBackup); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*RestoreIntent){
		func(i *RestoreIntent) { i.Target.Database = m.Source.Database }, func(i *RestoreIntent) { i.Target.Principal = "foreign@localhost" }, func(i *RestoreIntent) { i.Target.Version = "8.4.99" }, func(i *RestoreIntent) { i.ManifestSHA256 = strings.Repeat("b", 64) }, func(i *RestoreIntent) { i.SchemaSteps++ }, func(i *RestoreIntent) { i.Roots[0].Path = "/foreign" }, func(i *RestoreIntent) { i.Reason = "" }, func(i *RestoreIntent) { i.Format = "postgresql" },
	} {
		raw, _ := json.Marshal(i)
		var bad RestoreIntent
		json.Unmarshal(raw, &bad)
		change(&bad)
		if bad.validate(c, m, sha) == nil {
			t.Fatal("foreign isolated intent accepted")
		}
	}
	for _, change := range []func(*RestoredFiles){
		func(f *RestoredFiles) { f.ImportExecution.Joined = false }, func(f *RestoredFiles) { f.ImportExecution.ExitCode = 1 }, func(f *RestoredFiles) { f.ImportExecution.Argv = append(f.ImportExecution.Argv, "--force") }, func(f *RestoredFiles) { f.ImportExecution.Argv[3] = "--local-infile=1" }, func(f *RestoredFiles) { f.ImportExecution.Argv[8] = "--database=foreign" }, func(f *RestoredFiles) { f.ImportExecution.File = "/foreign/mysql" }, func(f *RestoredFiles) { f.ClientSHA256 = strings.Repeat("b", 64) }, func(f *RestoredFiles) { f.Trees[0].Bytes = c.Database.MySQLBackup.MaximumStoreBytes + 1 },
	} {
		raw, _ := json.Marshal(f)
		var bad RestoredFiles
		json.Unmarshal(raw, &bad)
		change(&bad)
		if bad.validate(i, sha, *c.Database.MySQLBackup) == nil {
			t.Fatal("failed or foreign import metadata accepted")
		}
	}
	proof := contextFrom(i, sha, sha, sha)
	if !proof.valid() {
		t.Fatal("valid synthetic context rejected")
	}
	proof.Format = "projectone.postgresql.restored-context.v1"
	if proof.valid() {
		t.Fatal("foreign context accepted")
	}
}
func TestRestoreRequiresExplicitIsolatedTargetAndActualOwnersBeforeIO(t *testing.T) {
	c, _, _, _ := parserRestore()
	for _, isolated := range []bool{false, true} {
		proof, e := Restore(t.Context(), nil, c, nil, "/synthetic/backup", "/synthetic/context", "Synthetic isolated restore", isolated)
		if e == nil || !reflect.DeepEqual(proof, RestoredContext{}) {
			t.Fatal("missing actual target projected restore", e)
		}
		proof, e = ReconcileRestore(t.Context(), nil, c, nil, "/synthetic/context", isolated)
		if e == nil || !reflect.DeepEqual(proof, RestoredContext{}) {
			t.Fatal("missing actual target adopted context", e)
		}
	}
}
func TestFreshRootLocksRejectPopulatedAndChangedIdentityButPermitOwnerFiles(t *testing.T) {
	parent := t.TempDir()
	if e := os.Chmod(parent, 0700); e != nil {
		t.Fatal(e)
	}
	paths := []string{}
	for _, kind := range []string{"models", "datasets", "training", "preparations"} {
		path := filepath.Join(parent, kind)
		if e := os.Mkdir(path, 0700); e != nil {
			t.Fatal(e)
		}
		paths = append(paths, path)
	}
	held, e := acquireFreshRoots(paths)
	if e != nil {
		t.Fatal(e)
	}
	defer held.Close()
	if _, e = acquireFreshRoots(paths); e == nil {
		t.Fatal("concurrent root operator accepted")
	}
	if e = os.WriteFile(filepath.Join(paths[0], "owner-created"), []byte("synthetic"), 0600); e != nil {
		t.Fatal(e)
	}
	if held.Check(false) != nil {
		t.Fatal("legitimate owner-created files rejected")
	}
	if held.Check(true) == nil {
		t.Fatal("populated root accepted as fresh")
	}
	original := paths[1]
	moved := original + "-retained"
	if e = os.Rename(original, moved); e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(original, 0700); e != nil {
		t.Fatal(e)
	}
	if held.Check(false) == nil {
		t.Fatal("replaced root identity accepted")
	}
}
