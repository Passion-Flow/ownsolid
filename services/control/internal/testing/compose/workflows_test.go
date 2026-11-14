package compose_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workflows"
)

// The actual Linux CPU container runs a started workflow executor under its DML
// role. No model alias/backend is configured in this install-only scope; actual
// full model/browser/native journeys have their separate source-bound profile.
func exerciseComposeWorkflows(t *testing.T, client *http.Client, origin string, restart func(), cumulativeProfile string) {
	t.Helper()
	var csrf string
	call := func(method, path string, value, out any, expected int) {
		t.Helper()
		var body []byte
		if value != nil {
			body, _ = json.Marshal(value)
		}
		request, e := http.NewRequest(method, origin+path, bytes.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		if method != "GET" {
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Origin", origin)
			request.Header.Set("X-CSRF-Token", csrf)
			request.Header.Set("Idempotency-Key", uuid.NewString())
		}
		response, e := client.Do(request)
		if e != nil {
			t.Fatal("actual workflow Compose transport", e)
		}
		defer response.Body.Close()
		raw, e := io.ReadAll(io.LimitReader(response.Body, 1048577))
		if e != nil || response.StatusCode != expected {
			var problem struct{ Code string }
			_ = json.Unmarshal(raw, &problem)
			t.Fatalf("actual workflow Compose %s expected%d observed%d code%s", path, expected, response.StatusCode, problem.Code)
		}
		if out != nil && json.Unmarshal(raw, out) != nil {
			t.Fatal("actual workflow Compose envelope")
		}
	}
	refresh := func() {
		var r struct{ Data struct{ Token string } }
		call("GET", "/api/v1/auth/csrf", nil, &r, 200)
		csrf = r.Data.Token
		if csrf == "" {
			t.Fatal("actual workflow Compose CSRF")
		}
	}
	refresh()
	var me struct {
		Data struct{ Workspace struct{ ID string } }
	}
	call("GET", "/api/v1/me", nil, &me, 200)
	collection := "/api/v1/workspaces/" + me.Data.Workspace.ID + "/workflows"
	probe := func() {
		if cumulativeProfile != "" {
			var runtime struct {
				Data struct {
					ApplicationProfile string `json:"application_profile"`
				}
			}
			call("GET", "/runtime-config.json", nil, &runtime, 200)
			if runtime.Data.ApplicationProfile != cumulativeProfile {
				t.Fatal("current cumulative workflows profile required")
			}
		}
		var r struct{ Data workflows.Configuration }
		call("GET", collection+"/configuration", nil, &r, 200)
		if !r.Data.Enabled || !r.Data.CanEdit || !r.Data.CanExecute || r.Data.Limits.WorkspaceConcurrency != 1 || r.Data.Limits.RetentionDays != 14 {
			t.Fatal("installed workflow nondefault configuration/executor not active")
		}
	}
	probe()
	var draft struct{ Data workflows.Workflow }
	call("POST", collection, map[string]string{"name": "Actual Linux Caf\u00e9 workflow", "reason": "Isolated installed workflow persistence"}, &draft, 201)
	path := collection + "/" + draft.Data.ID
	var graph workflows.Graph
	if json.Unmarshal(draft.Data.DraftGraph, &graph) != nil || len(graph.Nodes) != 4 {
		t.Fatal("installed initial four-node graph")
	}
	graph.Nodes[0].Position.X = 432.25
	call("PATCH", path, workflows.UpdateRequest{Name: draft.Data.Name, Graph: graph, Enabled: true, ExpectedRevision: draft.Data.Revision, Reason: "Installed graph position save"}, &draft, 200)
	digest := draft.Data.DraftSHA256
	restart()
	refresh()
	probe()
	call("GET", path, nil, &draft, 200)
	if draft.Data.DraftSHA256 != digest || json.Unmarshal(draft.Data.DraftGraph, &graph) != nil || graph.Nodes[0].Position.X != 432.25 {
		t.Fatal("installed restart lost workflow graph")
	}
	var validation struct{ Data workflows.Validation }
	input := workflows.RevisionReason{ExpectedRevision: draft.Data.Revision, Reason: "Reject incomplete executable graph"}
	call("POST", path+"/validations", input, &validation, 200)
	if validation.Data.Valid {
		t.Fatal("installed incomplete graph became executable")
	}
	call("POST", path+"/publications", input, nil, 422)
	call("PATCH", path, workflows.UpdateRequest{Name: draft.Data.Name, Graph: graph, Enabled: false, ExpectedRevision: draft.Data.Revision, Reason: "Disable exact installed synthetic workflow"}, &draft, 200)
	if draft.Data.Enabled {
		t.Fatal("installed workflow disable lost")
	}
	t.Log("Actual workflow-enabled Linux arm64 Compose/runtime DML/nondefault policy/started executor/graph save/restart/validation/disable passed; no configured model in this install scope, no complete offline or physical target qualification.")
}
