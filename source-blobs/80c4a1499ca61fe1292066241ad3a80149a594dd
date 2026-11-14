//go:build repository_integration && postgres_backup_integration

package postgresbackup

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/api/agent/modelartifact"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	lifecyclewire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
	trainingwire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

func verifyPopulatedAssets(ctx context.Context, in populatedInputs) error {
	bad := errors.New("M18_REAL_CPU_OR_PUBLIC_MODEL_INPUT_REQUIRED")
	for _, entry := range []struct{ path, sha, status string }{{filepath.Join(in.TrainingRoot, "producer.json"), in.TrainingProducerSHA256, "passed_actual_cpu_training_core_partial"}, {filepath.Join(in.PreparationRoot, "producer.json"), in.PreparationProducerSHA256, "passed_actual_model_preparation_process_partial"}} {
		raw, e := readPrivateBytes(entry.path, 4<<20)
		if e != nil || digest(raw) != entry.sha {
			return bad
		}
		var meta struct {
			Status string `json:"status"`
		}
		if json.Unmarshal(raw, &meta) != nil || meta.Status != entry.status {
			return bad
		}
	}
	for _, entry := range []struct{ root, sha, kind string }{{in.TrainingRoot, in.TrainingResultSHA256, "training"}, {in.PreparationRoot, in.PreparationResultSHA256, "preparation"}} {
		if !digestPattern.MatchString(entry.sha) {
			return bad
		}
		raw, e := readPrivateBytes(filepath.Join(entry.root, "result.json"), 8<<20)
		if e != nil || digest(raw) != entry.sha {
			return bad
		}
		var result struct {
			Status        string           `json:"status"`
			DeviceProfile string           `json:"device_profile"`
			Files         []datawire.Asset `json:"files"`
			ModelFiles    []datawire.Asset `json:"model_files"`
		}
		if json.Unmarshal(raw, &result) != nil || result.DeviceProfile != "cpu_verification" {
			return bad
		}
		assets := result.Files
		prefix := entry.root
		if entry.kind == "preparation" {
			if result.Status != "prepared" || len(result.ModelFiles) != 9 {
				return bad
			}
			assets = result.ModelFiles
			prefix = filepath.Join(entry.root, "model")
		} else if result.Status != "trained" || len(assets) < 10 {
			return bad
		}
		for _, asset := range assets {
			path := filepath.Join(prefix, filepath.FromSlash(asset.Path))
			if !contained(prefix, path) || asset.SizeBytes < 1 || !digestPattern.MatchString(asset.SHA256) {
				return bad
			}
			proof, e := fileDigest(ctx, path, 2<<30)
			if e != nil || proof.SHA256 != asset.SHA256 || proof.Bytes != asset.SizeBytes {
				return bad
			}
		}
	}
	return readApprovedBaseArchive(ctx, in, func(tarHeader *tar.Header, body io.Reader) error {
		_, _, e := hashStream(ctx, nil, body, tarHeader.Size)
		return e
	})
}

