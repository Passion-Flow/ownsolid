//go:build repository_integration && training_artifact_integration && training_experiment_integration

package training_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	agent "github.com/Passion-Flow/ownsolid/api/agent/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
)

// Only the retained native result and adapter files are real worker output.
// The selected repository exercises authenticated control admission with an
// explicitly synthetic production command, node observation and stop.
func TestActualTrainingExecutionEvidenceAdmission(t *testing.T) {
	if os.Getenv("PROJECT_ONE_REPOSITORY_ENGINE") != "postgresql" {
		t.Skip("this test injects faults in an owned PostgreSQL fixture schema")
	}
	output := os.Getenv("PROJECT_ONE_ACTUAL_TRAINING_OUTPUT")
	model := os.Getenv("PROJECT_ONE_APPROVED_MODEL_DIRECTORY")
	if !filepath.IsAbs(output) || !filepath.IsAbs(model) {
		t.Fatal("absolute retained CPU output and approved base directories required")
	}
	read := func(path string) []byte {
		t.Helper()
		raw, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		return raw
	}
	native, e := wire.DecodeRequest(read(filepath.Join(filepath.Dir(output), "request.json")))
	if e != nil || native.Configuration.Device != wire.VerificationProfile {
		t.Fatal("original CPU request required", e)
	}
	raw := read(filepath.Join(output, "result.json"))
	if result, e := wire.DecodeResult(raw, native, agent.ApprovedModel().Repository); e != nil || result.Status != "trained" {
		t.Fatal("original protected CPU result required", e)
	}
	g := trainingRuntimeSetup(t, &native.Configuration, true)
	prepareRetainedTrainingBase(t, g, model)
	command := *g.lease.Training
	request, e := command.ContainerRequest()
	if e != nil {
		t.Fatal(e)
	}
	events := append(prelude(command), eventFixture(command, 3, request.Configuration.MaximumSteps, "metric", map[string]any{"values": map[string]any{"loss": 1.0}}))
	if _, e = g.s.RecordEvents(g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, events); e != nil {
		t.Fatal(e)
	}
	var envelope map[string]json.RawMessage
	if e = json.Unmarshal(raw, &envelope); e != nil {
		t.Fatal(e)
	}
	bindings := wire.ExpectedBindings(request, agent.ApprovedModel().Repository)
	for key, value := range map[string]any{
		"bindings": bindings, "job_id": request.JobID, "attempt_id": request.AttemptID,
		"dataset_version_id": request.DatasetVersionID, "dataset_manifest_sha256": request.DatasetManifestSHA256,
		"memory_enforcement": "linux_cgroup_memory_cpu_fsize_wall", "device_profile": wire.ProductionProfile,
		"train_rows": 1, "validation_rows": 1, "event_count": len(events),
	} {
		envelope[key] = acceptedAdapterRaw(t, value)
	}
	begin := func(raw []byte) (training.Upload, error) {
		return g.s.BeginUpload(g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, "artifact", raw)
	}
	differentImage := strings.TrimPrefix(command.Recipe.ImageReference, "projectone/training@")
	if differentImage == *command.Recipe.Identity.ImageID {
		t.Fatal("the fixture must distinguish the distribution pin from the image ID")
	}
	forgedBindings := bindings
	forgedBindings.ExecutionIdentity.ImageID = &differentImage
	for _, variant := range []struct {
		key   string
		value any
	}{
		{"bindings", forgedBindings},
		{"attempt_id", uuid.NewString()},
		{"device_profile", wire.VerificationProfile},
		{"dependency_lock_sha256", strings.Repeat("a", 64)},
		{"hardware_observation", agent.NVIDIAGPU{UUID: command.GPUUUID}},
	} {
		original, present := envelope[variant.key]
		envelope[variant.key] = acceptedAdapterRaw(t, variant.value)
		u, e := begin(acceptedAdapterRaw(t, envelope))
		if e == nil || e.Error() != "TRAINING_RESULT_INVALID" || !reflect.DeepEqual(u, training.Upload{}) {
			t.Fatalf("unbound %s created an upload: %v", variant.key, e)
		}
		if present {
			envelope[variant.key] = original
		} else {
			delete(envelope, variant.key)
		}
	}
	if got := g.count(t, "SELECT count(*) FROM training_uploads WHERE attempt_id=$1", "", command.AttemptID); got != 0 {
		t.Fatal("refused evidence survived as an upload")
	}

	raw = acceptedAdapterRaw(t, envelope)
	upload, e := begin(raw)
	if e != nil {
		t.Fatal("valid bound upload", e)
	}
	files := make(map[string][]byte)
	for _, asset := range upload.Files {
		files[asset.Path] = read(filepath.Join(output, "adapter", asset.Path))
	}
	g.upload(t, upload, files)
	if accepted, e := g.finish(upload.ID); e != nil || accepted.ReferenceID != upload.ID {
		t.Fatal("retained adapter acceptance", e)
	}

	receipt := g.stopReceipt(t)
	receipt.Proof = "owned_container_removed"
	containerID := strings.Repeat("d", 64)
	receipt.ContainerID = &containerID
	digest := acceptedAdapterDigest(raw)
	receipt.OutcomeSHA256 = &digest
	stop := func(r wire.StopReceipt) (wire.StopAcceptance, error) {
		return g.s.RecordStop(g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, r, nil)
	}
	refuseStop := func(r wire.StopReceipt, code string) {
		t.Helper()
		got, e := stop(r)
		if e == nil || e.Error() != code || got != (wire.StopAcceptance{}) {
			t.Fatalf("expected %s, got %v", code, e)
		}
		g.requireDeliveredOwner(t)
	}
	wrongImage := receipt
	wrongImage.ImageID = differentImage
	refuseStop(wrongImage, "TRAINING_STOP_INVALID")

	// Only the disposable fixture may bypass immutable-row triggers to model
	// a restored result inconsistent with its original byte receipt.
	corrupt := func(table, trigger, update string, raw []byte, id string) {
		t.Helper()
		e := g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
			for _, action := range []transaction.Statement{
				transaction.SQL(transaction.PostgreSQL, "ALTER TABLE "+table+" DISABLE TRIGGER "+trigger),
				transaction.SQL(transaction.PostgreSQL, update, raw, id),
				transaction.SQL(transaction.PostgreSQL, "ALTER TABLE "+table+" ENABLE TRIGGER "+trigger),
			} {
				if _, e := q.Exec(ctx, action); e != nil {
					return e
				}
			}
			return nil
		})
		if e != nil {
			t.Fatal("inject owned repository fault", e)
		}
	}
	changeOutcome := func(raw []byte) {
		corrupt("training_attempts", "training_attempt_inputs_immutable", "UPDATE training_attempts SET outcome=$1::jsonb WHERE id=$2", raw, command.AttemptID)
	}
	changeUpload := func(raw []byte) {
		corrupt("training_uploads", "training_upload_inputs_immutable", "UPDATE training_uploads SET manifest_bytes=$1 WHERE id=$2", raw, upload.ID)
	}
	envelope["bindings"] = acceptedAdapterRaw(t, forgedBindings)
	forgedOutcome := acceptedAdapterRaw(t, envelope)
	changeOutcome(forgedOutcome)
	refuseStop(receipt, "TRAINING_RESULT_CONFLICT")
	changeOutcome(raw)
	changeUpload(append(bytes.Clone(raw), '\n'))
	refuseStop(receipt, "TRAINING_RESULT_CONFLICT")
	changeUpload(raw)

	if accepted, e := stop(receipt); e != nil || accepted.State != "succeeded" || !accepted.GPUReleased {
		t.Fatal("repaired exact result did not complete", e)
	}
	if replayed, e := stop(receipt); e != nil || !replayed.Duplicate {
		t.Fatal("exact stop replay", e)
	}
	readExperiment := func() (training.AcceptedExperiment, error) {
		return g.s.ReadAcceptedExperiment(g.ctx, g.a, g.f.Workspace, g.job.ID, uuid.NewString())
	}
	experiment, e := readExperiment()
	if e != nil || experiment.Execution.ImageID != receipt.ImageID ||
		experiment.Execution.ImageReference != command.Recipe.ImageReference ||
		experiment.Execution.WorkerSourceSHA256 != command.Recipe.Identity.WorkerSourceSHA256 ||
		experiment.Execution.DependencyLockSHA256 != nil || experiment.Execution.HardwareObservation != nil {
		t.Fatal("accepted experiment did not distinguish bound and unavailable evidence", e)
	}
	changeOutcome(forgedOutcome)
	if experiment, e := readExperiment(); e == nil || e.Error() != "TRAINING_DATA_INVALID" || !reflect.DeepEqual(experiment, training.AcceptedExperiment{}) {
		t.Fatal("inconsistent retained evidence escaped", e)
	}
	changeOutcome(raw)
}
