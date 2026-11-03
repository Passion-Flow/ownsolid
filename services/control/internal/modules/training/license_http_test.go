package training_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
)

// The dataset processor/publication and initialized local stores are real.
// GPU/model/image observations are the existing explicit synthetic fixtures; no
// training job is dispatched and no NVIDIA/AI execution qualification is claimed.
func TestM19ExpiredLicenseQueuedTrainingCancellationHTTP(t *testing.T) {
	f := setup(t)
	attachStore(t, f)
	hardware(t, f)
	ctx := context.Background()
	owner := f.s.Identity
	handler := transport.New(owner)
	handler.Training.Store = f.s.Store
	handler.Datasets.Service.Store = f.s.Datasets.Store
	handler.Datasets.Service.Processor = f.s.Datasets.Processor
	server := httpfixture.NewTLSServer(t, handler)
	defer server.Close()
	schemas := httpfixture.Schemas(t, "../../../../../api/openapi/v1/training/openapi.json")
	portal := httpfixture.New(t, server, "portal", schemas)
	admin := httpfixture.New(t, server, "admin", schemas)
	for _, client := range []*httpfixture.Client{portal, admin} {
		client.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": f.password}, 200, "")
	}
	base := "/api/v1/workspaces/" + f.input.WorkspaceID + "/training"
	var created []map[string]any
	for range 2 {
		portal.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
		created = append(created, portal.Write("POST", base+"/jobs", f.input, 201, "TrainingJobResponse"))
	}
	trust, e := owner.Config.LicenseTrust()
	if e != nil {
		t.Fatal(e)
	}
	expired, e := licensing.New(owner, trust, owner.Config.LicensePolicy(), func() time.Time { return time.Now().UTC().Add(26 * time.Hour) })
	if e != nil {
		t.Fatal(e)
	}
	owner.BusinessAdmission = expired
	portal.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	denied := portal.Write("POST", base+"/jobs", f.input, 403, "Problem")
	if denied["code"] != "LICENSE_NEW_BUSINESS_UNAVAILABLE" {
		t.Fatal("new training license refusal")
	}
	path := base + "/jobs/" + created[0]["id"].(string)
	portal.CSRF = ""
	portal.Call("POST", path+"/cancellations", map[string]any{"expected_revision": created[0]["revision"], "reason": "No CSRF"}, 403, "Problem")
	portal.Write("POST", path+"/cancellations", map[string]any{"expected_revision": created[0]["revision"].(float64) + 1, "reason": "Synthetic stale cancellation"}, 409, "Problem")
	portal.Write("POST", path+"/cancellations", map[string]any{"expected_revision": created[0]["revision"], "reason": "Strict cancellation", "command": "unapproved"}, 400, "Problem")
	first := portal.Write("POST", path+"/cancellations", map[string]any{"expected_revision": created[0]["revision"], "reason": "Synthetic expired Portal queued cancellation"}, 200, "TrainingJobResponse")
	if first["state"] != "cancelled" || first["finished_at"] == nil || first["cancel_requested_at"] == nil || first["current_attempt_id"] != nil {
		t.Fatal("Portal queued cancellation did not persist without execution")
	}
	secondPath := "/api/v1/admin/training/jobs/" + created[1]["id"].(string) + "/cancellations?workspace_id=" + f.input.WorkspaceID
	second := admin.Write("POST", secondPath, map[string]any{"expected_revision": created[1]["revision"], "reason": "Synthetic expired Admin queued cancellation"}, 200, "TrainingJobResponse")
	if second["state"] != "cancelled" || second["finished_at"] == nil || second["current_attempt_id"] != nil {
		t.Fatal("Admin queued cancellation did not persist")
	}
	// Terminal reduction replay retains the current revision and one original audit.
	again := portal.Write("POST", path+"/cancellations", map[string]any{"expected_revision": first["revision"], "reason": "Synthetic exact terminal cancellation replay"}, 200, "TrainingJobResponse")
	if again["revision"] != first["revision"] {
		t.Fatal("terminal cancellation changed revision")
	}
	portal.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	portal.Write("POST", path+"/recoveries", map[string]any{"expected_revision": first["revision"], "reason": "Denied new recovery start at expiry", "node_id": f.input.NodeID, "mode": "restart", "checkpoint_id": nil}, 403, "Problem")
	var jobs, cancelled, attempts, audits int
	if e = owner.DB.QueryRow(ctx, "SELECT count(*),count(*) FILTER(WHERE state='cancelled') FROM training_jobs").Scan(&jobs, &cancelled); e != nil || jobs != 2 || cancelled != 2 {
		t.Fatal("denied training wrote extra work or cancellation lost", e, jobs, cancelled)
	}
	if e = owner.DB.QueryRow(ctx, "SELECT count(*) FROM training_attempts").Scan(&attempts); e != nil || attempts != 0 {
		t.Fatal("queued cancellation started execution", e, attempts)
	}
	if e = owner.DB.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action='training.cancel_requested'").Scan(&audits); e != nil || audits != 2 {
		t.Fatal("cancellation audit integrity", e, audits)
	}
	if _, e = owner.DB.Exec(ctx, "DELETE FROM workspace_memberships WHERE workspace_id=$1 AND user_id=$2", f.input.WorkspaceID, f.portal.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	portal.Call("GET", path, nil, 404, "Problem")
	portal.Write("POST", path+"/cancellations", map[string]any{"expected_revision": first["revision"], "reason": "Current membership still required after expiry"}, 404, "Problem")
}
