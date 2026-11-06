//go:build training_artifact_integration && model_lifecycle_admission_integration

package training_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

// This exact owner observation is an explicit protocol test double, not an actual container/NVIDIA receipt.
func preparationStopReceipt(t *testing.T, g preparationScenario, l agentwire.Lease) wire.StopReceipt {
	t.Helper()
	c := l.Preparation
	sha, e := c.Digest()
	if e != nil {
		t.Fatal(e)
	}
	return wire.StopReceipt{SchemaVersion: 1, ReceiptID: uuid.NewString(), JournalID: uuid.NewString(), NodeID: g.node.NodeID, TaskID: l.TaskID, Scope: c.Scope, AttemptID: c.AttemptID, AttemptNumber: c.AttemptNumber, CommandSHA256: sha, ImageID: *c.Recipe.Identity.ImageID, GPUUUID: c.GPUUUID, ContainerName: wire.ContainerName(c.AttemptID), Proof: "never_created", OwnedContainerAbsent: true, AgentErrorCode: "MODEL_PREPARATION_AGENT_INTERRUPTED", CompletedAt: time.Now().UTC()}
}

func TestActualCpuModelPreparationStopExactAuthorityAuditRollbackReplayAndRelease(t *testing.T) {
	g := newPreparationScenario(t)
	p := preparationDispatched(t, g)
	l := preparationClaimed(t, g)
	ctx := context.Background()
	receipt := preparationStopReceipt(t, g, l)
	_, e := g.service.RecordStop(ctx, g.node.NodeID, g.node.Credential, l.TaskID, security.Token(), receipt, nil)
	expected(t, e, "MODEL_PREPARATION_STOP_INVALID")
	wrong := receipt
	wrong.ImageID = "sha256:" + strings.Repeat("c", 64)
	_, e = g.service.RecordStop(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, wrong, nil)
	expected(t, e, "MODEL_PREPARATION_STOP_INVALID")
	if _, e = g.f.s.DB.Exec(ctx, `CREATE FUNCTION reject_preparation_stop_audit() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='model.preparation_physical_stop_accepted' THEN RAISE EXCEPTION 'SYNTHETIC_PREPARATION_STOP_AUDIT_FAILURE';END IF;RETURN NEW;END;$$;CREATE TRIGGER reject_preparation_stop_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_preparation_stop_audit()`); e != nil {
		t.Fatal(e)
	}
	if _, e = g.service.RecordStop(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, receipt, nil); e == nil {
		t.Fatal("stop audit failure accepted")
	}
	if count(t, g.f, "SELECT count(*) FROM gpu_leases WHERE model_preparation_id=$1 AND state='held'", p.ID) != 1 || count(t, g.f, "SELECT count(*) FROM model_preparation_attempts WHERE id=$1 AND physical_stopped_at IS NULL", *p.CurrentAttemptID) != 1 {
		t.Fatal("partial physical release survived audit rollback")
	}
	if _, e = g.f.s.DB.Exec(ctx, "DROP TRIGGER reject_preparation_stop_audit ON audit_events"); e != nil {
		t.Fatal(e)
	}
	accepted, e := g.service.RecordStop(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, receipt, nil)
	if e != nil || !accepted.GPUReleased || accepted.State != "failed" || accepted.Duplicate {
		t.Fatal("positive exact owner cleanup", e)
	}
	again, e := g.service.RecordStop(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, receipt, nil)
	if e != nil || !again.Duplicate {
		t.Fatal("immutable stop replay", e)
	}
	wrong = receipt
	wrong.ReceiptID = uuid.NewString()
	_, e = g.service.RecordStop(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, wrong, nil)
	expected(t, e, "MODEL_PREPARATION_STOP_CONFLICT")
	if count(t, g.f, "SELECT count(*) FROM audit_events WHERE action='model.preparation_physical_stop_accepted'") != 1 || count(t, g.f, "SELECT count(*) FROM gpu_leases WHERE model_preparation_id=$1 AND state='released'", p.ID) != 1 {
		t.Fatal("duplicate release/audit")
	}
}