func fixtureWrite(path string, raw []byte) error {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	r, e := openPrivateRoot(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer r.Close()
	return writeNew(r, filepath.Base(path), raw)
}
func fixtureJSON(path string, value any) error {
	raw, e := canonical(value)
	if e != nil {
		return e
	}
	return fixtureWrite(path, raw)
}
func fixtureCopy(ctx context.Context, source, destination string, asset datawire.Asset) error {
	proof, e := fileDigest(ctx, source, 2<<30)
	if e != nil || proof.SHA256 != asset.SHA256 || proof.Bytes != asset.SizeBytes {
		return errors.New("M18_RETAINED_CPU_ASSET_CHANGED")
	}
	if e = os.MkdirAll(filepath.Dir(destination), 0700); e != nil {
		return e
	}
	in, e := os.Open(source)
	if e != nil {
		return e
	}
	defer in.Close()
	r, e := openPrivateRoot(filepath.Dir(destination))
	if e != nil {
		return e
	}
	defer r.Close()
	out, e := r.OpenFile(filepath.Base(destination), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	sha, n, e := hashStream(ctx, out, in, asset.SizeBytes)
	if e == nil && (sha != asset.SHA256 || n != asset.SizeBytes) {
		e = errors.New("M18_RETAINED_CPU_ASSET_CHANGED")
	}
	if e == nil {
		e = out.Sync()
	}
	return errors.Join(e, out.Close(), syncRoot(r))
}

func populateDataset(ctx context.Context, f actualPostgresBackupFixture, tenant, workspace, user string, refs *populatedReferences) error {
	return populateNamedDataset(ctx, f, tenant, workspace, user, "training", refs)
}
func populateNamedDataset(ctx context.Context, f actualPostgresBackupFixture, tenant, workspace, user, label string, refs *populatedReferences) error {
	refs.Dataset, refs.Version = uuid.NewString(), uuid.NewString()
	draft, job, attempt, object := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	first, _ := canonical(datawire.Record{SourceID: "m18-" + label + "-a", Prompt: "A synthetic " + label + " question", Completion: "A retained answer"})
	second, _ := canonical(datawire.Record{SourceID: "m18-" + label + "-b", Prompt: "Another synthetic " + label + " question", Completion: "Another retained answer"})
	rows := [][]byte{first, second}
	source := append(append([]byte{}, rows[0]...), rows[1]...)
	if label == "heldout" {
		rows = nil
		source = nil
		for _, name := range lifecyclewire.Labels() {
			row, _ := canonical(datawire.Record{SourceID: "m18-heldout-" + name, Prompt: "Synthetic retained " + name + " fixture", Completion: name})
			rows = append(rows, row)
			source = append(source, row...)
		}
	}
	assignmentA, _ := canonical(map[string]string{"source_id": "m18-" + label + "-a", "split": "train"})
	assignmentB, _ := canonical(map[string]string{"source_id": "m18-" + label + "-b", "split": "validation"})
	files := map[string][]byte{"samples.jsonl": source, "train.jsonl": rows[0], "validation.jsonl": rows[1], "assignments.jsonl": append(assignmentA, assignmentB...), "duplicates.jsonl": []byte{}}
	manifest := datawire.Manifest{SchemaVersion: 1, RowSchema: datawire.RowSchema, ProcessorVersion: datawire.ProcessorVersion, SplitAlgorithm: datawire.SplitAlgorithm, SourceSHA256: digest(source), Seed: strings.Repeat("1", 32), ValidationBasisPoints: 5000, TotalRows: 2, UniqueRows: 2, SourceGroups: 2, ValidationGroups: 1, TrainRows: 1, ValidationRows: 1, Files: []datawire.Asset{}}
	if label == "heldout" {
		files["samples.jsonl"] = source
		files["train.jsonl"] = append(append([]byte{}, rows[0]...), rows[1]...)
		files["validation.jsonl"] = rows[2]
		files["assignments.jsonl"] = nil
		for n, name := range lifecyclewire.Labels() {
			split := "train"
			if n == 2 {
				split = "validation"
			}
			row, _ := canonical(map[string]string{"source_id": "m18-heldout-" + name, "split": split})
			files["assignments.jsonl"] = append(files["assignments.jsonl"], row...)
		}
		manifest.TotalRows = 3
		manifest.UniqueRows = 3
		manifest.SourceGroups = 3
		manifest.TrainRows = 2
	}
	for _, name := range []string{"samples.jsonl", "train.jsonl", "validation.jsonl", "assignments.jsonl", "duplicates.jsonl"} {
		manifest.Files = append(manifest.Files, datawire.Asset{Path: name, SizeBytes: int64(len(files[name])), SHA256: digest(files[name])})
	}
	logical, _ := json.Marshal(manifest)
	var body map[string]json.RawMessage
	if e := json.Unmarshal(logical, &body); e != nil {
		return e
	}
	delete(body, "manifest_file")
	logical, e := canonical(body)
	if e != nil {
		return e
	}
	files["manifest.json"] = logical
	manifest.ManifestFile = datawire.Asset{Path: "manifest.json", SizeBytes: int64(len(logical)), SHA256: digest(logical)}
	root := f.runtime.Datasets.ArtifactRoot
	store, e := datasets.OpenStore(root, tenant, f.runtime.Policy().Datasets.MinimumFreeBytes)
	if e != nil {
		return e
	}
	defer store.Close()
	prefix := filepath.Join(root, "versions", refs.Version, attempt)
	for name, raw := range files {
		if e = fixtureWrite(filepath.Join(prefix, name), raw); e != nil {
			return e
		}
	}
	mapping := []byte("{\"line_number\":1,\"source_line_number\":1}\n{\"line_number\":2,\"source_line_number\":2}\n")
	if label == "heldout" {
		mapping = append(mapping, []byte("{\"line_number\":3,\"source_line_number\":3}\n")...)
	}
	for name, raw := range map[string][]byte{"effective.jsonl": source, "mapping.jsonl": mapping} {
		if e = fixtureWrite(filepath.Join(prefix, name), raw); e != nil {
			return e
		}
	}
	receipt := map[string]any{"schema_version": 1, "store_id": store.Namespace(), "tenant_id": tenant, "version_id": refs.Version, "draft_id": draft, "job_id": job, "attempt_id": attempt, "effective_sha256": digest(source), "mapping_sha256": digest(mapping), "manifest_sha256": manifest.ManifestFile.SHA256}
	if e = fixtureJSON(filepath.Join(prefix, "publication.json"), receipt); e != nil {
		return e
	}
	if e = fixtureWrite(filepath.Join(root, "objects", object, "source.jsonl"), source); e != nil {
		return e
	}
	if e = store.VerifyVersion(refs.Version, draft, job, attempt, digest(source), digest(mapping), manifest); e != nil {
		return e
	}
	jsonManifest, _ := json.Marshal(manifest)
	queries := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO datasets(id,tenant_id,workspace_id,name,name_key,origin,license,rights_asserted,created_by,creation_key,creation_fingerprint) VALUES($1,$2,$3,$4,$4,'local_upload','Synthetic fixture rights',true,$5,$6,$7)`, []any{refs.Dataset, tenant, workspace, "Synthetic retained " + label + " corpus", user, uuid.NewString(), make([]byte, 32)}},
		{`INSERT INTO dataset_drafts(id,tenant_id,workspace_id,dataset_id,created_by,requested_audience,requested_epoch,source_object_id,source_name,source_sha256,expected_bytes,state,creation_key,creation_fingerprint,source_received_at) VALUES($1,$2,$3,$4,$5,'admin',1,$6,'synthetic.jsonl',$7,$8,'published',$9,$10,clock_timestamp())`, []any{draft, tenant, workspace, refs.Dataset, user, object, digest(source), len(source), uuid.NewString(), make([]byte, 32)}},
		{`INSERT INTO dataset_jobs(id,tenant_id,workspace_id,dataset_id,draft_id,kind,requested_by,requested_audience,requested_epoch,reason,expected_revision,state,input_policy,creation_key,creation_fingerprint,version_id,split_seed,validation_basis_points,completed_at) VALUES($1,$2,$3,$4,$5,'publish',$6,'admin',1,'Synthetic immutable publication fixture',1,'succeeded','{}'::jsonb,$7,$8,$9,$10,5000,clock_timestamp())`, []any{job, tenant, workspace, refs.Dataset, draft, user, uuid.NewString(), make([]byte, 32), refs.Version, manifest.Seed}},
		{`INSERT INTO dataset_job_attempts(id,tenant_id,workspace_id,dataset_id,draft_id,job_id,attempt_number,state,finished_at) VALUES($1,$2,$3,$4,$5,$6,1,'succeeded',clock_timestamp())`, []any{attempt, tenant, workspace, refs.Dataset, draft, job}},
		{`INSERT INTO dataset_versions(id,tenant_id,workspace_id,dataset_id,draft_id,job_id,attempt_id,ordinal,published_by,raw_source_sha256,effective_source_sha256,manifest_sha256,manifest,row_mapping_sha256) VALUES($1,$2,$3,$4,$5,$6,$7,1,$8,$9,$9,$10,$11::jsonb,$12)`, []any{refs.Version, tenant, workspace, refs.Dataset, draft, job, attempt, user, digest(source), manifest.ManifestFile.SHA256, string(jsonManifest), digest(mapping)}},
	}
	tx, e := f.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	for _, q := range queries {
		if _, e = tx.Exec(ctx, q.sql, q.args...); e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}

func reboundCPUJSON(raw []byte, ids map[string]string) ([]byte, error) {
	var value any
	if e := json.Unmarshal(raw, &value); e != nil {
		return nil, e
	}
	var visit func(any)
	visit = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, child := range x {
				if replacement, ok := ids[k]; ok {
					x[k] = replacement
				} else {
					visit(child)
				}
			}
		case []any:
			for _, child := range x {
				visit(child)
			}
		}
	}
	visit(value)
	return canonical(value)
}

func reboundTrainingResult(raw []byte, ids map[string]string, checkpoint []byte) ([]byte, error) {
	bound, e := reboundCPUJSON(raw, ids)
	if e != nil {
		return nil, e
	}
	var result map[string]json.RawMessage
	if e = json.Unmarshal(bound, &result); e != nil {
		return nil, e
	}
	var checkpoints []trainingwire.Checkpoint
	var files []datawire.Asset
	if json.Unmarshal(result["checkpoints"], &checkpoints) != nil || json.Unmarshal(result["files"], &files) != nil {
		return nil, errors.New("M18_CPU_RESULT_REFERENCES_REQUIRED")
	}
	foundCheckpoint, foundFile := false, false
	for n := range checkpoints {
		if checkpoints[n].Path == "checkpoint-1" {
			checkpoints[n].ReceiptSHA256 = digest(checkpoint)
			foundCheckpoint = true
		}
	}
	for n := range files {
		if files[n].Path == "checkpoints/checkpoint-1/projectone-checkpoint.json" {
			files[n].SHA256 = digest(checkpoint)
			files[n].SizeBytes = int64(len(checkpoint))
			foundFile = true
		}
	}
	if !foundCheckpoint || !foundFile {
		return nil, errors.New("M18_CPU_RESULT_REFERENCES_REQUIRED")
	}
	result["checkpoints"], e = json.Marshal(checkpoints)
	if e != nil {
		return nil, e
	}
	result["files"], e = json.Marshal(files)
	if e != nil {
		return nil, e
	}
	return canonical(result)
}

func populatedTrainingBindings(raw []byte) (trainingwire.Bindings, error) {
	var result trainingwire.Result
	if json.Unmarshal(raw, &result) != nil || result.Bindings == nil {
		return trainingwire.Bindings{}, errors.New("M18_CPU_CONFIGURATION_REQUIRED")
	}
	b := *result.Bindings
	sha, e := b.Configuration.Digest()
	if e != nil || sha != b.ConfigurationSHA256 || !digestPattern.MatchString(b.TrainSHA256) || !digestPattern.MatchString(b.ValidationSHA256) {
		return b, errors.New("M18_CPU_CONFIGURATION_REQUIRED")
	}
	return b, nil
}

func TestPopulatedCPUBindingsUseNestedConfigurationAndSelectedFileHashes(t *testing.T) {
	configuration := trainingwire.DefaultConfiguration()
	configuration.Device = trainingwire.VerificationProfile
	sha, e := configuration.Digest()
	if e != nil {
		t.Fatal(e)
	}
	train, validation := digest([]byte("selected train\n")), digest([]byte("selected validation\n"))
	raw, _ := json.Marshal(trainingwire.Result{Bindings: &trainingwire.Bindings{Configuration: configuration, ConfigurationSHA256: sha, TrainSHA256: train, ValidationSHA256: validation}})
	b, e := populatedTrainingBindings(raw)
	if e != nil || b.Configuration != configuration || b.ConfigurationSHA256 != sha || b.TrainSHA256 != train || b.ValidationSHA256 != validation {
		t.Fatal("nested CPU bindings", e)
	}
	if _, e = populatedTrainingBindings([]byte(`{"configuration":{},"configuration_sha256":"top-level"}`)); e == nil {
		t.Fatal("wrong top-level shape accepted")
	}
}

func TestPopulatedCPUResultRebindsExactCheckpointDigestSizeAndDataset(t *testing.T) {
	original := []byte(`{"job_id":"old","dataset_manifest_sha256":"old-dataset","checkpoints":[{"global_step":1,"path":"checkpoint-1","receipt_sha256":"old"}],"files":[{"path":"checkpoints/checkpoint-1/projectone-checkpoint.json","size_bytes":3,"sha256":"old"}]}`)
	checkpoint := []byte("new canonical synthetic checkpoint metadata\n")
	bound, e := reboundTrainingResult(original, map[string]string{"job_id": "new-synthetic-job", "dataset_manifest_sha256": "selected-dataset"}, checkpoint)
	if e != nil {
		t.Fatal(e)
	}
	var result struct {
		JobID       string                    `json:"job_id"`
		DatasetSHA  string                    `json:"dataset_manifest_sha256"`
		Checkpoints []trainingwire.Checkpoint `json:"checkpoints"`
		Files       []datawire.Asset          `json:"files"`
	}
	if json.Unmarshal(bound, &result) != nil || result.JobID != "new-synthetic-job" || result.DatasetSHA != "selected-dataset" || result.Checkpoints[0].ReceiptSHA256 != digest(checkpoint) || result.Files[0].SHA256 != digest(checkpoint) || result.Files[0].SizeBytes != int64(len(checkpoint)) {
		t.Fatal("stale result cross-reference")
	}
	if !bytes.Contains(original, []byte(`"receipt_sha256":"old"`)) {
		t.Fatal("original CPU producer bytes changed")
	}
	if _, e = reboundTrainingResult([]byte(`{"checkpoints":[],"files":[]}`), nil, checkpoint); e == nil {
		t.Fatal("missing source checkpoint references accepted")
	}
}

func populateTraining(ctx context.Context, f actualPostgresBackupFixture, in populatedInputs, tenant, workspace, user string, refs *populatedReferences) error {
	refs.TrainingJob, refs.Checkpoint, refs.Adapter = uuid.NewString(), uuid.NewString(), uuid.NewString()
	attempt, node := uuid.NewString(), uuid.NewString()
	refs.TrainingAttempt, refs.Node = attempt, node
	store, e := training.OpenStore(f.runtime.Training.ArtifactRoot, tenant, f.runtime.Policy().Training.MinimumFreeBytes, trainingwire.MaximumCheckpointBytes, trainingwire.MaximumCheckpointFiles)
	if e != nil {
		return e
	}
	defer store.Close()
	root := f.runtime.Training.ArtifactRoot
	ids := map[string]string{"tenant_id": tenant, "workspace_id": workspace, "job_id": refs.TrainingJob, "attempt_id": attempt, "dataset_version_id": refs.Version, "model_id": refs.BaseModel}
	var datasetSHA string
	var datasetManifest datawire.Manifest
	var datasetRaw []byte
	if e = f.pool.QueryRow(ctx, `SELECT manifest_sha256,manifest FROM dataset_versions WHERE id=$1`, refs.Version).Scan(&datasetSHA, &datasetRaw); e != nil {
		return e
	}
	if e = json.Unmarshal(datasetRaw, &datasetManifest); e != nil {
		return e
	}
	ids["dataset_manifest_sha256"] = datasetSHA
	for _, asset := range datasetManifest.Files {
		if asset.Path == "train.jsonl" {
			ids["train_sha256"] = asset.SHA256
		}
		if asset.Path == "validation.jsonl" {
			ids["validation_sha256"] = asset.SHA256
		}
	}
	checkpointRaw, e := readPrivateBytes(filepath.Join(in.TrainingRoot, "checkpoints", "checkpoint-1", "projectone-checkpoint.json"), 8<<20)
	if e != nil {
		return e
	}
	checkpointRaw, e = reboundCPUJSON(checkpointRaw, ids)
	if e != nil {
		return e
	}
	resultRaw, e := readPrivateBytes(filepath.Join(in.TrainingRoot, "result.json"), 8<<20)
	if e != nil {
		return e
	}
	resultRaw, e = reboundTrainingResult(resultRaw, ids, checkpointRaw)
	if e != nil {
		return e
	}
	bindings, e := populatedTrainingBindings(resultRaw)
	if e != nil {
		return e
	}
	configuration, e := json.Marshal(bindings.Configuration)
	if e != nil {
		return e
	}
	configurationSHA := bindings.ConfigurationSHA256
	descriptors := []training.UploadDescriptor{}
	for _, kind := range []string{"checkpoint", "artifact"} {
		id, prefix := refs.Adapter, filepath.Join(in.TrainingRoot, "adapter")
		names := []string{"adapter_model.safetensors", "adapter_config.json", "README.md"}
		manifest := resultRaw
		if kind == "checkpoint" {
			id = refs.Checkpoint
			prefix = filepath.Join(in.TrainingRoot, "checkpoints", "checkpoint-1")
			names = []string{"adapter_model.safetensors", "adapter_config.json", "optimizer.pt", "scheduler.pt", "trainer_state.json", "rng_state.pth", "projectone-checkpoint.json"}
			manifest = checkpointRaw
		}
		d := training.UploadDescriptor{SchemaVersion: 1, Scope: trainingwire.Scope{TenantID: tenant, WorkspaceID: workspace, JobID: refs.TrainingJob}, UploadID: id, AttemptID: attempt, Kind: kind, GlobalStep: 1, ManifestSHA256: digest(manifest), Files: []datawire.Asset{}}
		for _, name := range names {
			path := filepath.Join(prefix, name)
			proof, e := fileDigest(ctx, path, trainingwire.MaximumCheckpointBytes)
			if e != nil {
				return e
			}
			if name == "projectone-checkpoint.json" {
				proof = FileReadback{digest(checkpointRaw), int64(len(checkpointRaw))}
			}
			d.Files = append(d.Files, datawire.Asset{Path: name, SizeBytes: proof.Bytes, SHA256: proof.SHA256})
		}
		if e = d.Validate(trainingwire.MaximumCheckpointBytes, trainingwire.MaximumCheckpointFiles); e != nil {
			return e
		}
		kindDir := "artifacts"
		if kind == "checkpoint" {
			kindDir = "checkpoints"
		}
		published := filepath.Join(root, kindDir, refs.TrainingJob, attempt, id)
		for _, asset := range d.Files {
			if asset.Path == "projectone-checkpoint.json" {
				e = fixtureWrite(filepath.Join(published, asset.Path), checkpointRaw)
			} else {
				e = fixtureCopy(ctx, filepath.Join(prefix, asset.Path), filepath.Join(published, asset.Path), asset)
			}
			if e != nil {
				return e
			}
		}
		if e = fixtureJSON(filepath.Join(published, "publication.json"), map[string]any{"schema_version": 1, "store_id": store.Namespace(), "descriptor": d}); e != nil {
			return e
		}
		if e = store.VerifyPublished(ctx, d); e != nil {
			return e
		}
		descriptors = append(descriptors, d)
	}
	queries := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO managed_nodes(id,tenant_id,workspace_id,name,status,protocol_version,facts,last_heartbeat_at,credential_hash) VALUES($1,$2,$3,'Synthetic offline CPU-reference node','active','1.5','{}'::jsonb,clock_timestamp(),$4)`, []any{node, tenant, workspace, make([]byte, 32)}},
		{`INSERT INTO training_jobs(id,tenant_id,workspace_id,node_id,current_node_id,model_id,dataset_id,dataset_version_id,gpu_uuid,configuration,configuration_sha256,execution_recipe,dataset_manifest_sha256,model_catalog_sha256,created_by,requested_by,requested_audience,requested_epoch,reason,creation_key,creation_fingerprint,state) VALUES($1,$2,$3,$4,$4,$5,$6,$7,$8,$9::jsonb,$10,'{}'::jsonb,$11,$12,$13,$13,'admin',1,'Synthetic retained CPU scope; no new training or physical GPU claim',$14,$15,'failed')`, []any{refs.TrainingJob, tenant, workspace, node, refs.BaseModel, refs.Dataset, refs.Version, "GPU-" + uuid.NewString(), string(configuration), configurationSHA, datasetSHA, agentwire.ModelCatalogDigest(), user, uuid.NewString(), make([]byte, 32)}},
		{`INSERT INTO training_attempts(id,tenant_id,workspace_id,job_id,node_id,attempt_number,state,requested_by,requested_audience,requested_epoch,input_inventory) VALUES($1,$2,$3,$4,$5,1,'failed',$6,'admin',1,'{}'::jsonb)`, []any{attempt, tenant, workspace, refs.TrainingJob, node, user}},
	}
	for _, q := range queries {
		if _, e = f.pool.Exec(ctx, q.sql, q.args...); e != nil {
			return e
		}
	}
	for _, d := range descriptors {
		raw, _ := json.Marshal(d)
		path := filepath.Join("artifacts", refs.TrainingJob, attempt, d.UploadID)
		manifest := resultRaw
		query := `INSERT INTO training_artifacts(id,tenant_id,workspace_id,job_id,attempt_id,manifest_sha256,manifest,file_inventory,storage_key) VALUES($1,$2,$3,$4,$5,$6,$7::jsonb,$8::jsonb,$9)`
		if d.Kind == "checkpoint" {
			path = filepath.Join("checkpoints", refs.TrainingJob, attempt, d.UploadID)
			manifest = checkpointRaw
			query = `INSERT INTO training_checkpoints(id,tenant_id,workspace_id,job_id,attempt_id,receipt_sha256,receipt,file_inventory,storage_key,global_step) VALUES($1,$2,$3,$4,$5,$6,$7::jsonb,$8::jsonb,$9,1)`
		}
		if _, e = f.pool.Exec(ctx, `INSERT INTO training_uploads(id,tenant_id,workspace_id,job_id,attempt_id,kind,global_step,manifest_sha256,manifest,state,manifest_bytes) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb,'accepted',$10)`, d.UploadID, tenant, workspace, refs.TrainingJob, attempt, d.Kind, d.GlobalStep, d.ManifestSHA256, string(manifest), manifest); e != nil {
			return e
		}
		if _, e = f.pool.Exec(ctx, query, d.UploadID, tenant, workspace, refs.TrainingJob, attempt, d.ManifestSHA256, string(manifest), string(raw), path); e != nil {
			return e
		}
	}
	return nil
}

