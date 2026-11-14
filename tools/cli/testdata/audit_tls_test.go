package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/google/uuid"
)

func TestLaneDAdminAuditTLS(t *testing.T) {
	binary := os.Getenv("LANE_D_CLI_BINARY")
	if binary == "" {
		t.Fatal("set LANE_D_CLI_BINARY to the compiled CLI")
	}
	s := fixture.New(t)
	password := fixture.Bootstrap(t, s)
	var tenant, workspace string
	if err := s.DB.QueryRow(t.Context(), "SELECT tenant_id::text,default_workspace_id::text FROM installation_state WHERE id=1").Scan(&tenant, &workspace); err != nil {
		t.Fatal(err)
	}
	const subjectEmail = "audit-cli-subject@example.invalid"
	subject := uuid.NewString()
	if _, err := s.DB.Exec(t.Context(), `INSERT INTO users(id,email_canonical,email_display,display_name,locale,status)
		VALUES($1,$2,$2,'Audit CLI Subject','en','active')`, subject, subjectEmail); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(t.Context(), "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,'member')", subject, tenant, workspace); err != nil {
		t.Fatal(err)
	}
	store, err := s.Repository()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	requests := []string{uuid.NewString(), uuid.NewString()}
	for _, requestID := range requests {
		if err := store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
			return audit.WriteProductAssistantWithin(ctx, tx, audit.ProductAssistantEvent{
				SubjectUserID: subject, TenantID: tenant, WorkspaceID: workspace, Audience: "portal",
				RequestID: requestID, ToolID: "list_applications", Outcome: "accepted",
			})
		}); err != nil {
			t.Fatal(err)
		}
	}

	server := httptest.NewUnstartedServer(nil)
	t.Cleanup(server.Close)
	s.Config.Server.AdminOrigin = "https://" + server.Listener.Addr().String()
	handler := transport.New(s)
	if err := handler.LicenseInitializationError(); err != nil {
		t.Fatal(err)
	}
	if err := handler.Licensing.Service.VerifyStored(t.Context()); err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = handler
	server.StartTLS()

	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OWNSOLID_CONFIG_DIR", dir)
	t.Setenv("OWNSOLID_SERVER", server.URL)
	ca, passwordFile := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "password")
	config, err := json.Marshal(map[string]string{
		"ca_file": ca, "site_id": "another-site", "cell_id": "another-cell", "workspace_id": uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string][]byte{
		ca:           pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}),
		passwordFile: []byte(password), filepath.Join(dir, "config.json"): config,
	} {
		if err := os.WriteFile(path, content, 0600); err != nil {
			t.Fatal(err)
		}
	}

	run := func(args ...string) ([]byte, string, error) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), binary, args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if bytes.Contains(out, []byte(password)) || strings.Contains(stderr.String(), password) {
			t.Fatal("CLI exposed its password")
		}
		return out, stderr.String(), err
	}
	ok := func(args ...string) []byte {
		t.Helper()
		out, stderr, err := run(args...)
		if err != nil || stderr != "" {
			t.Fatalf("CLI command failed: %v (%s)", err, stderr)
		}
		return out
	}
	refused := func(want string, args ...string) {
		t.Helper()
		out, stderr, err := run(args...)
		if len(out) != 0 || err == nil || !strings.Contains(stderr, want) {
			t.Fatalf("CLI did not fail closed with %s: %v (%s)", want, err, stderr)
		}
	}
	type page struct {
		Items []map[string]any `json:"items"`
		Next  *string          `json:"next_cursor"`
	}
	read := func(raw []byte) page {
		t.Helper()
		var p page
		if err := json.Unmarshal(raw, &p); err != nil || p.Items == nil {
			t.Fatalf("invalid audit page: %v", err)
		}
		return p
	}

	refused("no saved Admin session", "audit", "list")
	ok("login", "--email", "admin@example.invalid", "--password-file", passwordFile)
	lookup := []string{"audit", "list", "--request-id", requests[0], "--output", "json"}
	legacy := read(ok(lookup...))
	enriched := read(ok(append(lookup, "--assistant-actor")...))
	if len(legacy.Items) != 1 || len(enriched.Items) != 1 || legacy.Items[0]["request_id"] != requests[0] ||
		legacy.Items[0]["actor_id"] != nil || enriched.Items[0]["actor_id"] != "ownsolid.product-assistant" ||
		enriched.Items[0]["actor_kind"] != "product_assistant" || enriched.Items[0]["actor_email"] != nil ||
		enriched.Items[0]["subject_user_id"] != subject || enriched.Items[0]["subject_email"] != subjectEmail {
		t.Fatal("the audited assistant identity was not kept separate from the user subject")
	}
	table := string(ok("audit", "list", "--request-id", requests[0], "--assistant-actor"))
	if !strings.Contains(table, "ownsolid.product-assistant") || !strings.Contains(table, subjectEmail) {
		t.Fatal("the audit table hid the assistant or user subject")
	}

	pageArgs := []string{"audit", "list", "--outcome", "accepted", "--limit", "1", "--output", "json"}
	first := read(ok(pageArgs...))
	if len(first.Items) != 1 || first.Next == nil {
		t.Fatal("the first audit page lost its continuation")
	}
	nextArgs := append(pageArgs, "--cursor", *first.Next, "--assistant-actor")
	nextRaw := ok(nextArgs...)
	next := read(nextRaw)
	if len(next.Items) != 1 || next.Next != nil || next.Items[0]["id"] == first.Items[0]["id"] || !bytes.Equal(nextRaw, ok(nextArgs...)) {
		t.Fatal("the continued audit page overlapped, changed on retry or lost its end")
	}
	refused("INVALID_CURSOR", "audit", "list", "--outcome", "failure", "--cursor", *first.Next)
	refused("VALIDATION_FAILED", "audit", "list", "--category", "auth.login_succeeded")
	refused("VALIDATION_FAILED", "audit", "list", "--from", "invalid")
	refused("must be between 1 and 100", "audit", "list", "--limit", "101")

	var loginRequest, actor string
	if err := s.DB.QueryRow(t.Context(), `SELECT request_id::text, actor_user_id::text FROM audit_events
		WHERE action='auth.login_succeeded' AND audience='admin' ORDER BY occurred_at DESC LIMIT 1`).Scan(&loginRequest, &actor); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	login := read(ok("audit", "list", "--action", "auth.login_succeeded", "--category", "auth", "--actor-user-id", actor,
		"--request-id", loginRequest, "--outcome", "success", "--from", now.Add(-time.Minute).Format(time.RFC3339Nano),
		"--to", now.Add(time.Minute).Format(time.RFC3339Nano), "--output", "json"))
	if len(login.Items) != 1 || login.Items[0]["request_id"] != loginRequest || login.Items[0]["actor_user_id"] != actor {
		t.Fatal("the audit query did not preserve the exact human action filters")
	}
	empty := read(ok("audit", "list", "--target-id", uuid.NewString(), "--output", "json"))
	if len(empty.Items) != 0 || empty.Next != nil {
		t.Fatal("the absent target did not produce an empty final page")
	}
	if _, err := s.DB.Exec(t.Context(), `UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='logout'
		WHERE user_id=$1 AND audience='admin' AND revoked_at IS NULL`, actor); err != nil {
		t.Fatal(err)
	}
	refused("AUTH_SESSION_EXPIRED", "audit", "list", "--assistant-actor")
}
