//go:build offlineintegration

package scope

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func TestActualNativeRestoreReservationRefusesOriginalScopeWithoutMutation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	configName := os.Getenv("PROJECT_ONE_TEST_POSTGRES_RESTORE_CONFIGURATION")
	raw, e := bundle.ReadProtectedConfiguration(configName)
	if e != nil {
		t.Fatal(e)
	}
	c, e := configuration.Load(raw, nil)
	if e != nil || !c.Verification.AllowTestFixture {
		t.Fatal("explicit original synthetic fixture required", e)
	}
	r := configuration.RestoreRequest{RestoreSourceRequest: configuration.RestoreSourceDefaults(), Reason: "Explicit isolated native restore reservation refusal acceptance"}
	r.BackupRoot = os.Getenv("PROJECT_ONE_TEST_POSTGRES_RESTORE_BACKUP")
	r.ManifestSHA256 = os.Getenv("PROJECT_ONE_TEST_POSTGRES_RESTORE_MANIFEST_SHA256")
	out := os.Getenv("PROJECT_ONE_TEST_POSTGRES_RESTORE_EVIDENCE")
	if !privateDirectory(out) {
		t.Fatal("private evidence required")
	}
	root, e := os.OpenRoot(out)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	put := func(name string, value any) {
		if e := writeBackupFile(root, name, encode(value)); e != nil {
			t.Fatal(e)
		}
	}
	before, e := InspectRestoreSource(ctx, c, r.RestoreSourceRequest)
	if e != nil {
		t.Fatal(e)
	}
	journal := filepath.Join(c.ScopeRoot, journalName)
	head := filepath.Join(c.ScopeRoot, headName)
	journalBefore, e := os.ReadFile(journal)
	if e != nil {
		t.Fatal(e)
	}
	headBefore, e := os.ReadFile(head)
	if e != nil {
		t.Fatal(e)
	}
	if _, e := os.Lstat(filepath.Join(c.ScopeRoot, restorePlanFile)); !os.IsNotExist(e) {
		t.Fatal("original scope unexpectedly has a restore plan")
	}
	put("operation.json", r)
	results := []map[string]any{}
	for _, command := range []string{"prepare-restore", "reconcile-restore-reservation", "abort-restore-reservation"} {
		child := exec.CommandContext(ctx, os.Getenv("PROJECT_ONE_TEST_RESTORE_SOURCE_OPERATOR"), command, "--config", configName, "--operation-file", filepath.Join(out, "operation.json"))
		stdout, e := child.Output()
		exit, ok := e.(*exec.ExitError)
		expected := "OFFLINE_SCOPE_RESTORE_INDEPENDENT_SAME_RELEASE_TARGET_REQUIRED\n"
		if command == "abort-restore-reservation" {
			expected = "OFFLINE_SCOPE_RESTORE_RESERVATION_NOT_RECORDED\n"
		}
		if !ok || exit.ExitCode() != 1 || len(stdout) != 0 || string(exit.Stderr) != expected {
			t.Fatal("exact original-scope native refusal", command, e)
		}
		results = append(results, map[string]any{"command": command, "exit_code": 1, "expected_refusal": expected[:len(expected)-1]})
	}
	after, e := InspectRestoreSource(ctx, c, r.RestoreSourceRequest)
	if e != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("complete source changed", e)
	}
	journalAfter, e := os.ReadFile(journal)
	if e != nil || wire.Digest(journalBefore) != wire.Digest(journalAfter) {
		t.Fatal("source journal changed", e)
	}
	headAfter, e := os.ReadFile(head)
	if e != nil || wire.Digest(headBefore) != wire.Digest(headAfter) {
		t.Fatal("source journal head changed", e)
	}
	if _, e := os.Lstat(filepath.Join(c.ScopeRoot, restorePlanFile)); !os.IsNotExist(e) {
		t.Fatal("plan created in the original source")
	}
	put("actual.json", map[string]any{"status": "passed_actual_native_restore_reservation_same_source_refusal_readonly", "results": results, "qualification": "actual_native_original_scope_refusal_only_not_positive_fresh_target_reservation_restore_or_activation"})
	put("cleanup.json", map[string]bool{"no_new_target_or_services": true, "source_and_journal_unchanged": true})
}