func readApprovedBaseArchive(ctx context.Context, in populatedInputs, consume func(*tar.Header, io.Reader) error) error {
	return privateArchive(ctx, in.BaseArchive, 2<<30, in.BaseArchiveSHA256, func(file *os.File) error {
		reader := tar.NewReader(file)
		seen := map[string]bool{}
		for {
			header, e := reader.Next()
			if e == io.EOF {
				break
			}
			if e != nil {
				return e
			}
			asset, ok := agentwire.ModelFileForName(header.Name)
			if !ok || seen[header.Name] || header.Typeflag != tar.TypeReg || header.Size != asset.SizeBytes || header.Linkname != "" {
				return errors.New("M18_APPROVED_BASE_ARCHIVE_INVALID")
			}
			seen[header.Name] = true
			// Verify each real model file independently of the outer tar digest.
			hash := sha256.New()
			limited := &io.LimitedReader{R: reader, N: header.Size}
			if e = consume(header, io.TeeReader(limited, hash)); e != nil {
				return e
			}
			if limited.N != 0 || hex.EncodeToString(hash.Sum(nil)) != asset.SHA256 {
				return errors.New("M18_APPROVED_BASE_FILE_CHANGED")
			}
		}
		if len(seen) != len(agentwire.ApprovedModel().Files) {
			return errors.New("M18_APPROVED_BASE_ARCHIVE_INVALID")
		}
		return nil
	})
}

