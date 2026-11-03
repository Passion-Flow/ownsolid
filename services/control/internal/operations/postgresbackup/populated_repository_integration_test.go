//go:build repository_integration && postgres_backup_integration

package postgresbackup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

type populatedInputs struct {
	Format                    string `json:"format"`
	BaseArchive               string `json:"base_archive"`
	BaseArchiveSHA256         string `json:"base_archive_sha256"`
	TrainingRoot              string `json:"training_root"`
	TrainingProducerSHA256    string `json:"training_producer_sha256"`
	TrainingResultSHA256      string `json:"training_result_sha256"`
	PreparationRoot           string `json:"preparation_root"`
	PreparationProducerSHA256 string `json:"preparation_producer_sha256"`
	PreparationResultSHA256   string `json:"preparation_result_sha256"`
}

type populatedReferences struct {
	BaseModel           string                 `json:"base_model"`
	DerivedModel        string                 `json:"derived_model"`
	Dataset             string                 `json:"dataset"`
	Version             string                 `json:"version"`
	TrainingJob         string                 `json:"training_job"`
	Checkpoint          string                 `json:"checkpoint"`
	Adapter             string                 `json:"adapter"`
	Preparation         string                 `json:"preparation"`
	PreparationArtifact string                 `json:"preparation_artifact"`
	TrainingAttempt     string                 `json:"training_attempt"`
	Node                string                 `json:"node"`
	Heldout             string                 `json:"heldout"`
	Files               []StoreReadback        `json:"files"`
	Rows                []populatedSQLReadback `json:"rows"`
}

// This one new case reuses setup only. No historical training or unchanged
// empty-backup test is rerun. Immutable bytes come from the approved public
// model and retained actual CPU producers; scoped metadata is synthetic.
func TestActualPostgresPopulatedFourStoreNativeBackupAndRestore(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	inputs, e := loadPopulatedInputs(ctx)
	if e != nil {
		t.Fatal("all physical input/provenance checks before SQL", e)
	}
	f := newActualPostgresBackupFixture(t, 45*time.Minute, 1800, 8<<30, 2000, 16<<20)
	refs, e := populateActualFourStores(ctx, f, inputs)
	if e != nil {
		t.Fatal("real four-owner nonempty fixture construction", e)
	}
	if e = assertPopulatedReferences(ctx, f.pool, refs); e != nil {
		t.Fatal("source SQL references", e)
	}
	refs.Rows, e = populatedDomainRows(ctx, f.pool)
	if e != nil {
		t.Fatal("source domain SQL hashes", e)
	}
	// The restore runner opens its own fresh source lease. Close only this
	// fixture's current lease after real owner verification has completed.
	f.close()
	raw, e := canonical(f.request)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile("/fixture/request-native.json", raw, 0600); e != nil {
		t.Fatal(e)
	}
	actualPostgresRestoreFromPreparedSource(t, 45*time.Minute, true)
	var manifest Manifest
	if _, e = readPrivateDocument("/fixture/backups/authority-source/candidate.json", f.request.Policy.MaximumManifestBytes, &manifest); e != nil {
		t.Fatal(e)
	}
	if len(manifest.Stores) != 4 {
		t.Fatal("four immutable store payloads required")
	}
	minimum := map[string]int{"models": 20, "datasets": 12, "training": 14, "preparations": 13}
	for _, store := range manifest.Stores {
		if store.Tree.Files < minimum[store.Kind] || store.Tree.Bytes < 1 || minimum[store.Kind] == 0 {
			t.Fatal("empty or incomplete store payload", store.Kind, store.Tree)
		}
	}
	var restore RestoreCompletion
	if _, e = readPrivateDocument("/fixture/retained-original-restore-complete.json", f.request.Policy.MaximumManifestBytes, &restore); e != nil {
		t.Fatal(e)
	}
	var targetRecord RestoreFiles
	if _, e = readPrivateDocument(filepath.Join("/fixture/restores", restore.RuntimeContext.TargetScopeID, restoreFilesName), f.request.Policy.MaximumManifestBytes, &targetRecord); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(targetRecord.Roundtrip, manifest.Stores) {
		t.Fatal("populated restored snapshot differs from source")
	}
	if e = assertPopulatedTargetReferences(ctx, f, restore, refs); e != nil {
		t.Fatal("restored SQL references and file bytes", e)
	}
	root, e := openPrivateRoot("/fixture")
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	if e = writeDocument(root, "actual-populated-receipt.json", map[string]any{"status": "passed_actual_nonempty_four_owner_native_restore", "references": refs, "stores": manifest.Stores, "manifest_sha256": restore.ManifestSHA256, "restore": restore, "inputs": inputs, "scope": "Real public approved model and retained CPU-produced tensor bytes with explicit synthetic scope metadata. No new training/GPU/provider qualification. Served positive/old-authority and native fault cases are separate following required checks."}, f.request.Policy.MaximumManifestBytes); e != nil {
		t.Fatal(e)
	}
}

func loadPopulatedInputs(ctx context.Context) (populatedInputs, error) {
	var in populatedInputs
	if ctx == nil || os.Geteuid() != 65534 {
		return in, errors.New("M18_EXACT_NONROOT_POPULATED_FIXTURE_REQUIRED")
	}
	if _, e := readPrivateDocument("/fixture/populated-inputs.json", MaximumRequestBytes, &in); e != nil {
		return in, e
	}
	if in.Format != "projectone.m18.populated-inputs.v1" || in.BaseArchive != "/fixture/inputs/base.tar" || in.TrainingRoot != "/fixture/inputs/training" || in.PreparationRoot != "/fixture/inputs/preparation" || !digestPattern.MatchString(in.BaseArchiveSHA256) || !digestPattern.MatchString(in.TrainingProducerSHA256) || !digestPattern.MatchString(in.PreparationProducerSHA256) {
		return in, errors.New("M18_POPULATED_ASSET_BINDING_INVALID")
	}
	if e := verifyPopulatedAssets(ctx, in); e != nil {
		return in, e
	}
	return in, nil
}
