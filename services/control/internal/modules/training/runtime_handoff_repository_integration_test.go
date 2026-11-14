//go:build repository_integration && training_artifact_integration

package training_test

import (
	"bytes"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

// This path uses the real control loop, SQL/file callbacks and unchanged retained
// CPU tensors. Hardware, production report bindings and the stop proof remain
// explicitly synthetic; no container, GPU or new model computation is claimed.
func TestActualPairedTrainingRuntimeAcceptedHandoff(t *testing.T) {
	t.Run("queued_worker_positive_stop_and_verified_adapter_source", func(t *testing.T) {
		trainingRuntimeAcceptedHandoff(t, nil)
	})
}

// The shared body preserves every original SQL/file/stop/uncertainty assertion.
func trainingRuntimeAcceptedHandoff(t *testing.T, inspect func(*testing.T, trainingRuntimeStory)) {
	t.Helper()
	var join func()
	g, checkpoint, checkpointFiles, checkpointReceipt := trainingRuntimeCheckpointUsing(t, func(t *testing.T, cfg *wire.Configuration, _ bool) trainingRuntimeStory {
		g := trainingRuntimeQueue(t, cfg)
		join = trainingRuntimeStart(t, &g)
		g.claim(t)
		return g
	})
	if inspect != nil {
		inspect(t, g)
	}
	renewed, handled, e := g.s.RenewTraining(g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken)
	if e != nil || !handled || renewed.ValidForMilliseconds <= 0 {
		t.Fatal("runtime accepted handoff heartbeat", e)
	}
	g.upload(t, checkpoint, checkpointFiles)
	if _, e = g.finish(checkpoint.ID); e != nil {
		t.Fatal("runtime accepted checkpoint", e)
	}
	artifact, files, outcome := g.artifact(t, checkpointReceipt)
	g.upload(t, artifact, files)
	if _, e = g.finish(artifact.ID); e != nil {
		t.Fatal("runtime accepted adapter publication", e)
	}
	before, e := g.s.AcceptedAdapterSnapshot(g.ctx, g.a, g.f.Workspace, g.job.ID)
	acceptedAdapterWant(t, before, e, "TRAINING_RESULT_UNAVAILABLE")
	join()
	g.requireDeliveredOwner(t)

	stop := g.stopReceipt(t)
	stop.Proof = "owned_container_removed"
	container := strings.Repeat("d", 64)
	stop.ContainerID = &container
	sha := acceptedAdapterDigest(outcome)
	stop.OutcomeSHA256 = &sha
	accepted, e := g.s.RecordStop(g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, stop, outcome)
	if e != nil || accepted.State != "succeeded" || !accepted.GPUReleased {
		t.Fatal("runtime accepted exact stopped result", e)
	}
	if g.count(t, "SELECT count(*) FROM gpu_leases WHERE training_job_id=$1 AND state='held'", "SELECT COUNT(*) FROM gpu_leases WHERE training_job_id=? AND state='held'", g.job.ID) != 0 {
		t.Fatal("accepted exact stop retained held GPU")
	}
	source, e := g.s.AcceptedAdapterSnapshot(g.ctx, g.a, g.f.Workspace, g.job.ID)
	if e != nil || source.Job.ID != g.job.ID || source.Job.State != "succeeded" || !reflect.DeepEqual(source.Command, *g.lease.Training) || source.Descriptor.UploadID != artifact.ID || source.Descriptor.ManifestSHA256 != sha || !reflect.DeepEqual(source.Descriptor.Files, artifact.Files) {
		t.Fatal("runtime immutable preparation input", e)
	}
	if e = g.s.VerifyAcceptedAdapter(g.ctx, source); e != nil {
		t.Fatal("runtime retained adapter verification", e)
	}
	for _, asset := range source.Descriptor.Files {
		file, metadata, e := g.s.OpenAcceptedAdapterAsset(g.ctx, source, asset.Path)
		if e != nil {
			t.Fatal("runtime retained adapter asset open", e)
		}
		raw, readErr := io.ReadAll(file)
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || metadata != asset || !bytes.Equal(raw, files[asset.Path]) || acceptedAdapterDigest(raw) != asset.SHA256 {
			t.Fatal("runtime retained adapter bytes changed", readErr, closeErr)
		}
	}
	store := &trainingCreatorStore{Store: g.f.Store, at: 1, unknown: true}
	x := g.wrapped(store)
	denied, e := x.s.AcceptedAdapterSnapshot(x.ctx, x.a, x.f.Workspace, x.job.ID)
	acceptedAdapterWant(t, denied, e, string(transaction.CommitUnknown))
	if store.calls != 1 {
		t.Fatal("runtime source snapshot retried uncertain transaction")
	}
	x = g.wrapped(g.f.Store)
	readback, e := x.s.AcceptedAdapterSnapshot(x.ctx, x.a, x.f.Workspace, x.job.ID)
	if e != nil || !reflect.DeepEqual(readback, source) {
		t.Fatal("runtime explicit source reconciliation changed identity", e)
	}
}