// The source archive and compiled catalog supply the only filenames. The
// fixture publishes real immutable files in a fresh owned root, then invokes
// the production verifier; it never changes that verifier or source assets.
func populateApprovedBase(ctx context.Context, f actualPostgresBackupFixture, in populatedInputs, tenant, workspace, user string, refs *populatedReferences) error {
	refs.BaseModel = uuid.NewString()
	objects := filepath.Join(f.runtime.Models.ArtifactRoot, "objects")
	if _, e := os.Lstat(objects); os.IsNotExist(e) {
		r, e := newDirectory(objects)
		if e != nil {
			return e
		}
		if e = r.Close(); e != nil {
			return e
		}
	}
	root, e := newDirectory(filepath.Join(objects, refs.BaseModel))
	if e != nil {
		return e
	}
	defer root.Close()
	if e = readApprovedBaseArchive(ctx, in, func(header *tar.Header, body io.Reader) error {
		asset, _ := agentwire.ModelFileForName(header.Name)
		file, e := root.OpenFile(header.Name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		sha, n, e := hashStream(ctx, file, body, asset.SizeBytes)
		if e == nil && (sha != asset.SHA256 || n != asset.SizeBytes) {
			e = errors.New("M18_APPROVED_MODEL_BYTES_CHANGED")
		}
		if e == nil {
			e = file.Chmod(0444)
		}
		if e == nil {
			e = file.Sync()
		}
		return errors.Join(e, file.Close())
	}); e != nil {
		return e
	}
	if e = syncRoot(root); e != nil {
		return e
	}
	if e = modelartifact.VerifyDirectory(ctx, filepath.Join(objects, refs.BaseModel)); e != nil {
		return e
	}
	manifest, e := json.Marshal(agentwire.ApprovedModel())
	if e != nil {
		return e
	}
	_, e = f.pool.Exec(ctx, `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES($1,$2,$3,'Synthetic populated approved base',$4,$5,$6::jsonb,'ready',$7)`, refs.BaseModel, tenant, workspace, agentwire.ApprovedModel().ModelKey, agentwire.ModelCatalogDigest(), string(manifest), user)
	return e
}

func assertPopulatedReferences(ctx context.Context, pool *pgxpool.Pool, refs populatedReferences) error {
	if ctx == nil || pool == nil {
		return errors.New("M18_POPULATED_SQL_CONTEXT_REQUIRED")
	}
	for _, entry := range []struct{ query, id string }{{`SELECT count(*) FROM model_versions WHERE id=$1::uuid AND state='ready'`, refs.BaseModel}, {`SELECT count(*) FROM model_versions WHERE id=$1::uuid AND state='ready' AND kind='merged_lora'`, refs.DerivedModel}, {`SELECT count(*) FROM dataset_versions WHERE id=$1::uuid`, refs.Version}, {`SELECT count(*) FROM training_checkpoints WHERE id=$1::uuid`, refs.Checkpoint}, {`SELECT count(*) FROM training_artifacts WHERE id=$1::uuid`, refs.Adapter}, {`SELECT count(*) FROM model_preparation_artifacts WHERE id=$1::uuid AND octet_length(report_bytes)>2`, refs.PreparationArtifact}} {
		var n int
		if pool.QueryRow(ctx, entry.query, entry.id).Scan(&n) != nil || n != 1 {
			return errors.New("M18_NONEMPTY_EXACT_SQL_REFERENCE_REQUIRED")
		}
	}
	return nil
}

func populateActualFourStores(ctx context.Context, f actualPostgresBackupFixture, in populatedInputs) (refs populatedReferences, result error) {
	var tenant, workspace, user string
	if e := f.pool.QueryRow(ctx, `SELECT tenant_id::text,default_workspace_id::text,primary_admin_user_id::text FROM installation_state WHERE id=1 AND bootstrap_complete`).Scan(&tenant, &workspace, &user); e != nil {
		return refs, e
	}
	accounts := &identity.Service{DB: f.pool, Config: f.runtime}
	if e := populateApprovedBase(ctx, f, in, tenant, workspace, user, &refs); e != nil {
		return refs, e
	}
	if e := populateDataset(ctx, f, tenant, workspace, user, &refs); e != nil {
		return refs, e
	}
	if e := populateTraining(ctx, f, in, tenant, workspace, user, &refs); e != nil {
		return refs, e
	}
	prepared, e := populatePreparation(ctx, f, in, tenant, workspace, user, &refs)
	if e != nil {
		return refs, e
	}
	if e = populateDerivedModel(ctx, f, prepared, tenant, workspace, user, &refs); e != nil {
		return refs, e
	}
	owners, e := openOwners(ctx, f.runtime, accounts, tenant)
	if e != nil {
		return refs, e
	}
	defer func() { result = errors.Join(result, owners.Close()) }()
	if e = owners.Verify(ctx); e != nil {
		return refs, e
	}
	refs.Files, e = sourceStoreReadbacks(ctx, f.runtime, f.request)
	return refs, e
}

func sourceStoreReadbacks(ctx context.Context, cfg config.Config, r release.PostgreSQLBackupRequest) ([]StoreReadback, error) {
	out := []StoreReadback{}
	for n, kind := range []string{"models", "datasets", "training", "preparations"} {
		tree, e := TreeDigest(ctx, storeRoots(cfg)[n], r.Policy.MaximumStoreBytes, r.Policy.MaximumStoreFiles, r.Policy.MaximumManifestBytes)
		if e != nil {
			return nil, e
		}
		out = append(out, StoreReadback{kind, tree})
	}
	return out, nil
}

// These rows describe a synthetic restoration fixture, not a new training,
// evaluation or Admin approval. Original CPU producer/report bytes stay in
// inputs; only scoped fixture metadata is rebound. All SQL constraints and
// production storage verifiers remain enabled.
func populatePreparation(ctx context.Context, f actualPostgresBackupFixture, in populatedInputs, tenant, workspace, user string, refs *populatedReferences) (modelpreparations.UploadDescriptor, error) {
	var d modelpreparations.UploadDescriptor
	var heldoutRefs populatedReferences
	if e := populateNamedDataset(ctx, f, tenant, workspace, user, "heldout", &heldoutRefs); e != nil {
		return d, e
	}
	refs.Heldout = uuid.NewString()
	refs.Preparation = uuid.NewString()
	refs.PreparationArtifact = uuid.NewString()
	attempt, task := uuid.NewString(), uuid.NewString()
	var manifestSHA, effectiveSHA, mappingSHA string
	var manifestRaw []byte
	if e := f.pool.QueryRow(ctx, `SELECT manifest_sha256,effective_source_sha256,row_mapping_sha256,manifest FROM dataset_versions WHERE id=$1`, heldoutRefs.Version).Scan(&manifestSHA, &effectiveSHA, &mappingSHA, &manifestRaw); e != nil {
		return d, e
	}
	var heldoutManifest datawire.Manifest
	if e := json.Unmarshal(manifestRaw, &heldoutManifest); e != nil {
		return d, e
	}
	var sourceBytes int64
	for _, asset := range heldoutManifest.Files {
		if asset.Path == "samples.jsonl" {
			sourceBytes = asset.SizeBytes
		}
	}
	_, e := f.pool.Exec(ctx, `INSERT INTO model_holdouts(id,tenant_id,workspace_id,dataset_id,dataset_version_id,name,manifest_sha256,effective_source_sha256,row_mapping_sha256,source_bytes,row_count,label_counts,recipe_id,recipe_sha256,instruction_sha256,fingerprint_algorithm,created_by,audience,reason,creation_key,creation_fingerprint) VALUES($1,$2,$3,$4,$5,'Synthetic restoration holdout',$6,$7,$8,$9,3,'{"account":1,"billing":1,"technical":1}'::jsonb,$10,$11,$12,$13,$14,'admin','Synthetic metadata; retained CPU bytes, no new evaluation',$15,$16)`, refs.Heldout, tenant, workspace, heldoutRefs.Dataset, heldoutRefs.Version, manifestSHA, effectiveSHA, mappingSHA, sourceBytes, lifecyclewire.RecipeID, lifecyclewire.RecipeDigest(), lifecyclewire.InstructionDigest(), lifecyclewire.PromptFingerprintAlgorithm, user, uuid.NewString(), make([]byte, 32))
	if e != nil {
		return d, e
	}
	raw, e := readPrivateBytes(filepath.Join(in.PreparationRoot, "result.json"), lifecyclewire.MaximumResultBytes)
	if e != nil {
		return d, e
	}
	raw, e = reboundCPUJSON(raw, map[string]string{"tenant_id": tenant, "workspace_id": workspace, "preparation_id": refs.Preparation, "attempt_id": attempt, "training_job_id": refs.TrainingJob, "training_attempt_id": refs.TrainingAttempt, "heldout_id": refs.Heldout, "heldout_sha256": effectiveSHA})
	if e != nil {
		return d, e
	}
	var report lifecyclewire.PreparedResult
	if e = lifecyclewire.DecodeStrict(raw, &report); e != nil {
		return d, e
	}
	// Retained scores/predictions are provenance only. The fixture does not
	// submit this synthetic report to a quality/review API or claim a new run.
	configuration, e := json.Marshal(report.Configuration)
	if e != nil {
		return d, e
	}
	configurationSHA, e := report.Configuration.Digest()
	if e != nil || configurationSHA != report.ConfigurationSHA256 {
		return d, errors.New("M18_CPU_PREPARATION_CONFIGURATION_REQUIRED")
	}
	d = modelpreparations.UploadDescriptor{SchemaVersion: 1, Scope: lifecyclewire.Scope{TenantID: tenant, WorkspaceID: workspace, PreparationID: refs.Preparation}, UploadID: refs.PreparationArtifact, AttemptID: attempt, ManifestSHA256: digest(raw), Files: report.ModelFiles}
	if e = d.Validate(lifecyclewire.MaximumModelBytes, lifecyclewire.MaximumModelFiles); e != nil {
		return d, e
	}
	store, e := modelpreparations.OpenStore(f.runtime.ModelPreparation.ArtifactRoot, tenant, f.runtime.Policy().ModelLifecycle.MinimumFreeBytes, lifecyclewire.MaximumModelBytes, lifecyclewire.MaximumModelFiles)
	if e != nil {
		return d, e
	}
	defer store.Close()
	path := filepath.Join("artifacts", refs.Preparation, attempt, d.UploadID)
	published := filepath.Join(f.runtime.ModelPreparation.ArtifactRoot, path)
	for _, asset := range d.Files {
		if e = fixtureCopy(ctx, filepath.Join(in.PreparationRoot, "model", asset.Path), filepath.Join(published, asset.Path), asset); e != nil {
			return d, e
		}
	}
	if e = fixtureWrite(filepath.Join(published, "result.json"), raw); e != nil {
		return d, e
	}
	if e = fixtureJSON(filepath.Join(published, "publication.json"), map[string]any{"schema_version": 1, "store_id": store.Namespace(), "descriptor": d}); e != nil {
		return d, e
	}
	if e = store.VerifyPublished(ctx, d); e != nil {
		return d, e
	}
	descriptor, e := json.Marshal(d)
	if e != nil {
		return d, e
	}
	input, e := canonical(map[string]any{"scope": "synthetic restoration fixture; not newly executed training/evaluation", "base_model_id": refs.BaseModel, "training_job_id": refs.TrainingJob, "training_attempt_id": refs.TrainingAttempt, "training_artifact_id": refs.Adapter, "heldout_id": refs.Heldout, "original_cpu_report_sha256": in.PreparationResultSHA256})
	if e != nil {
		return d, e
	}
	queries := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO model_preparations(id,tenant_id,workspace_id,node_id,current_node_id,model_id,training_job_id,training_attempt_id,training_artifact_id,heldout_id,gpu_uuid,input_snapshot,input_sha256,configuration,configuration_sha256,execution_recipe,created_by,requested_by,creation_audience,requested_audience,requested_epoch,reason,creation_key,creation_fingerprint,state) VALUES($1,$2,$3,$4,$4,$5,$6,$7,$8,$9,$10,$11::jsonb,$12,$13::jsonb,$14,'{}'::jsonb,$15,$15,'admin','admin',1,'Synthetic retained CPU reference graph; no physical target or review qualification',$16,$17,'failed')`, []any{refs.Preparation, tenant, workspace, refs.Node, refs.BaseModel, refs.TrainingJob, refs.TrainingAttempt, refs.Adapter, refs.Heldout, "GPU-" + uuid.NewString(), string(input), digest(input), string(configuration), configurationSHA, user, uuid.NewString(), make([]byte, 32)}},
		{`INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,deadline_at,completed_at) VALUES($1,$2,$3,$4,'model.prepare.v1','failed',$5,$6,clock_timestamp()+interval '1 minute',clock_timestamp())`, []any{task, tenant, workspace, refs.Node, uuid.NewString(), user}},
		{`INSERT INTO model_preparation_attempts(id,tenant_id,workspace_id,preparation_id,node_id,task_id,attempt_number,state,requested_by,requested_audience,requested_epoch,command_sha256,finished_at) VALUES($1,$2,$3,$4,$5,$6,1,'failed',$7,'admin',1,$8,clock_timestamp())`, []any{attempt, tenant, workspace, refs.Preparation, refs.Node, task, user, digest(input)}},
		{`UPDATE model_preparations SET current_attempt_id=$1,attempt_number=1 WHERE id=$2`, []any{attempt, refs.Preparation}},
		{`INSERT INTO model_preparation_uploads(id,tenant_id,workspace_id,preparation_id,attempt_id,manifest_sha256,manifest,manifest_bytes,descriptor,state,accepted_at) VALUES($1,$2,$3,$4,$5,$6,$7::jsonb,$8,$9::jsonb,'accepted',clock_timestamp())`, []any{d.UploadID, tenant, workspace, refs.Preparation, attempt, d.ManifestSHA256, string(raw), raw, string(descriptor)}},
		{`INSERT INTO model_preparation_artifacts(id,tenant_id,workspace_id,preparation_id,attempt_id,upload_id,manifest_sha256,descriptor,report,report_bytes,storage_path) VALUES($1,$2,$3,$4,$5,$1,$6,$7::jsonb,$8::jsonb,$9,$10)`, []any{d.UploadID, tenant, workspace, refs.Preparation, attempt, d.ManifestSHA256, string(descriptor), string(raw), raw, path}},
	}
	for _, q := range queries {
		if _, e = f.pool.Exec(ctx, q.sql, q.args...); e != nil {
			return d, e
		}
	}
	return d, nil
}

func populateDerivedModel(ctx context.Context, f actualPostgresBackupFixture, d modelpreparations.UploadDescriptor, tenant, workspace, user string, refs *populatedReferences) error {
	refs.DerivedModel = d.UploadID
	c := lifecyclewire.DerivedCatalog{SchemaVersion: 1, Kind: "merged_lora", Scope: d.Scope, ModelID: d.UploadID, BaseModelID: refs.BaseModel, BaseCatalogSHA256: lifecyclewire.ApprovedModelCatalogSHA256, AttemptID: d.AttemptID, ArtifactID: d.UploadID, ReportSHA256: d.ManifestSHA256, RecipeSHA256: lifecyclewire.RecipeDigest(), BaseRepository: lifecyclewire.ApprovedBaseRepository, BaseRevision: lifecyclewire.ApprovedBaseRevision, License: lifecyclewire.ApprovedBaseLicense, Format: "safetensors", Architecture: lifecyclewire.ApprovedBaseArchitecture, ModelType: lifecyclewire.ApprovedBaseModelType, Precision: "BF16", MaximumContextTokens: lifecyclewire.MaximumContextTokens, Files: d.Files}
	if e := c.Validate(); e != nil {
		return e
	}
	files := map[string]*os.File{}
	defer func() {
		for _, file := range files {
			file.Close()
		}
	}()
	for _, asset := range d.Files {
		file, e := os.Open(filepath.Join(f.runtime.ModelPreparation.ArtifactRoot, "artifacts", refs.Preparation, d.AttemptID, d.UploadID, asset.Path))
		if e != nil {
			return e
		}
		files[asset.Path] = file
	}
	owner := &models.Service{DB: f.pool, RootDirectory: f.runtime.Models.ArtifactRoot}
	if _, e := owner.PrepareDerivedFiles(ctx, c, files, f.runtime.Policy().ModelLifecycle.MinimumFreeBytes); e != nil {
		return e
	}
	if e := owner.VerifyDerivedFiles(ctx, c); e != nil {
		return e
	}
	raw, e := c.CanonicalBytes()
	if e != nil {
		return e
	}
	sha, e := c.Digest()
	if e != nil {
		return e
	}
	review := uuid.NewString()
	tx, e := f.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	queries := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by,kind) VALUES($1,$2,$3,'Synthetic restored derivative',$7,$4,$5::jsonb,'ready',$6,'merged_lora')`, []any{c.ModelID, tenant, workspace, sha, string(raw), user, agentwire.ApprovedModel().ModelKey}},
		{`INSERT INTO model_preparation_reviews(id,tenant_id,workspace_id,preparation_id,attempt_id,artifact_id,report_sha256,expected_revision,decision,model_id,model_name,reviewed_by,audience,reason,idempotency_key,fingerprint) VALUES($1,$2,$3,$4,$5,$6,$7,1,'approve',$6,'Synthetic restored derivative',$8,'admin','Synthetic fixture graph only; no newly executed quality gate or human approval',$9,$10)`, []any{review, tenant, workspace, refs.Preparation, d.AttemptID, d.UploadID, d.ManifestSHA256, user, uuid.NewString(), make([]byte, 32)}},
		{`INSERT INTO model_derivations(model_id,tenant_id,workspace_id,preparation_id,attempt_id,artifact_id,base_model_id,review_id,catalog_digest,catalog,report_sha256) VALUES($1,$2,$3,$4,$5,$1,$6,$7,$8,$9::jsonb,$10)`, []any{c.ModelID, tenant, workspace, refs.Preparation, d.AttemptID, refs.BaseModel, review, sha, string(raw), d.ManifestSHA256}},
		{`UPDATE model_preparations SET state='approved',review_id=$1,derived_model_id=$2 WHERE id=$3`, []any{review, c.ModelID, refs.Preparation}},
	}
	for _, q := range queries {
		if _, e = tx.Exec(ctx, q.sql, q.args...); e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}

