package compose_test

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/applications"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/notifications"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/plugins"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/preferences"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workflows"
	wire "github.com/Passion-Flow/ownsolid/api/plugins/v1"
)

func configureComposePlugins(t *testing.T, conf map[string]any) []byte {
	t.Helper()
	public, private, e := ed25519.GenerateKey(nil)
	if e != nil {
		t.Fatal(e)
	}
	id, _ := wire.KeyID(public)
	conf["plugins"] = map[string]any{"enabled": true, "trusted_keys": []any{map[string]any{"key_id": id, "public_key": base64.StdEncoding.EncodeToString(public)}}}
	runtime, ok := conf["runtime"].(map[string]any)
	if !ok {
		t.Fatal("explicit workflow runtime required")
	}
	runtime["plugins"] = map[string]any{"capsule_bytes": 65536, "manifest_bytes": 4096, "result_bytes": 32768, "maximum_models": 10, "maximum_versions": 10, "maximum_grants": 50, "maximum_references": 200, "commit_seconds": 10, "maintenance_batch": 25, "maintenance_seconds": 45}
	raw, e := wire.Sign(wire.Manifest{PluginID: wire.PluginID, Version: "1.0.0", ABI: wire.ABI, Executor: wire.Executor, Capabilities: []string{wire.Capability}, License: wire.License}, private, wire.Limits{CapsuleBytes: 65536, ManifestBytes: 4096})
	if e != nil {
		t.Fatal(e)
	}
	return raw
}

type pluginComposeHTTP struct {
	t      *testing.T
	client *http.Client
	origin string
}

