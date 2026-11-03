//go:build training_artifact_integration && model_lifecycle_admission_integration && training_operator_integration

package training_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
)

func TestActualCpuModelPreparationNativeOperatorInitializeVerifyAndExclusiveLock(t *testing.T) {
	g := newPreparationScenario(t)
	h := newNativeTrainingOperator(t, g.f)
	h.SetPair("", g.f.s.Models.RootDirectory, g.f.s.Identity.Config.Datasets.ArtifactRoot)
	root := filepath.Join(t.TempDir(), "operator-preparations")
	if e := os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(h.Configuration)
	if e != nil {
		t.Fatal(e)
	}
	var values map[string]any
	if e = json.Unmarshal(raw, &values); e != nil {
		t.Fatal(e)
	}
	release := g.f.s.Identity.Config.ModelPreparation
	release.ArtifactRoot = root
	values["model_preparation"] = map[string]any{"artifact_root": release.ArtifactRoot, "image_reference": release.ImageReference, "image_id": release.ImageID, "worker_source_sha256": release.WorkerSourceSHA256}
	raw, e = json.Marshal(values)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(h.Configuration, raw, 0600); e != nil {
		t.Fatal(e)
	}
	args := []string{"--preparation-store-root", root, "--owner-uid", fmt.Sprint(os.Geteuid()), "--owner-gid", fmt.Sprint(os.Getegid())}
	if h.Run("initialize-preparation-store", args...) == nil {
		t.Fatal("unconfirmed initialization accepted")
	}
	entries, e := os.ReadDir(root)
	if e != nil || len(entries) != 0 {
		t.Fatal("unconfirmed initialization changed files", e)
	}
	if e = h.Run("initialize-preparation-store", append(args, "--confirm-empty-preparation-store")...); e != nil {
		t.Fatal("actual native initialization", e)
	}
	if e = h.Run("verify-preparation-files"); e != nil {
		t.Fatal("actual native verify", e)
	}
	if e = h.Run("initialize-preparation-store", append(args, "--confirm-empty-preparation-store")...); e == nil {
		t.Fatal("populated namespace reinitialized")
	}
	store, e := modelpreparations.OpenStore(root, g.f.admin.Me.Workspace.TenantID, 4096, 2147483648, 32)
	if e != nil {
		t.Fatal(e)
	}
	if e = h.Run("verify-preparation-files"); e == nil {
		store.Close()
		t.Fatal("native CLI bypassed exclusive namespace owner")
	}
	store.Close()
	conn, e := g.f.s.DB.Acquire(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Release()
	if _, e = conn.Exec(context.Background(), "SELECT pg_advisory_lock(713090002)"); e != nil {
		t.Fatal(e)
	}
	if e = h.Run("verify-preparation-files"); e == nil {
		t.Fatal("CLI bypassed installation lock")
	}
	if _, e = conn.Exec(context.Background(), "SELECT pg_advisory_unlock(713090002)"); e != nil {
		t.Fatal(e)
	}
	if count(t, g.f, "SELECT count(*) FROM audit_events WHERE action='model.preparation_store_initialize_verified'") != 1 || count(t, g.f, "SELECT count(*) FROM audit_events WHERE action='model.preparation_store_verify_verified'") != 1 {
		t.Fatal("native operator audit missing or duplicated")
	}
}