type populatedSQLReadback struct {
	Table  string `json:"table"`
	Rows   int64  `json:"rows"`
	SHA256 string `json:"sha256"`
}

func populatedDomainRows(ctx context.Context, pool *pgxpool.Pool) ([]populatedSQLReadback, error) {
	var out []populatedSQLReadback
	// Fixed contract identifiers only; no caller-controlled SQL interpolation.
	for _, table := range []string{"model_versions", "model_derivations", "datasets", "dataset_versions", "training_jobs", "training_checkpoints", "training_artifacts", "model_holdouts", "model_preparations", "model_preparation_artifacts", "model_preparation_reviews"} {
		p := populatedSQLReadback{Table: table}
		q := `SELECT count(*),encode(sha256(convert_to(COALESCE(jsonb_agg(row_data ORDER BY row_data::text)::text,'[]'),'UTF8')),'hex') FROM (SELECT to_jsonb(t) AS row_data FROM ` + table + ` t) selected`
		if e := pool.QueryRow(ctx, q).Scan(&p.Rows, &p.SHA256); e != nil {
			return nil, e
		}
		if p.Rows < 1 || !digestPattern.MatchString(p.SHA256) {
			return nil, errors.New("M18_NONEMPTY_DOMAIN_ROWS_REQUIRED")
		}
		out = append(out, p)
	}
	return out, nil
}

