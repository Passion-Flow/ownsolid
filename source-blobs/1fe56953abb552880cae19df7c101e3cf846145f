package training_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/google/uuid"
)

type gatewayStoreSnapshot struct {
	taskID, taskState, attemptState, physicalState string
	attempt, acknowledged                          int
	leaseMissing, stopMissing                      bool
	cursor                                         int64
}

func gatewayStoreReadback(t *testing.T, f scenario, j training.Job) gatewayStoreSnapshot {
	t.Helper()
	if j.CurrentAttemptID == nil {
		t.Fatal("actual dispatched attempt missing")
	}
	var out gatewayStoreSnapshot
	err := f.s.DB.QueryRow(t.Context(), `SELECT t.id::text,t.state,t.attempt,t.acknowledged_attempt,t.lease_token_hash IS NULL,a.state,a.stop_token_hash IS NULL,g.state,n.task_acknowledged_sequence
 FROM training_attempts a JOIN node_tasks t ON t.id=a.task_id JOIN managed_nodes n ON n.id=t.node_id
 JOIN gpu_leases g ON g.training_job_id=a.job_id AND g.node_id=a.node_id AND g.revision=a.attempt_number
 WHERE a.id=$1 AND a.job_id=$2`, *j.CurrentAttemptID, j.ID).Scan(&out.taskID, &out.taskState, &out.attempt, &out.acknowledged, &out.leaseMissing, &out.attemptState, &out.stopMissing, &out.physicalState, &out.cursor)
	if err != nil {
		t.Fatal("actual attempt ownership readback", err)
	}
	return out
}

// This diagnostic deliberately observes an unsupported constructor path. A pass
// is evidence of the known failure, never acceptance of gateway training claims.
func TestObserveGatewayTrainingClaimArtifactOwnership(t *testing.T) {
	namespace, err := os.Readlink("/proc/self/ns/net")
	if err != nil || namespace != os.Getenv("ARC111_EXPECTED_NETWORK_NAMESPACE") {
		t.Fatal("owned offline database network namespace required")
	}
	f := setup(t)
	j := dispatched(t, f)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	before := gatewayStoreReadback(t, f, j)
	if before.taskState != "pending" || before.attempt != 0 || before.acknowledged != 0 || !before.leaseMissing || !before.stopMissing || before.physicalState != "held" {
		t.Fatal("actual undispatched-lease baseline differs")
	}
	gateway, err := transport.NewAgentGateway(&identity.Service{DB: f.s.DB, Config: f.s.Identity.Config}, func() error { return nil })
	if err != nil {
		t.Fatal("actual gateway construction", err)
	}
	delegated, ok := gateway.Nodes.Training.(*training.Service)
	if !ok || delegated.Datasets == nil || delegated.Datasets.Store != nil || delegated.Identity.Config.Datasets.ArtifactRoot == "" {
		t.Fatal("constructor state differs from the researched missing-store boundary")
	}
	refused, err := gateway.Nodes.Claim(ctx, f.input.NodeID, f.credential, uuid.NewString())
	if err == nil || err.Error() != "DATASET_STORAGE_UNAVAILABLE" || refused.Lease != nil {
		t.Fatal("known gateway file-ownership failure was not observed", err)
	}
	if after := gatewayStoreReadback(t, f, j); after != before {
		t.Fatal("failed gateway claim mutated task, attempt, cursor or physical ownership")
	}
	lease := claimed(t, f)
	if lease.TaskID != before.taskID || lease.Attempt != 1 || lease.Training == nil || lease.Training.Scope.JobID != j.ID || lease.Training.AttemptID != *j.CurrentAttemptID {
		t.Fatal("control file-owning path did not claim the same retained attempt")
	}
	after := gatewayStoreReadback(t, f, j)
	if after.taskState != "running" || after.attempt != 1 || after.acknowledged != 1 || after.leaseMissing || after.stopMissing || after.physicalState != "held" || uint64(after.cursor) != lease.Sequence {
		t.Fatal("actual control claim and acknowledgement did not preserve exact ownership")
	}
	t.Log("KNOWN_GATEWAY_FAILURE=DATASET_STORAGE_UNAVAILABLE gateway_store=nil failed_claim_unchanged=true control_claim_and_ack_same_attempt=true restricted_role_not_tested=true")
}
