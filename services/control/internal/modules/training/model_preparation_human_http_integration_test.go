//go:build training_artifact_integration && model_lifecycle_admission_integration

package training_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
)

func preparationHumanClients(t *testing.T, g preparationScenario) (*httpfixture.Client, *httpfixture.Client) {
	t.Helper()
	server := httpfixture.NewTLSServer(t, preparationHTTPHandler(g))
	t.Cleanup(server.Close)
	schemas := httpfixture.Schemas(t, "../../../../../api/openapi/v1/model-lifecycle/openapi.json")
	admin := httpfixture.New(t, server, "admin", schemas)
	portal := httpfixture.New(t, server, "portal", schemas)
	for _, client := range []*httpfixture.Client{admin, portal} {
		client.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": g.f.password}, 200, "")
	}
	return admin, portal
}
func TestActualCpuModelPreparationHumanTLSCreateReadCSRFQueryRoleAndCancellation(t *testing.T) {
	g := newPreparationScenario(t)
	admin, portal := preparationHumanClients(t, g)
	workspace := g.input.WorkspaceID
	base := "/api/v1/workspaces/" + workspace + "/model-preparations"
	adminBase := "/api/v1/admin/model-preparations"
	caps := portal.Call("GET", base+"/capabilities", nil, 200, "ModelPreparationCapabilitiesResponse")
	if caps["execution_configured"] != true || caps["can_create"] != true || caps["can_review"] != false || caps["can_cancel_own"] != true {
		t.Fatal("current owner capabilities")
	}
	// Only the real credentialed synthetic observation makes a selectable target.
	portal.Call("GET", base+"/nodes/"+g.node.NodeID, nil, 409, "Problem")
	preparationHardware(t, g)
	target := portal.Call("GET", base+"/nodes/"+g.node.NodeID, nil, 200, "ModelPreparationTargetResponse")
	if target["node_id"] != g.node.NodeID || len(target["gpus"].([]any)) != 1 {
		t.Fatal("fresh scoped target inventory")
	}
	portal.Call("GET", base+"/nodes/"+g.f.input.NodeID, nil, 403, "Problem")
	admin.Call("GET", adminBase+"/nodes/"+g.node.NodeID+"?workspace_id="+workspace, nil, 200, "ModelPreparationTargetResponse")
	portal.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	csrf := portal.CSRF
	portal.CSRF = ""
	portal.Call("POST", base, g.input, 403, "Problem")
	portal.CSRF = csrf
	p := portal.Write("POST", base, g.input, 201, "ModelPreparationResponse")
	id := p["id"].(string)
	replay := portal.Write("POST", base, g.input, 201, "ModelPreparationResponse")
	if replay["id"] != id {
		t.Fatal("HTTP exact intent replay")
	}
	portal.Headers = map[string]string{}
	portal.Call("GET", base+"?limit=1", nil, 200, "ModelPreparationPageResponse")
	portal.Call("GET", base+"/"+id, nil, 200, "ModelPreparationResponse")
	report := portal.Call("GET", base+"/"+id+"/report", nil, 200, "ModelPreparationReportResponse")
	if report["result"] != nil || report["artifact_id"] != nil || report["review"] != nil {
		t.Fatal("queued report fabricated result")
	}
	portal.Call("GET", base+"/"+id+"/attempts?limit=1", nil, 200, "ModelPreparationAttemptPageResponse")
	for _, query := range []string{"?limit=01", "?limit=1&limit=2", "?unknown=1"} {
		portal.Call("GET", base+query, nil, 400, "Problem")
	}
	admin.Call("GET", adminBase+"/"+id+"?workspace_id="+workspace, nil, 200, "ModelPreparationResponse")
	admin.Call("GET", adminBase+"?workspace_id="+workspace+"&workspace_id="+workspace, nil, 400, "Problem")
	portal.Call("GET", "/api/v1/workspaces/"+uuid.NewString()+"/model-preparations/"+id, nil, 404, "Problem")
	raw, _ := json.Marshal(modelpreparations.ActionRequest{ExpectedRevision: 1, Reason: "Cancel through actual scoped TLS"})
	raw = append([]byte(`{"unknown":1,`), raw[1:]...)
	portal.Write("POST", base+"/"+id+"/cancellations", raw, 400, "Problem")
	if _, e := g.f.s.DB.Exec(context.Background(), "UPDATE workspace_memberships SET role='member' WHERE workspace_id=$1 AND user_id=$2", workspace, g.f.portal.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	memberCaps := portal.Call("GET", base+"/capabilities", nil, 200, "ModelPreparationCapabilitiesResponse")
	if memberCaps["can_manage"] != false || memberCaps["can_cancel_own"] != true {
		t.Fatal("current member original creator capabilities")
	}
	cancelled := portal.Write("POST", base+"/"+id+"/cancellations", modelpreparations.ActionRequest{ExpectedRevision: 1, Reason: "Cancel real queued preparation"}, 200, "ModelPreparationResponse")
	if cancelled["state"] != "cancelled" {
		t.Fatal("real cancellation not persisted")
	}
	if _, e := g.f.s.DB.Exec(context.Background(), "UPDATE workspace_memberships SET role='viewer' WHERE workspace_id=$1 AND user_id=$2", workspace, g.f.portal.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	caps = portal.Call("GET", base+"/capabilities", nil, 200, "ModelPreparationCapabilitiesResponse")
	if caps["can_create"] != false || caps["can_manage"] != false || caps["can_cancel_own"] != false {
		t.Fatal("viewer mutation controls")
	}
	portal.Call("GET", base+"/"+id, nil, 200, "ModelPreparationResponse")
	portal.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	portal.Write("POST", base, g.input, 403, "Problem")
	portal.Write("POST", base+"/"+id+"/reviews", map[string]any{}, 404, "Problem")
}

func TestActualCpuModelPreparationHumanTLSReportEventsReviewSchemaAndScope(t *testing.T) {
	// Explicit accepted/stopped metadata substitute; actual native full approval has its own profile.
	g, p, input := reviewMetadataFixture(t)
	admin, portal := preparationHumanClients(t, g)
	base := "/api/v1/workspaces/" + p.WorkspaceID + "/model-preparations/" + p.ID
	report := portal.Call("GET", base+"/report", nil, 200, "ModelPreparationReportResponse")
	if report["artifact_id"] != input.ArtifactID || report["report_sha256"] != input.ReportSHA256 || report["result"] == nil || report["physical_stopped_at"] == nil {
		t.Fatal("actual frozen metadata report")
	}
	attempts := portal.Call("GET", base+"/attempts?limit=1", nil, 200, "ModelPreparationAttemptPageResponse")
	if len(attempts["items"].([]any)) != 1 {
		t.Fatal("current attempt missing")
	}
	events := portal.Call("GET", base+"/attempts/"+*p.CurrentAttemptID+"/events?limit=2&after=0", nil, 200, "ModelPreparationEventPageResponse")
	if len(events["items"].([]any)) != 2 || events["next_after"] != float64(2) {
		t.Fatal("bounded ordered event page")
	}
	portal.Call("GET", base+"/attempts/"+uuid.NewString()+"/events", nil, 404, "Problem")
	portal.Call("GET", base+"/attempts/"+*p.CurrentAttemptID+"/events?after=00", nil, 400, "Problem")
	admin.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	review := admin.Write("POST", "/api/v1/admin/model-preparations/"+p.ID+"/reviews?workspace_id="+p.WorkspaceID, input, 201, "ModelPreparationReviewResponse")
	if review["decision"] != "reject" || review["model_id"] != nil {
		t.Fatal("actual scoped Admin rejection")
	}
	report = portal.Call("GET", base+"/report", nil, 200, "ModelPreparationReportResponse")
	if report["review"] == nil {
		t.Fatal("immutable current review not readable")
	}
}

func TestActualCpuModelPreparationHumanTLSRecoveryExactReplayAndFrozenInputs(t *testing.T) {
	g := newPreparationScenario(t)
	p := preparationStoppedUndelivered(t, g)
	admin, portal := preparationHumanClients(t, g)
	base := "/api/v1/workspaces/" + p.WorkspaceID + "/model-preparations/" + p.ID
	input := modelpreparations.RecoveryRequest{ExpectedRevision: p.Revision, NodeID: g.node.NodeID, Reason: "Explicit actual TLS stopped-owner recovery"}
	portal.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	value := portal.Write("POST", base+"/recoveries", input, 201, "ModelPreparationResponse")
	replay := portal.Write("POST", base+"/recoveries", input, 201, "ModelPreparationResponse")
	if value["state"] != "queued" || value["pending_recovery_id"] == nil || replay["pending_recovery_id"] != value["pending_recovery_id"] || value["input_sha256"] != p.InputSHA256 {
		t.Fatal("frozen recovery replay")
	}
	input.Reason = "Changed recovery body"
	portal.Write("POST", base+"/recoveries", input, 409, "Problem")
	admin.Call("GET", "/api/v1/admin/model-preparations/"+p.ID+"/report?workspace_id="+p.WorkspaceID, nil, 200, "ModelPreparationReportResponse")
}