func TestActualCpuModelPreparationRestrictedCleanupSurvivesRevocationCancellationDisableAndExpiry(t *testing.T) {
	for _, mode := range []string{"epoch", "cancel", "disabled", "expired"} {
		t.Run(mode, func(t *testing.T) {
			g := newPreparationScenario(t)
			p := preparationDispatched(t, g)
			l := preparationClaimed(t, g)
			ctx := context.Background()
			receipt := preparationStopReceipt(t, g, l)
			var e error
			switch mode {
			case "epoch":
				_, e = g.f.s.DB.Exec(ctx, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", g.f.portal.Me.User.ID)
			case "cancel":
				_, e = g.service.Cancel(ctx, g.f.portal, p.WorkspaceID, p.ID, modelpreparations.ActionRequest{ExpectedRevision: p.Revision, Reason: "Cancel before any container side effect"}, uuid.NewString())
			case "disabled":
				n, readError := g.f.s.Nodes.Get(ctx, g.f.admin, "", g.node.NodeID)
				if readError != nil {
					t.Fatal(readError)
				}
				_, e = g.f.s.Nodes.Update(ctx, g.f.admin, n.ID, n.Name, "disabled", n.Version, "Disable execution but retain restricted stop authority", uuid.NewString())
			case "expired":
				_, e = g.f.s.DB.Exec(ctx, "UPDATE node_tasks SET lease_expires_at=clock_timestamp()-interval '1 second',deadline_at=clock_timestamp()-interval '1 second' WHERE id=$1", l.TaskID)
				if e == nil {
					preparationDue(t, g, p.ID)
					_, e = g.service.ReconcileNext(ctx)
				}
			}
			if e != nil {
				t.Fatal(e)
			}
			accepted, e := g.service.RecordStop(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, receipt, nil)
			if e != nil || !accepted.GPUReleased {
				t.Fatal("restricted cleanup incorrectly required live execution authority", mode, e)
			}
			if mode == "epoch" || mode == "cancel" {
				if accepted.State != "cancelled" {
					t.Fatal("revocation/cancel failed to win")
				}
			}
			if count(t, g.f, "SELECT count(*) FROM gpu_leases WHERE model_preparation_id=$1 AND state='released'", p.ID) != 1 {
				t.Fatal("positive restricted cleanup retained wrong owner")
			}
		})
	}
}

func TestActualCpuModelPreparationFailedWorkerOutcomeRequiresOwnedRemovalAndRetainsExactBytes(t *testing.T) {
	g := newPreparationScenario(t)
	p := preparationDispatched(t, g)
	l := preparationClaimed(t, g)
	ctx := context.Background()
	receipt := preparationStopReceipt(t, g, l)
	raw, _ := json.Marshal(wire.FailedResult{SchemaVersion: 1, WorkerVersion: wire.WorkerVersion, Status: "failed", PreparationID: p.ID, AttemptID: *p.CurrentAttemptID, ErrorCode: "MODEL_PREPARATION_PROCESSOR_TIMEOUT"})
	sha := digest(raw)
	receipt.OutcomeSHA256 = &sha
	_, e := g.service.RecordStop(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, receipt, raw)
	expected(t, e, "MODEL_PREPARATION_STOP_INVALID")
	container := strings.Repeat("b", 64)
	receipt.ContainerID = &container
	receipt.Proof = "owned_container_removed"
	receipt.AgentErrorCode = ""
	accepted, e := g.service.RecordStop(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, receipt, raw)
	if e != nil || accepted.State != "failed" || !accepted.GPUReleased {
		t.Fatal("actual exact failed-result cleanup", e)
	}
	var stored []byte
	if e = g.f.s.DB.QueryRow(ctx, "SELECT outcome_bytes FROM model_preparation_attempts WHERE id=$1", *p.CurrentAttemptID).Scan(&stored); e != nil || string(stored) != string(raw) {
		t.Fatal("original failed-result bytes", e)
	}
	again, e := g.service.RecordStop(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, receipt, nil)
	if e != nil || !again.Duplicate {
		t.Fatal("stored failed-outcome replay", e)
	}
}

func TestActualCpuModelPreparationMissingPreviouslyAcceptedArtifactCannotPreventPhysicalCleanupOrApprove(t *testing.T) {
	g, p, l, metadata := preparationOutputFixture(t)
	ctx := context.Background()
	u, e := g.service.BeginUpload(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, metadata)
	if e != nil {
		t.Fatal(e)
	}
	// Explicit database test double represents an already accepted artifact whose physical files were lost.
	// No FinishUpload/tensor acceptance is claimed by this negative corruption fixture.
	_, e = g.f.s.DB.Exec(ctx, `INSERT INTO model_preparation_artifacts(id,tenant_id,workspace_id,preparation_id,attempt_id,upload_id,manifest_sha256,descriptor,report,report_bytes,storage_path) SELECT id,tenant_id,workspace_id,preparation_id,attempt_id,id,manifest_sha256,descriptor,manifest,manifest_bytes,'artifacts/'||preparation_id::text||'/'||attempt_id::text||'/'||id::text FROM model_preparation_uploads WHERE id=$1`, u.ID)
	if e != nil {
		t.Fatal(e)
	}
	_, e = g.f.s.DB.Exec(ctx, "UPDATE model_preparation_attempts SET outcome=$2,outcome_bytes=$3,outcome_sha256=$4 WHERE id=$1", *p.CurrentAttemptID, metadata, metadata, digest(metadata))
	if e != nil {
		t.Fatal(e)
	}
	receipt := preparationStopReceipt(t, g, l)
	sha := digest(metadata)
	receipt.OutcomeSHA256 = &sha
	receipt.AgentErrorCode = ""
	receipt.Proof = "owned_container_removed"
	container := strings.Repeat("c", 64)
	receipt.ContainerID = &container
	accepted, e := g.service.RecordStop(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, receipt, nil)
	if e != nil || accepted.State != "failed" || !accepted.GPUReleased {
		t.Fatal("corrupt/lost bytes blocked exact physical cleanup", e)
	}
	if count(t, g.f, "SELECT count(*) FROM model_preparations WHERE id=$1 AND state='failed' AND error_code='MODEL_PREPARATION_ARTIFACT_INVALID'", p.ID) != 1 || count(t, g.f, "SELECT count(*) FROM model_versions") != 1 {
		t.Fatal("lost artifact became reviewable/servable")
	}
	if count(t, g.f, "SELECT count(*) FROM model_preparation_artifacts WHERE preparation_id=$1", p.ID) != 1 {
		t.Fatal("corruption cleanup discarded immutable forensic evidence")
	}
}

func TestActualCpuModelPreparationInterruptedUnacceptedOutputDigestAllowsOnlyPhysicalCleanup(t *testing.T) {
	g, p, l, metadata := preparationOutputFixture(t)
	ctx := context.Background()
	receipt := preparationStopReceipt(t, g, l)
	container := strings.Repeat("a", 64)
	receipt.ContainerID = &container
	receipt.Proof = "owned_container_removed"
	sum := sha256.Sum256(metadata)
	sha := hex.EncodeToString(sum[:])
	receipt.OutcomeSHA256 = &sha
	receipt.AgentErrorCode = ""
	_, e := g.service.RecordStop(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, receipt, nil)
	expected(t, e, "MODEL_PREPARATION_REPORT_CONFLICT")
	receipt.AgentErrorCode = "MODEL_PREPARATION_AGENT_INTERRUPTED"
	accepted, e := g.service.RecordStop(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, receipt, nil)
	if e != nil || accepted.State != "failed" || !accepted.GPUReleased {
		t.Fatal("interrupted unaccepted output prevented positive cleanup", e)
	}
	if count(t, g.f, "SELECT count(*) FROM model_preparation_attempts WHERE id=$1 AND outcome IS NULL AND outcome_sha256 IS NULL AND physical_stopped_at IS NOT NULL", *p.CurrentAttemptID) != 1 || count(t, g.f, "SELECT count(*) FROM model_preparation_artifacts WHERE preparation_id=$1", p.ID) != 0 {
		t.Fatal("unaccepted digest became a prepared artifact/result")
	}
}
