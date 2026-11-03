package http_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/smtpsink"
)

func TestM19ActualIsolatedLicenseTargetScript(t *testing.T) {
	python := os.Getenv("PROJECT_ONE_LICENSE_TARGET_PYTHON")
	if python == "" || !filepath.IsAbs(python) {
		t.Fatal("explicit pinned target validation Python required")
	}
	repository, e := filepath.Abs("../../../../../..")
	if e != nil {
		t.Fatal(e)
	}
	script := filepath.Join(repository, "tools/validation/licensing/qualify.py")
	owner := fixture.New(t)
	password := fixture.Bootstrap(t, owner)
	auth := fixture.Login(t, owner, "admin@example.invalid", password, "admin")
	ctx := context.Background()
	invitation, e := owner.Invite(ctx, auth, "", "createInvitation", uuid.NewString(), "synthetic-license-target@example.invalid", "en", 0, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	portalPassword := fixture.Password()
	if e = owner.Register(ctx, fixture.InvitationToken(t, owner, invitation.ID), "Synthetic isolated target user", portalPassword, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	root := t.TempDir()
	if os.Chmod(root, 0700) != nil {
		t.Fatal("private target scope")
	}
	write := func(name string, value any) string {
		t.Helper()
		raw, e := json.Marshal(value)
		if e != nil {
			t.Fatal(e)
		}
		path := filepath.Join(root, name)
		if os.WriteFile(path, raw, 0600) != nil {
			t.Fatal("private target input")
		}
		return path
	}
	certificate, caFile, _, _ := smtpsink.Certificate(t, root)
	ca, e := os.ReadFile(caFile)
	if e != nil {
		t.Fatal(e)
	}
	caCopy := filepath.Join(root, "trusted-ca.pem")
	if os.WriteFile(caCopy, ca, 0600) != nil {
		t.Fatal("protected target CA")
	}
	server := httptest.NewUnstartedServer(nil)
	_, port, e := net.SplitHostPort(server.Listener.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	owner.Config.Server.PortalOrigin = "https://127.0.0.1:" + port
	owner.Config.Server.AdminOrigin = "https://localhost:" + port
	handler := transport.New(owner)
	trust, e := owner.Config.LicenseTrust()
	if e != nil {
		t.Fatal(e)
	}
	clock := &licenseClock{value: time.Now().UTC()}
	service, e := licensing.New(owner, trust, owner.Config.LicensePolicy(), clock.now)
	if e != nil {
		t.Fatal(e)
	}
	handler.Licensing.Service = service
	owner.BusinessAdmission = service
	server.Config.Handler = handler
	server.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	defer server.Close()
	state, e := service.StatusAdmin(ctx, auth)
	if e != nil {
		t.Fatal(e)
	}
	fingerprint, e := trust.Fingerprint()
	if e != nil {
		t.Fatal(e)
	}
	mount := root
	for {
		parent := filepath.Dir(mount)
		current, ce := os.Stat(mount)
		above, ae := os.Stat(parent)
		if ce != nil || ae != nil {
			t.Fatal("actual target test mount")
		}
		if current.Sys().(*syscall.Stat_t).Dev != above.Sys().(*syscall.Stat_t).Dev {
			break
		}
		if parent == mount {
			t.Fatal("nonroot mounted target test scope required")
		}
		mount = parent
	}
	scope := uuid.NewString()
	config := map[string]any{"schema_version": 1, "kind": "projectone-isolated-license-qualification-v1", "profile": "ubuntu24.04-linux-amd64", "case": "snapshot", "scope_id": scope, "mount_root": mount, "workspace_id": auth.Me.Workspace.ID, "deployment_id": state.DeploymentID, "portal_origin": owner.Config.Server.PortalOrigin, "admin_origin": owner.Config.Server.AdminOrigin, "ca_file": caCopy, "administrator_credentials_file": write("administrator.json", map[string]string{"email": "admin@example.invalid", "password": password}), "portal_credentials_file": write("portal.json", map[string]string{"email": "synthetic-license-target@example.invalid", "password": portalPassword}), "expected_state": "active", "expected_public_key_fingerprint": fingerprint, "expected_history_head_license_id": state.HistoryHeadLicenseID, "expected_accepted_licenses": state.AcceptedLicenses, "request_seconds": 3, "maximum_seconds": 30, "cleanup_seconds": 5, "maximum_requests": 64, "maximum_response_bytes": 1048576}
	write(".projectone-license-test-scope.json", map[string]any{"schema_version": 1, "kind": "projectone-license-test-scope-v1", "purpose": "isolated_test_only", "scope_id": scope, "deployment_id": state.DeploymentID, "workspace_id": auth.Me.Workspace.ID, "portal_origin": owner.Config.Server.PortalOrigin, "admin_origin": owner.Config.Server.AdminOrigin, "permitted_cases": []string{"snapshot", "denial"}})
	count := func(query string) int {
		t.Helper()
		var n int
		if e := owner.DB.QueryRow(ctx, query).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	baselineSessions := count("SELECT count(*) FROM sessions WHERE revoked_at IS NULL")
	run := func(label string, success bool) {
		t.Helper()
		configuration := write(label+"-config.json", config)
		output := filepath.Join(root, label)
		commandCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		command := exec.CommandContext(commandCtx, python, "-B", script, "--config", configuration, "--output", output, "--local-protocol-only")
		command.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
		stdout, err := command.CombinedOutput()
		raw, e := os.ReadFile(filepath.Join(output, "manifest.json"))
		if e != nil {
			if regexp.MustCompile(`^LICENSE_TARGET_[A-Z0-9_]+\n$`).Match(stdout) {
				t.Fatalf("target preparation refused with safe code %s", strings.TrimSpace(string(stdout)))
			}
			t.Fatal("actual target script receipt missing; no safe failure code available")
		}
		for _, secret := range []string{password, portalPassword, "Synthetic isolated fixture", fingerprint + "invalid"} {
			if bytes.Contains(raw, []byte(secret)) {
				t.Fatal("target receipt leaked protected or customer input")
			}
		}
		if evidence := os.Getenv("PROJECT_ONE_EVIDENCE_ROOT"); evidence != "" {
			if os.WriteFile(filepath.Join(evidence, "target-"+label+".json"), raw, 0600) != nil {
				t.Fatal("retained target receipt")
			}
		}
		var receipt map[string]any
		if json.Unmarshal(raw, &receipt) != nil || receipt["mode"] != "local_protocol_only" || receipt["profile"] != "ubuntu24.04-linux-amd64" {
			t.Fatal("local target qualification boundary changed")
		}
		if (err == nil) != success {
			code, _ := receipt["error_code"].(string)
			if !regexp.MustCompile(`^LICENSE_TARGET_[A-Z0-9_]+$`).MatchString(code) {
				code = "LICENSE_TARGET_FAILURE_CODE_UNAVAILABLE"
			}
			t.Fatalf("target script %s expected success=%v, observed process error=%v, safe code=%s; failed receipt retained", label, success, err, code)
		}
		var envelope map[string]any
		if json.Unmarshal(stdout, &envelope) != nil {
			t.Fatal("target script did not emit only safe status")
		}
		if success && receipt["status"] != "passed_local_protocol_scope_only" || !success && receipt["status"] != "failed" {
			t.Fatal("target receipt status mismatch")
		}
		cleanup := receipt["cleanup"].(map[string]any)
		if cleanup["admin"] != "session_closed_or_not_created" || cleanup["portal"] != "session_closed_or_not_created" {
			t.Fatal("actual target logout incomplete")
		}
		if count("SELECT count(*) FROM sessions WHERE revoked_at IS NULL") != baselineSessions {
			t.Fatal("target session cleanup changed preexisting sessions")
		}
	}
	run("snapshot", true)
	config["expected_public_key_fingerprint"] = strings.Repeat("0", 64)
	run("wrong-fingerprint", false)
	config["expected_public_key_fingerprint"] = fingerprint
	clock.set(time.Now().UTC().Add(26 * time.Hour))
	config["case"] = "denial"
	config["expected_state"] = "expired"
	run("expired-denial", true)
	if count("SELECT count(*) FROM node_join_tokens") != 0 || count("SELECT count(*) FROM audit_events WHERE action='license.diagnostics_exported'") != 2 {
		t.Fatal("target denial or diagnostic audit integrity")
	}
	t.Log("Actual standalone Python/TLS/Go/PostgreSQL snapshot and expired denial pass; wrong fingerprint refuses; original sessions preserved; diagnostic audits=" + strconv.Itoa(2) + ". License clock is explicitly injected in this test only, no target qualification.")
}
