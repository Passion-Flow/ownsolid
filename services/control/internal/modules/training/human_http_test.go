package training_test

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	"github.com/google/uuid"
)

func TestActualTLSDualEntranceTrainingIntentHistoryMetricsRecoveryAndCurrentRoles(t *testing.T) {
	f := setup(t)
	attachStore(t, f)
	hardware(t, f)
	ctx := context.Background()
	handler := transport.New(f.s.Identity)
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
	capabilities := portal.Call("GET", base+"/capabilities", nil, 200, "TrainingCapabilitiesResponse")
	if capabilities["enabled"] != true || capabilities["can_create"] != true {
		t.Fatal("actual initialized owner capabilities")
	}
	portal.Call("GET", base+"/nodes/"+f.input.NodeID, nil, 200, "TrainingTargetResponse")
	portal.Call("GET", base+"/nodes/"+uuid.NewString(), nil, 404, "Problem")
	portal.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	created := portal.Write("POST", base+"/jobs", f.input, 201, "TrainingJobResponse")
	id := created["id"].(string)
	again := portal.Write("POST", base+"/jobs", f.input, 201, "TrainingJobResponse")
	if again["id"] != id {
		t.Fatal("HTTP creation key replay changed job")
	}
	portal.Headers = nil
	jobPath := base + "/jobs/" + id
	portal.CSRF = ""
	portal.Call("POST", jobPath+"/cancellations", map[string]any{"expected_revision": created["revision"], "reason": "Missing CSRF must fail"}, 403, "Problem")
	adminBase := "/api/v1/admin/training"
	admin.Call("GET", adminBase+"/jobs?workspace_id="+f.input.WorkspaceID+"&state=queued", nil, 200, "TrainingJobPageResponse")
	admin.Call("GET", adminBase+"/jobs/"+id, nil, 400, "Problem")
	portal.Call("GET", jobPath+"/attempts", nil, 200, "TrainingAttemptPageResponse")
	portal.Call("GET", jobPath+"/files/optimizer.pt", nil, 404, "Problem")
	portal.Call("GET", jobPath+"/files/adapter_config.json", nil, 409, "Problem")
	if worked, e := handler.Training.DispatchNext(ctx); e != nil || !worked {
		t.Fatal("actual human-created queued job dispatch", e)
	}
	l := claimed(t, f)
	events := prelude(*l.Training)
	events = append(events, eventFixture(*l.Training, 3, 1, "metric", map[string]any{"values": map[string]any{"loss": 1.0, "learning_rate": 0.0002}}), eventFixture(*l.Training, 4, 2, "metric", map[string]any{"values": map[string]any{"loss": 0.9}}))
	if _, e := handler.Training.RecordEvents(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, events); e != nil {
		t.Fatal(e)
	}
	metricsPath := jobPath + "/metrics?attempt_id=" + l.Training.AttemptID + "&limit=1"
	page := portal.Call("GET", metricsPath, nil, 200, "TrainingMetricRecordPageResponse")
	if len(page["items"].([]any)) != 1 || page["next_cursor"] == nil {
		t.Fatal("actual ordered metric pagination")
	}
	cursor := page["next_cursor"].(string)
	next := portal.Call("GET", metricsPath+"&cursor="+url.QueryEscape(cursor), nil, 200, "TrainingMetricRecordPageResponse")
	if next["items"].([]any)[0].(map[string]any)["global_step"] != float64(2) {
		t.Fatal("metric cursor skipped/replayed actual step")
	}
	portal.Call("GET", jobPath+"/attempts?cursor="+url.QueryEscape(cursor), nil, 400, "Problem")
	portal.Call("GET", jobPath+"/metrics?attempt_id="+uuid.NewString(), nil, 404, "Problem")
	stop := stopFixture(t, f, l)
	stop.Proof = "owned_container_removed"
	container := strings.Repeat("f", 64)
	stop.ContainerID = &container
	stop.AgentErrorCode = "TRAINING_PROCESSOR_MEMORY_EXCEEDED"
	if _, e := handler.Training.RecordStop(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, stop, nil); e != nil {
		t.Fatal(e)
	}
	job := portal.Call("GET", jobPath, nil, 200, "TrainingJobResponse")
	if job["state"] != "failed" || job["error_code"] != stop.AgentErrorCode || job["finished_at"] == nil ||
		count(t, f, "SELECT count(*) FROM gpu_leases WHERE training_job_id=$1 AND state='released'", id) != 1 {
		t.Fatalf("failed attempt did not retain its error and release the stopped GPU: %+v", job)
	}
	recovery := map[string]any{"expected_revision": job["revision"], "reason": "Actual HTTP explicit restart after exact synthetic stop", "node_id": f.input.NodeID, "mode": "restart", "checkpoint_id": nil}
	if _, e := f.s.DB.Exec(ctx, "UPDATE workspace_memberships SET role='viewer' WHERE workspace_id=$1 AND user_id=$2", f.input.WorkspaceID, f.portal.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	capabilities = portal.Call("GET", base+"/capabilities", nil, 200, "TrainingCapabilitiesResponse")
	if capabilities["can_create"] != false {
		t.Fatal("current viewer retained owner controls")
	}
	portal.Call("GET", jobPath+"/attempts", nil, 200, "TrainingAttemptPageResponse")
	portal.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	portal.Write("POST", jobPath+"/recoveries", recovery, 403, "Problem")
	if _, e := f.s.DB.Exec(ctx, "UPDATE workspace_memberships SET role='owner' WHERE workspace_id=$1 AND user_id=$2", f.input.WorkspaceID, f.portal.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	queued := portal.Write("POST", jobPath+"/recoveries", recovery, 200, "TrainingJobResponse")
	pending, ok := queued["pending_recovery_id"].(string)
	if !ok || queued["state"] != "queued" || queued["current_attempt_id"] != l.Training.AttemptID ||
		queued["attempt_number"] != float64(1) || queued["error_code"] != nil {
		t.Fatalf("recovery did not queue the same job for a new attempt: %+v", queued)
	}
	raw, _ := json.Marshal(recovery)
	bad := append([]byte(`{"Unknown":true,`), raw[1:]...)
	portal.Write("POST", jobPath+"/recoveries", bad, 400, "Problem")
	portal.Headers = nil

	cancelPath := jobPath + "/cancellations"
	cancel := map[string]any{"expected_revision": job["revision"], "reason": "Withdraw queued recovery"}
	problem := portal.Write("POST", cancelPath, cancel, 409, "Problem")
	if problem["code"] != "TRAINING_REVISION_CONFLICT" {
		t.Fatalf("stale cancellation: %+v", problem)
	}
	cancel["expected_revision"] = queued["revision"]
	if _, e := f.s.DB.Exec(ctx, `CREATE FUNCTION reject_http_training_cancel() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF NEW.action='training.cancel_requested' THEN
		RAISE EXCEPTION 'SYNTHETIC_CANCEL_AUDIT_FAILURE';
	END IF;
	RETURN NEW;
END
$$;
CREATE TRIGGER reject_http_training_cancel BEFORE INSERT ON audit_events
FOR EACH ROW EXECUTE FUNCTION reject_http_training_cancel()`); e != nil {
		t.Fatal(e)
	}
	portal.Write("POST", cancelPath, cancel, 503, "Problem")
	current := portal.Call("GET", jobPath, nil, 200, "TrainingJobResponse")
	if current["state"] != "queued" || current["revision"] != queued["revision"] || current["pending_recovery_id"] != pending {
		t.Fatalf("failed cancellation audit changed the queued intent: %+v", current)
	}
	if _, e := f.s.DB.Exec(ctx, "DROP TRIGGER reject_http_training_cancel ON audit_events"); e != nil {
		t.Fatal(e)
	}
	cancelled := portal.Write("POST", cancelPath, cancel, 200, "TrainingJobResponse")
	if cancelled["state"] != "cancelled" || cancelled["pending_recovery_id"] != nil || cancelled["finished_at"] == nil ||
		count(t, f, "SELECT count(*) FROM training_jobs WHERE id=$1 AND pending_recovery_id IS NULL", id) != 1 {
		t.Fatalf("cancelled job retained a pending recovery: %+v", cancelled)
	}
	history := portal.Call("GET", jobPath+"/recoveries", nil, 200, "TrainingRecoveryPageResponse")["items"].([]any)
	if len(history) != 1 {
		t.Fatalf("withdrawn recovery history: %+v", history)
	}
	withdrawn := history[0].(map[string]any)
	if withdrawn["id"] != pending || withdrawn["previous_attempt_id"] != l.Training.AttemptID || withdrawn["next_attempt_id"] != nil {
		t.Fatalf("withdrawn recovery history lost its provenance: %+v", withdrawn)
	}
	if worked, e := handler.Training.DispatchNext(ctx); e != nil || worked {
		t.Fatal("withdrawn recovery remained dispatchable", worked, e)
	}

	// Older installations persisted the queued pointer after cancellation.
	if _, e := f.s.DB.Exec(ctx, "UPDATE training_jobs SET pending_recovery_id=$2 WHERE id=$1", id, pending); e != nil {
		t.Fatal(e)
	}
	if legacy := portal.Call("GET", jobPath, nil, 200, "TrainingJobResponse"); legacy["pending_recovery_id"] != nil {
		t.Fatalf("legacy cancelled job exposed an inactive recovery: %+v", legacy)
	}
	recovery["expected_revision"] = cancelled["revision"]
	portal.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	portal.Write("POST", jobPath+"/recoveries", recovery, 200, "TrainingJobResponse")
	portal.Headers = nil
	if worked, e := handler.Training.DispatchNext(ctx); e != nil || !worked {
		t.Fatal("explicit retry did not dispatch", worked, e)
	}
	nextLease := claimed(t, f)
	running := portal.Call("GET", jobPath, nil, 200, "TrainingJobResponse")
	cancel["expected_revision"] = running["revision"]
	cancel["reason"] = "Cancel the running retry"
	cancelling := portal.Write("POST", cancelPath, cancel, 200, "TrainingJobResponse")
	if cancelling["state"] != "cancelling" ||
		count(t, f, "SELECT count(*) FROM gpu_leases WHERE training_job_id=$1 AND state='held' AND revision=2", id) != 1 {
		t.Fatalf("active cancellation released a possibly running attempt: %+v", cancelling)
	}
	agent := httpfixture.New(t, server, "admin", httpfixture.Schemas(t, "../../../../../api/openapi/v1/nodes/openapi.json"))
	agent.Headers = map[string]string{"Authorization": "Bearer " + f.credential}
	stopPath := "/agent/v1/sites/default/cells/default/nodes/" + f.input.NodeID + "/tasks/" + nextLease.TaskID + "/training-stops"
	body := wire.StopSubmission{LeaseToken: nextLease.LeaseToken, Receipt: stopFixture(t, f, nextLease), Outcome: json.RawMessage("null")}
	accepted := agent.Call("POST", stopPath, body, 200, "TrainingStopAcceptanceResponse")
	if accepted["state"] != "cancelled" || accepted["gpu_released"] != true ||
		count(t, f, "SELECT count(*) FROM gpu_leases WHERE training_job_id=$1 AND state='released' AND revision=2", id) != 1 {
		t.Fatalf("exact stop did not release the cancelled attempt: %+v", accepted)
	}
	if _, e := f.s.DB.Exec(ctx, "DELETE FROM workspace_memberships WHERE workspace_id=$1 AND user_id=$2", f.input.WorkspaceID, f.portal.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	portal.Call("GET", jobPath, nil, 404, "Problem")
	admin.Call("GET", adminBase+"/jobs/"+id+"?workspace_id="+f.input.WorkspaceID, nil, 200, "TrainingJobResponse")
}
