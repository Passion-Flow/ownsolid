package http_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestLaneDWorkspaceReadsTLS(t *testing.T) {
	c := newWorkflowCLI(t, nil, nil)
	s := c.service
	ctx := t.Context()
	var tenant, actor string
	if err := s.DB.QueryRow(ctx, "SELECT tenant_id::text,primary_admin_user_id::text FROM installation_state WHERE id=1").Scan(&tenant, &actor); err != nil {
		t.Fatal(err)
	}
	first, second, decoy := uuid.NewString(), uuid.NewString(), uuid.NewString()
	foreignTenant, foreign := uuid.NewString(), uuid.NewString()
	const search = " %_ \u4e00 "
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := s.DB.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,$3,'active'),($4,$2,$5,'disabled'),($6,$2,$7,'active')",
		first, tenant, "CLI Scope"+search+"A", second, "CLI Scope"+search+"B", decoy, "CLI Scope xx \u4e00 C")
	exec("INSERT INTO tenants(id,name,status) VALUES($1,'Foreign workspace CLI tenant','active')", foreignTenant)
	exec("INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,$3,'active')", foreign, foreignTenant, "CLI Scope"+search+"Foreign")

	configFile := filepath.Join(c.config, "config.json")
	raw, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]string
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	config["site_id"], config["cell_id"], config["workspace_id"] = "other-site", "other-cell", foreign
	unchanged, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configFile, unchanged, 0600); err != nil {
		t.Fatal(err)
	}

	type page struct {
		Items []map[string]any `json:"items"`
		Next  *string          `json:"next_cursor"`
	}
	read := func(raw string) page {
		t.Helper()
		var result page
		if err := json.Unmarshal([]byte(raw), &result); err != nil || result.Items == nil {
			t.Fatalf("invalid workspace page: %v", err)
		}
		for _, item := range result.Items {
			if item["tenant_id"] != tenant || item["id"] == foreign {
				t.Fatal("workspace page disclosed another tenant")
			}
		}
		return result
	}
	refused := func(want string, args ...string) {
		t.Helper()
		out, stderr, err := c.run(args...)
		if out != "" || err == nil || !strings.Contains(stderr, want) {
			t.Fatalf("workspace command did not refuse with %s and empty output: %q %s (%v)", want, out, stderr, err)
		}
	}

	args := []string{"workspace", "list", "--query", search, "--limit", "1", "--output", "json"}
	a := read(c.ok("", args...))
	if len(a.Items) != 1 || a.Next == nil {
		t.Fatal("filtered workspace page lost its continuation")
	}
	nextArgs := append(append([]string{}, args...), "--cursor", *a.Next)
	nextRaw := c.ok("", nextArgs...)
	b := read(nextRaw)
	if len(b.Items) != 1 || b.Next != nil || b.Items[0]["id"] == a.Items[0]["id"] || c.ok("", nextArgs...) != nextRaw {
		t.Fatal("workspace continuation overlapped, changed on retry or lost its end")
	}
	seen := map[any]bool{a.Items[0]["id"]: true, b.Items[0]["id"]: true}
	if !seen[first] || !seen[second] {
		t.Fatal("literal wildcard/Unicode search changed its matching workspaces")
	}
	refused("INVALID_CURSOR", "workspace", "list", "--query", "CLI Scope", "--cursor", *a.Next)
	refused("RESOURCE_NOT_FOUND", "workspace", "get", foreign)
	refused("nonzero UUID", "workspace", "get", "00000000-0000-0000-0000-000000000000")

	var detail map[string]any
	if err := json.Unmarshal([]byte(c.ok("", "workspace", "get", second, "-o", "json")), &detail); err != nil ||
		detail["id"] != second || detail["tenant_id"] != tenant || detail["status"] != "disabled" ||
		detail["is_default"] != false || detail["role"] != nil {
		t.Fatalf("disabled workspace detail changed its identity or nullable role: %v", err)
	}
	c.ok(first, "workspace", "list", "--query", search)
	c.ok(second, "workspace", "get", second, "-o", "yaml")
	empty := read(c.ok("", "workspace", "list", "--query", uuid.NewString(), "-o", "json"))
	if len(empty.Items) != 0 || empty.Next != nil {
		t.Fatal("unmatched workspace search did not return an empty final page")
	}
	all := read(c.ok("", "workspace", "list", "-o", "json"))
	if len(all.Items) == 0 {
		t.Fatal("default workspace page was incorrectly scoped by local configuration")
	}

	exec("UPDATE users SET is_platform_admin=false WHERE id=$1", actor)
	refused("AUTH_SESSION_EXPIRED", nextArgs...)
	refused("AUTH_SESSION_EXPIRED", "workspace", "get", second)
	exec("UPDATE users SET is_platform_admin=true WHERE id=$1", actor)
	c.ok("signed out", "logout")
	c.ok("signed in", "login", "--email", "admin@example.invalid", "--password-file", filepath.Join(c.config, "password"))
	if c.ok("", nextArgs...) != nextRaw {
		t.Fatal("fresh authorized login changed the retained actor-bound continuation")
	}

	exec("UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='logout' WHERE user_id=$1 AND audience='admin' AND revoked_at IS NULL", actor)
	refused("AUTH_SESSION_EXPIRED", nextArgs...)
	refused("AUTH_SESSION_EXPIRED", "workspace", "get", first)
	after, err := os.ReadFile(configFile)
	if err != nil || string(after) != string(unchanged) {
		t.Fatal("workspace reads changed local configuration")
	}
}