func assertPopulatedTargetReferences(ctx context.Context, f actualPostgresBackupFixture, complete RestoreCompletion, refs populatedReferences) error {
	var request release.PostgreSQLRestoreRequest
	if _, e := readPrivateDocument("/fixture/request-restore.json", MaximumRequestBytes, &request); e != nil {
		return e
	}
	runtime, e := config.LoadProtected(request.RuntimeConfigurationFile)
	if e != nil {
		return e
	}
	raw, e := readPrivateBytes(runtime.Database.DSNFile, 16<<10)
	if e != nil {
		return e
	}
	pc, e := pgxpool.ParseConfig(strings.TrimSpace(string(raw)))
	if e != nil {
		return e
	}
	if pc.ConnConfig.Database != request.TargetDatabase || request.TargetScopeID != complete.RuntimeContext.TargetScopeID {
		return errors.New("M18_EXACT_RESTORED_REFERENCE_SCOPE_REQUIRED")
	}
	pc.MaxConns = 2
	pc.MinConns = 0
	pool, e := pgxpool.NewWithConfig(ctx, pc)
	if e != nil {
		return e
	}
	defer pool.Close()
	if e = assertPopulatedReferences(ctx, pool, refs); e != nil {
		return e
	}
	rows, e := populatedDomainRows(ctx, pool)
	if e != nil {
		return e
	}
	expected, _ := json.Marshal(refs.Rows)
	actual, _ := json.Marshal(rows)
	if !bytes.Equal(expected, actual) {
		return errors.New("M18_RESTORED_DOMAIN_ROWS_CHANGED")
	}
	var tenant string
	if e = pool.QueryRow(ctx, `SELECT tenant_id::text FROM installation_state WHERE id=1`).Scan(&tenant); e != nil {
		return e
	}
	owners, e := openOwners(ctx, runtime, &identity.Service{DB: pool, Config: runtime}, tenant)
	if e != nil {
		return e
	}
	if e = owners.Verify(ctx); e != nil {
		return errors.Join(e, owners.Close())
	}
	if e = owners.Close(); e != nil {
		return e
	}
	// Persist only protected typed non-secret facts, never the DSN.
	root, e := openPrivateRoot("/fixture")
	if e != nil {
		return e
	}
	defer root.Close()
	projection, e := yaml.Marshal(map[string]any{"target_scope_id": request.TargetScopeID, "target_database": request.TargetDatabase, "domain_rows": rows, "four_existing_owner_verifiers": "passed under restored restricted runtime role"})
	if e != nil {
		return e
	}
	return writeNew(root, "actual-populated-target-references.yaml", projection)
}
