package training_test

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
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
	if _, e := handler.Training.RecordStop(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, stop, nil); e != nil {
		t.Fatal(e)
	}
	job := portal.Call("GET", jobPath, nil, 200, "TrainingJobResponse")
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
	portal.Write("POST", jobPath+"/recoveries", recovery, 200, "TrainingJobResponse")
	portal.Call("GET", jobPath+"/recoveries", nil, 200, "TrainingRecoveryPageResponse")
	raw, _ := json.Marshal(recovery)
	bad := append([]byte(`{"Unknown":true,`), raw[1:]...)
	portal.Write("POST", jobPath+"/recoveries", bad, 400, "Problem")
	if _, e := f.s.DB.Exec(ctx, "DELETE FROM workspace_memberships WHERE workspace_id=$1 AND user_id=$2", f.input.WorkspaceID, f.portal.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	portal.Call("GET", jobPath, nil, 404, "Problem")
	admin.Call("GET", adminBase+"/jobs/"+id+"?workspace_id="+f.input.WorkspaceID, nil, 200, "TrainingJobResponse")
}
