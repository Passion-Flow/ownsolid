//go:build offlineintegration

package scope

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func TestActualIndependentRestoreSourceAndNativeInspection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	raw, e := bundle.ReadProtectedConfiguration(os.Getenv("PROJECT_ONE_TEST_POSTGRES_RESTORE_CONFIGURATION"))
	if e != nil {
		t.Fatal(e)
	}
	c, e := configuration.Load(raw, nil)
	if e != nil || !c.Verification.AllowTestFixture {
		t.Fatal("explicit synthetic fixture", e)
	}
	r := configuration.RestoreSourceDefaults()
	r.BackupRoot = os.Getenv("PROJECT_ONE_TEST_POSTGRES_RESTORE_BACKUP")
	r.ManifestSHA256 = os.Getenv("PROJECT_ONE_TEST_POSTGRES_RESTORE_MANIFEST_SHA256")
	out := os.Getenv("PROJECT_ONE_TEST_POSTGRES_RESTORE_EVIDENCE")
	if !privateDirectory(out) {
		t.Fatal("private evidence")
	}
	put := func(name string, value any) {
		raw, e := json.MarshalIndent(value, "", "  ")
		if e != nil {
			t.Fatal(e)
		}
		root, e := os.OpenRoot(out)
		if e != nil {
			t.Fatal(e)
		}
		defer root.Close()
		if e = writeBackupFile(root, name, append(raw, '\n')); e != nil {
			t.Fatal(e)
		}
	}
	// No Engine or Compose configuration is available to the independent
	// reader or native process. Successful validation cannot contact Docker.
	c.Engine, c.Compose = nil, nil
	journal := filepath.Join(c.ScopeRoot, journalName)
	before, e := os.ReadFile(journal)
	if e != nil {
		t.Fatal(e)
	}
	result, e := InspectRestoreSource(ctx, c, r)
	if e != nil || result.Status != "verified_independent_backup_source_not_restored" || result.Source.SchemaVersion != 16 || !reflect.DeepEqual(result.Stores, []string{"datasets", "models"}) {
		t.Fatal("actual complete source", e)
	}
	put("original-source.json", result)
	root, e := openBackupDirectory(r.BackupRoot)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	var m BackupManifest
	raw, e = regular(root, backupManifestFile, 1<<20)
	if e != nil || wire.StrictJSON(raw, &m) != nil {
		t.Fatal("source manifest")
	}
	assertSource := func() {
		for _, expected := range m.Files {
			f, e := backupHashFile(ctx, root, expected.Name, r.MaximumBytes)
			if e != nil || f != expected {
				t.Fatal("original source changed", e)
			}
		}
		after, e := os.ReadFile(journal)
		if e != nil || wire.Digest(after) != wire.Digest(before) {
			t.Fatal("source scope journal changed", e)
		}
	}
	copyCount := 0
	copyBackup := func() string {
		target := t.TempDir()
		if os.Chmod(target, 0700) != nil {
			t.Fatal("private portable copy")
		}
		copyCount++
		put("copy-"+strconv.Itoa(copyCount)+"-intent.json", map[string]any{"path": target, "disposable_private_synthetic_copy": true})
		e := filepath.WalkDir(r.BackupRoot, func(name string, d fs.DirEntry, e error) error {
			if e != nil {
				return e
			}
			rel, e := filepath.Rel(r.BackupRoot, name)
			if e != nil {
				return e
			}
			if rel == "." {
				return nil
			}
			dest := filepath.Join(target, rel)
			if d.IsDir() {
				return os.Mkdir(dest, 0700)
			}
			in, e := os.Open(name)
			if e != nil {
				return e
			}
			defer in.Close()
			out, e := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if e != nil {
				return e
			}
			_, ce := io.Copy(out, in)
			se := out.Sync()
			cl := out.Close()
			if ce != nil {
				return ce
			}
			if se != nil {
				return se
			}
			return cl
		})
		if e != nil {
			t.Fatal(e)
		}
		return target
	}
	portable := r
	portable.BackupRoot = copyBackup()
	if copied, e := InspectRestoreSource(ctx, c, portable); e != nil || copied.ManifestSHA256 != result.ManifestSHA256 || copied.Source != result.Source {
		t.Fatal("portable source tied to original absolute path", e)
	}
	results := []map[string]any{{"case": "portable-copy", "passed": true}}
	mutations := map[string]func(string, *configuration.RestoreSourceRequest){
		"wrong-digest": func(_ string, q *configuration.RestoreSourceRequest) { q.ManifestSHA256 = strings.Repeat("f", 64) },
		"missing-file": func(p string, _ *configuration.RestoreSourceRequest) {
			if os.Remove(filepath.Join(p, "datasets.tar")) != nil {
				t.Fatal("fixture")
			}
		},
		"changed-archive": func(p string, _ *configuration.RestoreSourceRequest) {
			f, e := os.OpenFile(filepath.Join(p, "postgres.tar"), os.O_WRONLY, 0)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = f.WriteAt([]byte("X"), 1024); e != nil {
				t.Fatal(e)
			}
			f.Close()
		},
		"public-file": func(p string, _ *configuration.RestoreSourceRequest) {
			if os.Chmod(filepath.Join(p, "datasets.tar"), 0644) != nil {
				t.Fatal("fixture")
			}
		},
		"hardlink": func(p string, _ *configuration.RestoreSourceRequest) {
			if os.Link(filepath.Join(p, "datasets.tar"), filepath.Join(p, "outside-linked")) != nil {
				t.Fatal("fixture")
			}
		},
		"symlink": func(p string, _ *configuration.RestoreSourceRequest) {
			if os.Remove(filepath.Join(p, "datasets.tar")) != nil || os.Symlink(filepath.Join(p, "postgres.tar"), filepath.Join(p, "datasets.tar")) != nil {
				t.Fatal("fixture")
			}
		},
		"unknown-directory": func(p string, _ *configuration.RestoreSourceRequest) {
			if os.Mkdir(filepath.Join(p, "unknown"), 0700) != nil {
				t.Fatal("fixture")
			}
		},
		"caller-byte-bound":  func(_ string, q *configuration.RestoreSourceRequest) { q.MaximumBytes = 1 << 20 },
		"caller-entry-bound": func(_ string, q *configuration.RestoreSourceRequest) { q.MaximumEntries = 4 },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			q := r
			q.BackupRoot = copyBackup()
			mutate(q.BackupRoot, &q)
			_, e := InspectRestoreSource(ctx, c, q)
			if e == nil {
				t.Fatal("unsafe source accepted")
			}
			results = append(results, map[string]any{"case": name, "refused": true, "code": e.Error()})
			assertSource()
		})
	}
	for _, target := range []string{r.BackupRoot, filepath.Join(r.BackupRoot, "child"), filepath.Dir(r.BackupRoot)} {
		bad := c
		bad.ScopeRoot = target
		if _, e = InspectRestoreSource(ctx, bad, r); e == nil {
			t.Fatal("target/source overlap admitted")
		}
	}
	bad := c
	bad.Verification.MountRoot = "/"
	if _, e = InspectRestoreSource(ctx, bad, r); e == nil {
		t.Fatal("internal/nonmounted source admitted")
	}
	put("operator.json", c)
	put("operation.json", r)
	operator := os.Getenv("PROJECT_ONE_TEST_RESTORE_SOURCE_OPERATOR")
	if operator == "" {
		t.Fatal("current native operator required")
	}
	run := func(operation string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, operator, "inspect-restore-source", "--config", filepath.Join(out, "operator.json"), "--operation-file", filepath.Join(out, operation))
		cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "LANG=C"}
		return cmd.CombinedOutput()
	}
	output, e := run("operation.json")
	var observed RestoreSourceResult
	if e != nil || wire.StrictJSON(output, &observed) != nil || !reflect.DeepEqual(observed, result) {
		t.Fatal("current native readonly inspection", e)
	}
	put("native-result.json", observed)
	wrong := r
	wrong.ManifestSHA256 = strings.Repeat("f", 64)
	put("wrong-operation.json", wrong)
	output, e = run("wrong-operation.json")
	if e == nil || strings.TrimSpace(string(output)) != "OFFLINE_SCOPE_RESTORE_MANIFEST_DIGEST_MISMATCH" {
		t.Fatal("native independent digest refusal", e)
	}
	assertSource()
	put("actual.json", map[string]any{"status": "passed_actual_independent_restore_source_native_and_failure_guards", "source_scope_id": result.SourceScopeID, "source_manifest_sha256": r.ManifestSHA256, "original_scope_journal_unchanged": true, "all_original_files_unchanged": true, "engine_and_compose_configuration_absent": true, "no_target_or_service_created": true, "cases": results, "qualification": "readonly_backup_source_only_not_restored_installation_or_native_target"})
}
