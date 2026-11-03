package http_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func TestM16CompositionEnabledControlsAndDisabledDescendants(t *testing.T) {
	s := fixture.NewM16(t)
	password := fixture.Bootstrap(t, s)
	h := transport.New(s)
	if h.InitializationError() != nil || h.Applications == nil || h.Preferences == nil || h.Notifications == nil || h.SSO == nil || s.Sources != h.SSO.Service || h.Knowledge == nil || h.Knowledge.Service.Distribution != h.Distribution.Service || h.Workflows == nil || h.Workflows.Service.Knowledge != h.Knowledge.Service || h.Plugins == nil || h.Workflows.Service.Plugins != h.Plugins.Service || h.Models == nil || h.Deployments == nil || h.Components == nil || h.Training == nil || h.Datasets == nil || h.Distribution == nil || h.ModelPreparations == nil || h.Training.Holdouts != h.ModelPreparations || h.Nodes.Service.Preparations != h.ModelPreparations {
		t.Fatal("composition ownership")
	}
	if e := h.Notifications.Service.Start(t.Context()); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if e := h.Notifications.Service.Stop(ctx); e != nil {
			t.Error(e)
		}
	})
	server := httpfixture.NewTLSServer(t, h)
	defer server.Close()
	schemas := httpfixture.Schemas(t, "../../../../../../api/openapi/v1/model-lifecycle/openapi.json")
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

	for _, kind := range []string{"model-holdouts", "model-preparations"} {
		admin.Call("GET", "/api/v1/admin/"+kind+"?workspace_id="+w, nil, 200, "")
		portal.Call("GET", "/api/v1/workspaces/"+w+"/"+kind, nil, 200, "")
		admin.Call("GET", "/api/v1/admin/"+kind+"/"+uuid.NewString()+"?workspace_id="+w, nil, 404, "Problem")
		portal.Call("GET", "/api/v1/workspaces/"+w+"/"+kind+"/future", nil, 404, "Problem")
	}
	for _, tail := range []string{"image-registry", "applications", "notifications", "middleware", "aws", "private-clusters"} {
		portal.Call("GET", "/api/v1/workspaces/"+w+"/"+tail, nil, 404, "Problem")
		admin.Call("GET", "/api/v1/admin/"+tail, nil, 404, "Problem")
	}
	n := nodes.Service{DB: s.DB, Identity: s, Workspaces: &workspaces.Service{DB: s.DB, Identity: s}}
	for _, profile := range []string{wire.DockerAIProfile, wire.DockerModelLifecycleProfile} {
		if _, e := n.MintForProfile(t.Context(), a, w, "Explicit selected profile", profile, uuid.NewString()); e != nil {
			t.Fatal(profile, e)
		}
	}
	for _, tail := range []string{"targets", "cutovers"} {
		admin.Call("GET", "/api/v1/admin/distribution/workspaces/"+w+"/aliases/"+uuid.NewString()+"/"+tail, nil, 404, "Problem")
		portal.Call("GET", "/api/v1/workspaces/"+w+"/distribution/aliases/"+uuid.NewString()+"/"+tail, nil, 404, "Problem")
	}
	admin.Call("GET", "/api/v1/admin/knowledge-bases/configuration?workspace_id="+w, nil, 200, "")
	portal.Call("GET", "/api/v1/workspaces/"+w+"/knowledge-bases/configuration", nil, 200, "")
	admin.Call("GET", "/api/v1/admin/workflows/configuration?workspace_id="+w, nil, 200, "")
	portal.Call("GET", "/api/v1/workspaces/"+w+"/workflows/configuration", nil, 200, "")
	portal.Call("GET", "/api/v1/preferences", nil, 200, "")
	portal.Call("GET", "/api/v1/notifications", nil, 200, "")
	portal.Call("GET", "/api/v1/applications?workspace_id="+w, nil, 200, "")
	portal.CSRF = ""
	problem := portal.Call("POST", "/api/v1/workspaces/"+w+"/model-preparations", map[string]any{}, 403, "Problem")
	if problem["code"] != "CSRF_INVALID" {
		t.Fatal("CSRF ordering")
	}
	portal.Write("POST", "/api/v1/workspaces/"+w+"/model-preparations", map[string]any{}, 400, "Problem")
}

func TestM16CompositionCloneBindsSourceGuardToCurrentIdentity(t *testing.T) {
	s := fixture.NewM16(t)
	h := transport.New(s)
	if h.InitializationError() != nil {
		t.Fatal(h.InitializationError())
	}
	clone := *s
	next := transport.New(&clone)
	if next.InitializationError() != nil || next.SSO.Service.Identity != &clone || clone.Sources != next.SSO.Service || next.SSO.Service == h.SSO.Service {
		t.Fatal("source guard retained another identity/pool")
	}
}
