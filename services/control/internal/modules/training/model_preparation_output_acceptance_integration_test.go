//go:build training_artifact_integration && model_lifecycle_admission_integration && model_lifecycle_output_acceptance_integration

package training_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

func copyPreparationBase(t *testing.T, g preparationScenario, model string) {
	t.Helper()
	base := os.Getenv("PROJECT_ONE_APPROVED_MODEL_DIRECTORY")
	for _, asset := range wire.ApprovedBaseFiles() {
		path := filepath.Join(g.f.s.Models.RootDirectory, "objects", model, asset.Path)
		if info, e := os.Stat(path); e == nil {
			if info.Size() != asset.SizeBytes {
				t.Fatal("existing fixture base size")
			}
			continue
		}
		source, e := os.Open(filepath.Join(base, asset.Path))
		if e != nil {
			t.Fatal(e)
		}
		target, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0444)
		if e != nil {
			source.Close()
			t.Fatal(e)
		}
		n, e := io.Copy(target, source)
		source.Close()
		closeError := target.Close()
		if e == nil && closeError == nil {
			closeError = os.Chmod(path, 0444)
		}
		if e != nil || closeError != nil || n != asset.SizeBytes {
			t.Fatal("actual isolated approved base copy", e, closeError)
		}
	}
}

// All source/candidate files are actual native producer bytes; only production/case/hardware envelopes
// are explicit test doubles. Successful artifact acceptance is not physical stop, review or CUDA qualification.
func TestActualCpuModelPreparationCompleteOutputIndependentAcceptanceAuditRetryAndNoGPURelease(t *testing.T) {
	g, p, l, metadata := preparationOutputFixtureBeforeClaim(t, wire.DefaultConfiguration().MinimumAccuracyBasisPoints, func(g preparationScenario, p modelpreparations.Preparation) {
		if count(t, g.f, "SELECT count(*) FROM node_tasks WHERE state='running'") != 0 {
			t.Fatal("fixture base copy must precede live agent claim")
		}
		copyPreparationBase(t, g, p.ModelID)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 270*time.Second)
	defer cancel()
	var group sync.WaitGroup
	errors := make(chan error, 1)
	group.Add(1)
	go func() {
		defer group.Done()
		tick := time.NewTicker(5 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if _, e := g.f.s.Nodes.Renew(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken); e != nil {
					if ctx.Err() == nil {
						select {
						case errors <- e:
						default:
						}
					}
					return
				}
			}
		}
	}()
	defer func() { cancel(); group.Wait() }()
	if _, e := g.f.s.Nodes.Renew(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken); e != nil {
		t.Fatal("initial actual lease renewal before large file IO", e)
	}
	u, e := g.service.BeginUpload(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, metadata)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = g.service.FinishUpload(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, u.ID); e == nil {
		t.Fatal("incomplete model accepted")
	}
	var total int64
	chunks := 0
	for _, asset := range u.Files {
		file, e := os.Open(filepath.Join(os.Getenv("PROJECT_ONE_ACTUAL_PREPARATION_OUTPUT"), "model", asset.Path))
		if e != nil {
			t.Fatal(e)
		}
		for offset := int64(0); offset < asset.SizeBytes; offset += wire.TransferChunkBytes {
			length := min(wire.TransferChunkBytes, asset.SizeBytes-offset)
			body, e := io.ReadAll(io.NewSectionReader(file, offset, length))
			if e != nil || int64(len(body)) != length {
				file.Close()
				t.Fatal(e)
			}
			accepted, e := g.service.UploadChunk(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, u.ID, wire.ChunkRequest{Asset: asset.Path, Offset: offset, Length: length, SHA256: digest(body)}, bytes.NewReader(body))
			if e != nil {
				file.Close()
				t.Fatal("actual native model chunk", asset.Path, offset, e)
			}
			total += length
			chunks++
			if accepted.ReceivedBytes != total {
				file.Close()
				t.Fatal("exact received byte accounting")
			}
		}
		file.Close()
	}
	if total != 1208042441 || chunks != 152 {
		t.Fatal("declared actual native output workload", total, chunks)
	}
	if _, e = g.f.s.DB.Exec(ctx, `CREATE FUNCTION reject_preparation_artifact_audit() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='model.preparation_artifact_accepted' THEN RAISE EXCEPTION 'SYNTHETIC_PREPARATION_ARTIFACT_AUDIT_FAILURE';END IF;RETURN NEW;END;$$;CREATE TRIGGER reject_preparation_artifact_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_preparation_artifact_audit()`); e != nil {
		t.Fatal(e)
	}
	if _, e = g.service.FinishUpload(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, u.ID); e == nil {
		t.Fatal("artifact audit failure accepted")
	}
	if count(t, g.f, "SELECT count(*) FROM model_preparation_artifacts") != 0 || count(t, g.f, "SELECT count(*) FROM model_preparation_attempts WHERE id=$1 AND outcome IS NULL AND outcome_bytes IS NULL", *p.CurrentAttemptID) != 1 {
		t.Fatal("partial output/outcome survived audit rollback")
	}
	if _, e = g.f.s.DB.Exec(ctx, "DROP TRIGGER reject_preparation_artifact_audit ON audit_events"); e != nil {
		t.Fatal(e)
	}
	accepted, e := g.service.FinishUpload(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, u.ID)
	if e != nil || accepted.Duplicate || accepted.UploadID != u.ID || accepted.ManifestSHA256 != digest(metadata) {
		t.Fatal("actual independent model/report acceptance", e)
	}
	again, e := g.service.FinishUpload(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, u.ID)
	if e != nil || !again.Duplicate || again.UploadID != u.ID {
		t.Fatal("verified immutable output replay", e)
	}
	if count(t, g.f, "SELECT count(*) FROM model_preparation_artifacts WHERE preparation_id=$1", p.ID) != 1 || count(t, g.f, "SELECT count(*) FROM audit_events WHERE action='model.preparation_artifact_accepted'") != 1 {
		t.Fatal("duplicate accepted artifact/audit")
	}
	if count(t, g.f, "SELECT count(*) FROM gpu_leases WHERE model_preparation_id=$1 AND state='held'", p.ID) != 1 || count(t, g.f, "SELECT count(*) FROM model_preparation_attempts WHERE id=$1 AND physical_stopped_at IS NULL", *p.CurrentAttemptID) != 1 || count(t, g.f, "SELECT count(*) FROM model_preparations WHERE id=$1 AND state='running'", p.ID) != 1 {
		t.Fatal("file acceptance falsely stopped/released/reviewed preparation")
	}
	if count(t, g.f, "SELECT count(*) FROM model_versions") != 1 {
		t.Fatal("file acceptance published a derived model without review")
	}
	if _, e = g.f.s.DB.Exec(ctx, "UPDATE model_preparation_attempts SET outcome_bytes=$2 WHERE id=$1", *p.CurrentAttemptID, []byte(`{"tampered":true}`)); e == nil {
		t.Fatal("accepted original outcome bytes mutable")
	}
	select {
	case e := <-errors:
		t.Fatal("real live lease renewal", e)
	default:
	}
	receipt := preparationStopReceipt(t, g, l)
	receipt.AgentErrorCode = ""
	receipt.Proof = "owned_container_removed"
	container := strings.Repeat("d", 64)
	receipt.ContainerID = &container
	sha := digest(metadata)
	receipt.OutcomeSHA256 = &sha
	stopped, e := g.service.RecordStop(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, receipt, nil)
	if e != nil || stopped.State != "quality_failed" || !stopped.GPUReleased {
		t.Fatal("actual accepted failed-quality report requires positive stop", e)
	}
	stoppedAgain, e := g.service.RecordStop(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, receipt, nil)
	if e != nil || !stoppedAgain.Duplicate || stoppedAgain.State != "quality_failed" {
		t.Fatal("prepared exact stop replay", e)
	}
	t.Logf("Actual native model files9 bytes%d chunks%d; real DB/file/tensor/report/audit/replay, synthetic production/stop observations, no actual container or GPU qualification", total, chunks)
}