func (c pluginComposeHTTP) call(method, path string, input, output any, status int) {
	c.t.Helper()
	var body []byte
	if input != nil {
		body, _ = json.Marshal(input)
	}
	request, e := http.NewRequest(method, c.origin+path, bytes.NewReader(body))
	if e != nil {
		c.t.Fatal(e)
	}
	if method != "GET" {
		var csrf struct {
			Token string `json:"token"`
		}
		c.call("GET", "/api/v1/auth/csrf", nil, &csrf, 200)
		request.Header.Set("X-CSRF-Token", csrf.Token)
		request.Header.Set("Origin", c.origin)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", uuid.NewString())
	}
	response, e := c.client.Do(request)
	if e != nil {
		c.t.Fatal("actual installed plugin transport", e)
	}
	defer response.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(response.Body, 1048577))
	if e != nil || len(raw) > 1048576 || response.StatusCode != status {
		var problem struct {
			Code string `json:"code"`
		}
		json.Unmarshal(raw, &problem)
		c.t.Fatalf("actual plugin Compose %s expected%d observed%d code%s", path, status, response.StatusCode, problem.Code)
	}
	if output != nil {
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(raw, &envelope) != nil || json.Unmarshal(envelope.Data, output) != nil {
			c.t.Fatal("actual installed plugin envelope")
		}
	}
}
func exerciseComposePlugins(t *testing.T, client *http.Client, portalOrigin, adminOrigin, password string, capsule []byte, restart func(), toolOnly bool, expectedModels []plugins.ModelReference) {
	t.Helper()
	admin := pluginComposeHTTP{t, client, adminOrigin}
	portal := pluginComposeHTTP{t, client, portalOrigin}
	admin.call("POST", "/api/v1/auth/login", map[string]string{"email": "admin@example.invalid", "password": password}, nil, 200)
	var configuration plugins.Configuration
	admin.call("GET", "/api/v1/admin/plugins/configuration", nil, &configuration, 200)
	if !configuration.Enabled || !configuration.CanManage || configuration.Limits.MaximumModels != 10 || configuration.Limits.MaximumReferences != 200 || len(configuration.TrustedKeyIDs) != 1 {
		t.Fatal("installed plugin nondefault configuration not active")
	}
	var version plugins.Version
	admin.call("POST", "/api/v1/admin/plugins/imports", plugins.ImportRequest{Capsule: base64.StdEncoding.EncodeToString(capsule), SHA256: wire.Digest(capsule), Reason: "Actual synthetic installed signed capsule"}, &version, 201)
	versionPath := "/api/v1/admin/plugins/versions/" + version.ID
	admin.call("POST", versionPath+"/installations", plugins.RevisionReason{ExpectedRevision: version.Revision, Reason: "Actual installed explicit plugin version"}, &version, 200)
	var me struct {
		Workspace struct {
			ID string `json:"id"`
		} `json:"workspace"`
	}
	portal.call("GET", "/api/v1/me", nil, &me, 200)
	workspace := me.Workspace.ID
	var grant plugins.Grant
	grantPath := "/api/v1/admin/plugins/workspaces/" + workspace + "/grants"
	admin.call("POST", grantPath, plugins.GrantRequest{PluginVersionID: version.ID, Reason: "Actual installed exact Workspace grant"}, &grant, 201)
	prefix := "/api/v1/workspaces/" + workspace
	var available plugins.Page[plugins.Grant]
	portal.call("GET", prefix+"/plugins", nil, &available, 200)
	if len(available.Items) != 1 || available.Items[0].PluginVersionID != version.ID {
		t.Fatal("installed scoped plugin selector")
	}
	var draft workflows.Workflow
	portal.call("POST", prefix+"/workflows", map[string]string{"name": "Actual installed Caf\u00e9 tool", "reason": "Actual installed tool workflow"}, &draft, 201)
	if toolOnly {
		var initial workflows.Graph
		if e := json.Unmarshal(draft.DraftGraph, &initial); e != nil || initial.SchemaVersion != 2 || len(initial.Nodes) != 3 {
			t.Fatal("actual tool-only default graph", e)
		}
	}
	ids := []string{uuid.NewString(), uuid.NewString(), uuid.NewString()}
	tool, _ := json.Marshal(workflows.ToolConfiguration{PluginVersionID: version.ID})
	graph := workflows.Graph{SchemaVersion: 2, Nodes: []workflows.Node{{ID: ids[0], Kind: "start", Configuration: json.RawMessage(`{}`)}, {ID: ids[1], Kind: "tool", Position: workflows.Position{X: 220}, Configuration: tool}, {ID: ids[2], Kind: "end", Position: workflows.Position{X: 440}, Configuration: json.RawMessage(`{}`)}}, Edges: []workflows.Edge{{ID: uuid.NewString(), Source: ids[0], Target: ids[1]}, {ID: uuid.NewString(), Source: ids[1], Target: ids[2]}}}
	path := prefix + "/workflows/" + draft.ID
	portal.call("PATCH", path, workflows.UpdateRequest{Name: draft.Name, Graph: graph, Enabled: true, ExpectedRevision: draft.Revision, Reason: "Actual installed saved tool graph"}, &draft, 200)
	var publication workflows.Version
	portal.call("POST", path+"/publications", workflows.RevisionReason{ExpectedRevision: draft.Revision, Reason: "Actual installed immutable tool publication"}, &publication, 201)
	var conversation workflows.Conversation
	portal.call("POST", path+"/conversations", workflows.ConversationRequest{VersionID: publication.ID, Reason: "Actual installed private pinned conversation"}, &conversation, 201)
	var run workflows.Run
	portal.call("POST", path+"/conversations/"+conversation.ID+"/runs", workflows.RunRequest{Question: "List my installed current model references", Reason: "Actual installed current caller SQL"}, &run, 202)
	limit := time.Now().Add(20 * time.Second)
	for {
		portal.call("GET", path+"/runs/"+run.ID, nil, &run, 200)
		if run.State != "pending" && run.State != "running" {
			break
		}
		if time.Now().After(limit) {
			t.Fatal("installed tool run deadline")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if run.State != "succeeded" || run.ResultKind != "model_list" || !run.ResultAvailable || run.ToolResult == nil || len(run.ToolResult.Items) != len(expectedModels) || run.Answer != nil || run.Usage != nil || len(run.Nodes) != 3 {
		t.Fatal("actual installed tool result invalid", run.State, run.ErrorCode)
	}
	for i, expected := range expectedModels {
		if run.ToolResult.Items[i] != expected {
			t.Fatal("installed tool did not return exact independently imported model reference")
		}
	}
	var savedLocale identity.Me
	if os.Getenv("PROJECT_ONE_COMPOSE_LOCALIZATION_ONLY") == "1" {
		portal.call("GET", "/api/v1/me", nil, &savedLocale, 200)
		portal.call("PATCH", "/api/v1/me", map[string]any{"display_name": savedLocale.User.DisplayName, "locale": "fr", "version": savedLocale.User.Version}, &savedLocale, 200)
		if savedLocale.User.Locale != "fr" {
			t.Fatal("installed current static locale save")
		}
	}
	var savedPreference preferences.Preference
	var savedNotice notifications.Notification
	if os.Getenv("PROJECT_ONE_COMPOSE_PORTAL_ONLY") == "1" {
		var apps applications.Page[applications.Application]
		portal.call("GET", "/api/v1/applications?workspace_id="+workspace, nil, &apps, 200)
		if len(apps.Items) != 1 || apps.Items[0].WorkflowID != draft.ID || apps.Items[0].VersionID != publication.ID {
			t.Fatal("installed exact published application projection")
		}
		portal.call("GET", "/api/v1/preferences", nil, &savedPreference, 200)
		portal.call("PATCH", "/api/v1/preferences", preferences.UpdateRequest{Theme: new("dark"), ExpectedRevision: savedPreference.Revision}, &savedPreference, 200)
		var notices notifications.Page
		portal.call("GET", "/api/v1/notifications?limit=100", nil, &notices, 200)
		for _, n := range notices.Items {
			if n.RunID == run.ID {
				savedNotice = n
			}
		}
		if savedNotice.ID == "" || savedNotice.Outcome != "succeeded" {
			t.Fatal("installed genuine terminal notice")
		}
		portal.call("POST", "/api/v1/notifications/"+savedNotice.ID+"/read", map[string]any{}, &savedNotice, 200)
		if savedNotice.ReadAt == nil {
			t.Fatal("installed durable notice read")
		}
	}
	restart()
	if os.Getenv("PROJECT_ONE_COMPOSE_LOCALIZATION_ONLY") == "1" {
		var current identity.Me
		portal.call("GET", "/api/v1/me", nil, &current, 200)
		if current.User.ID != savedLocale.User.ID || current.User.Locale != "fr" || current.User.Version != savedLocale.User.Version {
			t.Fatal("installed immutable projection restart lost locale identity/revision")
		}
	}
	if os.Getenv("PROJECT_ONE_COMPOSE_PORTAL_ONLY") == "1" {
		var preference preferences.Preference
		portal.call("GET", "/api/v1/preferences", nil, &preference, 200)
		if preference.Theme != "dark" || preference.Revision != savedPreference.Revision || !preference.UpdatedAt.Equal(savedPreference.UpdatedAt) {
			t.Fatal("installed restart lost preference")
		}
		var notices notifications.Page
		portal.call("GET", "/api/v1/notifications?limit=100", nil, &notices, 200)
		matches := 0
		for _, n := range notices.Items {
			if n.RunID == run.ID {
				matches++
				if n.ID != savedNotice.ID || n.ReadAt == nil || !n.ReadAt.Equal(*savedNotice.ReadAt) {
					t.Fatal("installed restart changed notice/read identity")
				}
			}
		}
		if matches != 1 {
			t.Fatal("installed duplicate/missing terminal notice")
		}
	}
	portal.call("GET", path+"/runs/"+run.ID, nil, &run, 200)
	if !run.ResultAvailable || run.ToolResult == nil || len(run.ToolResult.Items) != len(expectedModels) || run.VersionID == nil || *run.VersionID != publication.ID {
		t.Fatal("installed restart lost private tool pin/result")
	}
	for i, expected := range expectedModels {
		if run.ToolResult.Items[i] != expected {
			t.Fatal("installed restart changed exact model reference")
		}
	}
	admin.call("PATCH", grantPath+"/"+grant.ID, plugins.GrantUpdate{Enabled: false, ExpectedRevision: grant.Revision, Reason: "Actual installed grant revocation"}, &grant, 200)
	portal.call("GET", path+"/runs/"+run.ID, nil, &run, 200)
	if run.ResultAvailable || run.ToolResult != nil {
		t.Fatal("installed revoked tool history leaked")
	}
	admin.call("PATCH", grantPath+"/"+grant.ID, plugins.GrantUpdate{Enabled: true, ExpectedRevision: grant.Revision, Reason: "Explicit installed reviewed re-enable"}, &grant, 200)
	var references plugins.References
	admin.call("GET", versionPath+"/references", nil, &references, 200)
	if references.Count != 3 {
		t.Fatal("installed exact draft/publication/conversation references", references.Count)
	}
	admin.call("POST", versionPath+"/uninstallations", plugins.UninstallRequest{ExpectedRevision: version.Revision, ReferencesSHA256: references.SHA256, ConfirmReferencedUninstall: true, Reason: "Actual installed reference-aware uninstall"}, &version, 200)
	portal.call("GET", path+"/runs/"+run.ID, nil, &run, 200)
	if run.ResultAvailable || run.ToolResult != nil {
		t.Fatal("installed uninstalled version history leaked")
	}
	raw, _ := json.MarshalIndent(map[string]any{"status": "passed_actual_plugin_enabled_compose_tool_lifecycle", "workflow_id": draft.ID, "run_id": run.ID, "configuration_nondefault": true, "runtime_dml_actual_tool_query": true, "expected_model_references": expectedModels, "actual_model_count": len(expectedModels), "restart_pin_and_history": true, "grant_revocation_withholding": true, "reference_uninstall_withholding": true, "synthetic_signing_key": true, "target_qualification": false}, "", "  ")
	output := os.Getenv("PROJECT_ONE_EVIDENCE_ROOT")
	if output == "" {
		t.Fatal("explicit plugin Compose evidence root required")
	}
	if e := os.WriteFile(filepath.Join(output, "plugin-compose.json"), append(raw, '\n'), 0600); e != nil {
		t.Fatal(e)
	}
}
