package training_test

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func cloneLineageVersion(t *testing.T, f scenario, original datasets.Version) datasets.Version {
	t.Helper()
	ctx := t.Context()
	ds := f.s.Datasets
	workspace, dataset := f.input.WorkspaceID, f.input.DatasetID
	draft, err := ds.CloneVersion(ctx, f.portal, workspace, dataset, original.ID, "Create a changed successor version", uuid.NewString(), uuid.NewString())
	if err != nil {
		t.Fatal("clone training input", err)
	}
	if worked, err := ds.ProcessNext(ctx); err != nil || !worked {
		t.Fatal("inspect cloned input", worked, err)
	}
	draft, err = ds.GetDraft(ctx, f.portal, workspace, dataset, draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ds.AppendRow(ctx, f.portal, workspace, dataset, draft.ID, datasets.RowRequest{
		ExpectedRevision: draft.Revision, SourceID: uuid.NewString(), Prompt: "New version only",
		Completion: "Distinct successor content", Reason: "Change the source before publishing",
	}, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	draft, err = ds.GetDraft(ctx, f.portal, workspace, dataset, draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	job, err := ds.Publish(ctx, f.portal, workspace, dataset, draft.ID, datasets.PublicationRequest{
		ExpectedRevision: draft.Revision, Seed: "00112233445566778899aabbccddeeff", ValidationBasisPoints: 2000,
		Reason: "Publish changed successor without replacing the original",
	}, uuid.NewString(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if worked, err := ds.ProcessNext(ctx); err != nil || !worked {
		t.Fatal("publish cloned input", worked, err)
	}
	job, err = ds.GetJob(ctx, f.portal, workspace, dataset, job.ID)
	if err != nil || job.State != "succeeded" || job.VersionID == nil {
		t.Fatalf("successor publication: %+v, %v", job, err)
	}
	version, err := ds.GetVersion(ctx, f.portal, workspace, dataset, *job.VersionID)
	if err != nil || version.ParentVersionID == nil || *version.ParentVersionID != original.ID ||
		version.ManifestSHA256 == original.ManifestSHA256 || version.EffectiveSourceSHA256 == original.EffectiveSourceSHA256 ||
		version.Manifest.TrainRows+version.Manifest.ValidationRows != original.Manifest.TrainRows+original.Manifest.ValidationRows+1 {
		t.Fatalf("successor must retain its parent and have changed content: %+v, %v", version, err)
	}
	return version
}

func assertLineagePartition(t *testing.T, f scenario, l agentwire.Lease, asset datawire.Asset) {
	t.Helper()
	ctx := t.Context()
	file, selected, err := f.s.Datasets.VersionFile(ctx, f.portal, f.input.WorkspaceID, f.input.DatasetID, f.input.DatasetVersionID, asset.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	part, err := f.s.InputFile(ctx, f.input.NodeID, f.credential, l.TaskID, wire.InputFileRequest{
		LeaseToken: l.LeaseToken, Kind: "dataset", Asset: asset.Path, Length: asset.SizeBytes,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer part.File.Close()
	original, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := io.ReadAll(io.NewSectionReader(part.File, part.Offset, part.Size))
	if err != nil || selected != asset || part.SHA256 != asset.SHA256 ||
		digest(retry) != asset.SHA256 || !bytes.Equal(original, retry) {
		t.Fatalf("retry served different %s bytes: %v", asset.Path, err)
	}
}

func TestActualTrainingDatasetLineage(t *testing.T) {
	ctx := t.Context()
	run, base, shape := newPublicationRun(t, 0)
	f := run.f
	s := f.s
	workspace, dataset := f.input.WorkspaceID, f.input.DatasetID
	original, err := s.Datasets.GetVersion(ctx, f.portal, workspace, dataset, f.input.DatasetVersionID)
	if err != nil {
		t.Fatal(err)
	}
	key := uuid.NewString()
	job, err := s.Create(ctx, f.portal, f.input, key, uuid.NewString())
	if err != nil || job.DatasetID != dataset || job.DatasetVersionID != original.ID || job.DatasetManifestSHA256 != original.ManifestSHA256 {
		t.Fatalf("job did not freeze its original source: %+v, %v", job, err)
	}
	hardware(t, f)
	if worked, err := s.DispatchNext(ctx); err != nil || !worked {
		t.Fatal("dispatch first attempt", worked, err)
	}
	first := run.claim(base, 0)
	acknowledgePublicationLease(run, base, first)
	if first.Training.Scope.JobID != job.ID || first.Training.DatasetID != dataset ||
		first.Training.DatasetVersionID != original.ID || first.Training.DatasetManifestSHA256 != original.ManifestSHA256 {
		t.Fatal("first attempt did not use the source frozen by job creation")
	}

	next := cloneLineageVersion(t, f, original)
	retired, err := s.Datasets.RetireVersion(ctx, f.portal, workspace, dataset, original.ID, "Retire the original from new training", uuid.NewString())
	if err != nil || retired.RetiredAt == nil {
		t.Fatal("retire original version", err)
	}
	if replay, err := s.Create(ctx, f.portal, f.input, key, uuid.NewString()); err != nil || replay.ID != job.ID ||
		replay.DatasetVersionID != original.ID || replay.DatasetManifestSHA256 != original.ManifestSHA256 {
		t.Fatalf("creation replay lost the original source: %+v, %v", replay, err)
	}
	_, err = s.Create(ctx, f.portal, f.input, uuid.NewString(), uuid.NewString())
	expected(t, err, "DATASET_RETIRED")
	replacement := f.input
	replacement.DatasetVersionID = next.ID
	_, err = s.Create(ctx, f.portal, replacement, key, uuid.NewString())
	expected(t, err, "IDEMPOTENCY_CONFLICT")

	other, err := s.Workspaces.Create(ctx, f.admin, "Foreign training dataset", f.portal.Me.User.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	replacement.DatasetID, replacement.DatasetVersionID = publishFixtureDataset(t, s.Datasets, f.portal, other.ID)
	_, err = s.Create(ctx, f.portal, replacement, uuid.NewString(), uuid.NewString())
	expected(t, err, "DATASET_NOT_FOUND")

	failure := stopFixture(t, f, first)
	failure.AgentErrorCode = "TRAINING_PROCESSOR_MEMORY_EXCEEDED"
	stopped, err := s.RecordStop(ctx, f.input.NodeID, f.credential, first.TaskID, first.LeaseToken, failure, nil)
	if err != nil || stopped.State != "failed" || !stopped.GPUReleased {
		t.Fatalf("first attempt did not release on failure: %+v, %v", stopped, err)
	}
	failed, err := s.Get(ctx, f.portal, workspace, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	recovery := training.RecoveryRequest{ExpectedRevision: failed.Revision, Reason: "Retry against the frozen training input", NodeID: f.input.NodeID, Mode: "restart"}
	recoveryKey := uuid.NewString()
	for range 2 {
		recovered, err := s.Recover(ctx, f.portal, workspace, job.ID, recovery, recoveryKey, uuid.NewString())
		if err != nil || recovered.State != "queued" || recovered.DatasetVersionID != original.ID ||
			recovered.DatasetManifestSHA256 != original.ManifestSHA256 {
			t.Fatalf("recovery lost the retired source: %+v, %v", recovered, err)
		}
	}
	if count(t, f, "SELECT count(*) FROM training_recoveries WHERE job_id=$1", job.ID) != 1 {
		t.Fatal("recovery replay created multiple intents")
	}
	if worked, err := s.DispatchNext(ctx); err != nil || !worked {
		t.Fatal("dispatch recovered attempt", worked, err)
	}
	l := run.claim(base, first.Sequence)
	if l.Training == nil || l.Training.AttemptNumber != 2 || l.Training.AttemptID == first.Training.AttemptID ||
		l.Training.Scope != first.Training.Scope || l.Training.DatasetID != dataset || l.Training.DatasetVersionID != original.ID ||
		l.Training.DatasetManifestSHA256 != original.ManifestSHA256 || l.Training.Inputs.Train != first.Training.Inputs.Train ||
		l.Training.Inputs.Validation != first.Training.Inputs.Validation {
		t.Fatalf("retry changed the frozen training content: %+v", l.Training)
	}
	p := newPublicationFixtureForLease(run, base, shape, l)
	assertLineagePartition(t, f, l, l.Training.Inputs.Train)
	assertLineagePartition(t, f, l, l.Training.Inputs.Validation)
	p.finish(p.stage("checkpoint", p.checkpoint, p.checkpointFiles, ""), "checkpoint", false)
	metadata := p.artifact()
	artifact := p.stage("artifact", metadata, p.adapterFiles, "")
	p.finish(artifact, "artifact", false)
	receipt := stopFixture(t, f, l)
	container, result := digest([]byte("synthetic stopped container")), digest(metadata)
	receipt.Proof, receipt.ContainerID, receipt.OutcomeSHA256 = "owned_container_removed", &container, &result
	reply := p.client.Call("POST", p.base+p.task+"/training-stops",
		wire.StopSubmission{LeaseToken: l.LeaseToken, Receipt: receipt, Outcome: json.RawMessage("null")}, 200, "TrainingStopAcceptanceResponse")
	if reply["state"] != "succeeded" || reply["attempt_id"] != l.Training.AttemptID || reply["gpu_released"] != true {
		t.Fatalf("retry did not publish a successful adapter: %+v", reply)
	}

	exported, err := s.Export(ctx, f.portal, workspace, job.ID, "manifest.json", uuid.NewString())
	var provenance training.AdapterProvenance
	if err != nil || exported.Manifest == nil || json.Unmarshal(exported.Manifest.Bytes, &provenance) != nil ||
		provenance.DatasetID != dataset || provenance.DatasetVersionID != original.ID ||
		provenance.DatasetManifestSHA256 != original.ManifestSHA256 || provenance.ArtifactID != artifact {
		t.Fatalf("export did not retain the original lineage: %+v, %v", provenance, err)
	}
	replay, err := s.Export(ctx, f.portal, workspace, job.ID, "manifest.json", uuid.NewString())
	if err != nil || replay.Manifest == nil || !bytes.Equal(replay.Manifest.Bytes, exported.Manifest.Bytes) {
		t.Fatal("export replay changed immutable provenance", err)
	}
	_, err = s.Export(ctx, f.portal, other.ID, job.ID, "manifest.json", uuid.NewString())
	expected(t, err, "TRAINING_NOT_FOUND")

	// Model preparation borrows this same scoped source reader under its actor and workspace fences.
	source, err := s.AcceptedAdapterSnapshot(ctx, f.portal, workspace, job.ID)
	if err != nil || source.Command.DatasetID != provenance.DatasetID || source.Command.DatasetVersionID != provenance.DatasetVersionID ||
		source.Command.DatasetManifestSHA256 != provenance.DatasetManifestSHA256 || source.Command.Inputs.Train != l.Training.Inputs.Train ||
		source.Command.Inputs.Validation != l.Training.Inputs.Validation || source.Descriptor.UploadID != artifact ||
		source.Descriptor.AttemptID != l.Training.AttemptID {
		t.Fatalf("model preparation source did not retain the original lineage: %+v, %v", source.Command, err)
	}
	if err := s.VerifyAcceptedAdapter(ctx, source); err != nil {
		t.Fatal(err)
	}
	file, selected, err := s.OpenAcceptedAdapterAsset(ctx, source, "adapter_config.json")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	raw, err := io.ReadAll(file)
	if err != nil || selected.SHA256 != digest(raw) || !bytes.Equal(raw, p.adapterFiles[selected.Path]) {
		t.Fatal("model preparation source did not read the retained adapter bytes", err)
	}
	_, err = s.AcceptedAdapterSnapshot(ctx, f.portal, other.ID, job.ID)
	expected(t, err, "TRAINING_NOT_FOUND")

	t.Run("inconsistent retained lineage", func(t *testing.T) {
		// Simulate a restored row that disagrees with the independently retained accepted task.
		err := pgx.BeginFunc(t.Context(), s.DB, func(tx pgx.Tx) error {
			if _, err := tx.Exec(t.Context(), "ALTER TABLE training_jobs DISABLE TRIGGER training_job_inputs_immutable"); err != nil {
				return err
			}
			if _, err := tx.Exec(t.Context(), "UPDATE training_jobs SET dataset_version_id=$2,dataset_manifest_sha256=$3 WHERE id=$1", job.ID, next.ID, next.ManifestSHA256); err != nil {
				return err
			}
			_, err := tx.Exec(t.Context(), "ALTER TABLE training_jobs ENABLE TRIGGER training_job_inputs_immutable")
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.AcceptedAdapterSnapshot(t.Context(), f.portal, workspace, job.ID)
		expected(t, err, "TRAINING_DATA_INVALID")
		for _, asset := range []string{"manifest.json", "adapter_config.json"} {
			request := uuid.NewString()
			out, err := s.Export(t.Context(), f.portal, workspace, job.ID, asset, request)
			if out.File != nil {
				out.File.File.Close()
			}
			expected(t, err, "TRAINING_DATA_INVALID")
			if out.Manifest != nil || out.File != nil ||
				count(t, f, "SELECT count(*) FROM audit_events WHERE request_id=$1 AND action='training.adapter_exported'", request) != 0 {
				t.Fatal("inconsistent lineage exposed bytes or recorded a successful export")
			}
		}
	})
}
