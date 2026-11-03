package http_test

import (
	"testing"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func TestM09CompositionEnabledControlsAndDisabledDescendants(t *testing.T) {
	s := fixture.NewM09(t)
	password := fixture.Bootstrap(t, s)
	h := transport.New(s)
	if h.InitializationError() != nil || h.Models == nil || h.Deployments == nil || h.Components == nil || h.Training == nil || h.Datasets == nil || h.Distribution == nil || h.ModelPreparations != nil {
		t.Fatal("composition ownership")
	}
	server := httpfixture.NewTLSServer(t, h)
	defer server.Close()
	schemas := httpfixture.Schemas(t, "../../../../../../api/openapi/v1/models/openapi.json")
	admin := httpfixture.New(t, server, "admin", schemas)
	admin.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "")
	a := fixture.Login(t, s, "admin@example.invalid", password, "admin")
	w := a.Me.Workspace.ID
	for _, path := range []string{"/api/v1/admin/models/catalog", "/api/v1/admin/models?workspace_id=" + w, "/api/v1/admin/deployments?workspace_id=" + w, "/api/v1/admin/nodes"} {
		admin.Call("GET", path, nil, 200, "")
	}
	portal := httpfixture.New(t, server, "portal", schemas)
	portal.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "")
	for _, tail := range []string{"models", "deployments", "nodes"} {
		portal.Call("GET", "/api/v1/workspaces/"+w+"/"+tail, nil, 200, "")
	}
	for _, tail := range []string{"knowledge-bases", "model-preparations", "image-registry", "plugins", "workflows", "middleware", "aws", "private-clusters"} {
		portal.Call("GET", "/api/v1/workspaces/"+w+"/"+tail, nil, 404, "Problem")
		admin.Call("GET", "/api/v1/admin/"+tail, nil, 404, "Problem")
	}
	portal.CSRF = ""
	problem := portal.Call("POST", "/api/v1/workspaces/"+w+"/model-preparations", map[string]any{}, 403, "Problem")
	if problem["code"] != "CSRF_INVALID" {
		t.Fatal("cumulative disabled-write CSRF order changed")
	}
	portal.Write("POST", "/api/v1/workspaces/"+w+"/model-preparations", map[string]any{}, 404, "Problem")
	for _, path := range []string{"/api/v1/admin/datasets?workspace_id=" + w, "/api/v1/admin/datasets/configuration?workspace_id=" + w} {
		admin.Call("GET", path, nil, 200, "")
	}
	portal.Call("GET", "/api/v1/workspaces/"+w+"/datasets", nil, 200, "")
	for _, path := range []string{"/api/v1/admin/datasets/" + uuid.NewString() + "/mongodb/sources", "/api/v1/workspaces/" + w + "/datasets/" + uuid.NewString() + "/mongodb/imports", "/api/v1/admin/datasets/" + uuid.NewString() + "/training-inputs"} {
		admin.Call("GET", path, nil, 404, "Problem")
	}
	for _, tail := range []string{"gateways", "aliases", "keys", "requests"} {
		admin.Call("GET", "/api/v1/admin/distribution/workspaces/"+w+"/"+tail, nil, 200, "")
	}
	for _, tail := range []string{"aliases", "keys", "requests"} {
		portal.Call("GET", "/api/v1/workspaces/"+w+"/distribution/"+tail, nil, 200, "")
	}
	for _, tail := range []string{"aliases/" + uuid.NewString() + "/cutovers", "aliases/" + uuid.NewString() + "/targets", "training", "requests/" + uuid.NewString() + "/unknown"} {
		admin.Call("GET", "/api/v1/admin/distribution/workspaces/"+w+"/"+tail, nil, 404, "Problem")
		portal.Call("GET", "/api/v1/workspaces/"+w+"/distribution/"+tail, nil, 404, "Problem")
	}
	for _, tail := range []string{"capabilities", "jobs"} {
		admin.Call("GET", "/api/v1/admin/training/"+tail+"?workspace_id="+w, nil, 200, "")
		portal.Call("GET", "/api/v1/workspaces/"+w+"/training/"+tail, nil, 200, "")
	}
	for _, tail := range []string{"jobs/" + uuid.NewString() + "/evaluations", "jobs/" + uuid.NewString() + "/models", "jobs/" + uuid.NewString() + "/files/optimizer.pt", "unknown"} {
		admin.Call("GET", "/api/v1/admin/training/"+tail+"?workspace_id="+w, nil, 404, "Problem")
		portal.Call("GET", "/api/v1/workspaces/"+w+"/training/"+tail, nil, 404, "Problem")
	}
	n := nodes.Service{DB: s.DB, Identity: s, Workspaces: &workspaces.Service{DB: s.DB, Identity: s}}
	for _, profile := range []string{wire.DockerModelLifecycleProfile} {
		if _, e := n.MintForProfile(t.Context(), a, w, "Disabled descendant", profile, uuid.NewString()); e == nil || e.Error() != "AGENT_PROFILE_REJECTED" {
			t.Fatal("later machine authority", profile, e)
		}
	}
	if _, e := n.MintForProfile(t.Context(), a, w, "Explicit synthetic training profile", wire.DockerAIProfile, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	for _, tail := range []string{"model-preparation-input-files", "model-preparation-events", "training-uploads/" + uuid.NewString() + "/unknown", "training-unknown"} {
		admin.Call("POST", "/agent/v1/nodes/"+uuid.NewString()+"/tasks/"+uuid.NewString()+"/"+tail, map[string]any{}, 404, "Problem")
	}
}
