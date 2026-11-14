//go:build plugin_database_integration && knowledge_gateway_integration

package distribution_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/notifications"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/preferences"
)

type portalNativeState struct {
	Preference preferences.Preference
	Notice     notifications.Notification
	RunID      string
}

func capturePortalNativeState(t *testing.T, p *nativeWorkflowProcess, run string) portalNativeState {
	t.Helper()
	out := portalNativeState{RunID: run}
	if json.Unmarshal(p.call("GET", "/api/v1/preferences", nil, "", 200), &out.Preference) != nil {
		t.Fatal("native preference read")
	}
	if json.Unmarshal(p.call("PATCH", "/api/v1/preferences", preferences.UpdateRequest{Theme: new(preferences.DarkTheme), ExpectedRevision: out.Preference.Revision}, "", 200), &out.Preference) != nil || out.Preference.Theme != preferences.DarkTheme {
		t.Fatal("native persisted fixed theme")
	}
	var page notifications.Page
	if json.Unmarshal(p.call("GET", "/api/v1/notifications?limit=100", nil, "", 200), &page) != nil {
		t.Fatal("native terminal notices")
	}
	for _, n := range page.Items {
		if n.RunID == run {
			out.Notice = n
		}
	}
	if out.Notice.ID == "" || out.Notice.Outcome != "succeeded" {
		t.Fatal("native actual-success notice missing")
	}
	if json.Unmarshal(p.call("POST", "/api/v1/notifications/"+out.Notice.ID+"/read", map[string]any{}, "", 200), &out.Notice) != nil || out.Notice.ReadAt == nil {
		t.Fatal("native actual read state")
	}
	return out
}
func verifyPortalNativeState(t *testing.T, p *nativeWorkflowProcess, want portalNativeState) {
	t.Helper()
	var pref preferences.Preference
	if json.Unmarshal(p.call("GET", "/api/v1/preferences", nil, "", 200), &pref) != nil || pref.Theme != want.Preference.Theme || pref.Revision != want.Preference.Revision || !pref.UpdatedAt.Equal(want.Preference.UpdatedAt) {
		t.Fatal("restart/restore lost preference identity")
	}
	var page notifications.Page
	if json.Unmarshal(p.call("GET", "/api/v1/notifications?limit=100", nil, "", 200), &page) != nil {
		t.Fatal("restart/restore notice page")
	}
	found := false
	for _, n := range page.Items {
		if n.ID == want.Notice.ID {
			found = true
			if n.RunID != want.RunID || n.Outcome != "succeeded" || n.ReadAt == nil || !n.ReadAt.Equal(*want.Notice.ReadAt) {
				t.Fatal("restart/restore changed terminal provenance or read state")
			}
		}
	}
	if !found {
		t.Fatal("restart/restore lost authorized terminal notice")
	}
}

func verifyPortalTargetClient(t *testing.T, ctx context.Context, p *nativeWorkflowProcess, scope, workspace, workflow, password string) {
	t.Helper()
	directory := filepath.Dir(p.path)
	credential := filepath.Join(directory, "synthetic-portal-target-credentials.json")
	raw, _ := json.Marshal(map[string]string{"email": "admin@example.invalid", "password": password})
	if e := os.WriteFile(credential, raw, 0600); e != nil {
		t.Fatal(e)
	}
	raw, _ = json.Marshal(map[string]any{"schema_version": 1, "kind": "projectone-isolated-portal-journey-v1", "scope_id": scope, "mount_root": os.Getenv("PROJECT_ONE_EXTERNAL_ROOT"), "workspace_id": workspace, "portal_origin": p.origin, "ca_file": p.caFile, "credentials_file": credential, "workflow_id": workflow})
	profile := filepath.Join(directory, "portal-target-profile.json")
	if e := os.WriteFile(profile, raw, 0600); e != nil {
		t.Fatal(e)
	}
	output := filepath.Join(os.Getenv("PROJECT_ONE_EVIDENCE_ROOT"), "portal-target-client")
	command := exec.CommandContext(ctx, "python3", "-B", filepath.Join(os.Getenv("PROJECT_ONE_REPOSITORY_ROOT"), "tests/validation/portal/journey.py"), "--local-protocol-check", "--config", profile, "--output-directory", output)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if e := command.Run(); e != nil {
		t.Fatal("actual local Portal target-client protocol", e)
	}
	raw, e := os.ReadFile(filepath.Join(output, "manifest.json"))
	var result struct {
		Status string `json:"status"`
	}
	if e != nil || json.Unmarshal(raw, &result) != nil || result.Status != "passed_local_protocol_only" {
		t.Fatal("actual Portal target-client receipt")
	}
}
