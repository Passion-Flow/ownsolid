//go:build license_browser_integration

package http_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/mail"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	strict "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
)

const licenseBrowserSeconds = 120
const licenseBrowserLogBytes = 1 << 20
const licenseBrowserControlBytes = 1024

type browserLog struct{ value bytes.Buffer }

func (b *browserLog) Write(raw []byte) (int, error) {
	if b.value.Len()+len(raw) > licenseBrowserLogBytes {
		return 0, errors.New("LICENSE_BROWSER_LOG_LIMIT")
	}
	return b.value.Write(raw)
}

// These controls exist only in this explicitly tagged test. No product API,
// runtime configuration or release binary can accept a caller-selected clock.
func TestM19ActualReactGoPostgreSQLLicenseBrowser(t *testing.T) {
	owner := fixture.NewUnlicensed(t)
	t.Logf("license_browser_owned_test_schema=%s", owner.DB.Config().ConnConfig.RuntimeParams["search_path"])
	password := fixture.Bootstrap(t, owner)
	base := time.Now().UTC().Truncate(time.Second).Add(2 * time.Second)
	clock := &licenseClock{value: base}
	ctx, cancel := context.WithTimeout(context.Background(), licenseBrowserSeconds*time.Second)
	defer cancel()
	root := t.TempDir()
	if os.Chmod(root, 0700) != nil {
		t.Fatal("private browser fixture")
	}
	write := func(name string, value []byte) string {
		t.Helper()
		path := filepath.Join(root, name)
		if e := os.WriteFile(path, value, 0600); e != nil {
			t.Fatal(e)
		}
		return path
	}
	repository := os.Getenv("PROJECT_ONE_REPOSITORY_ROOT")
	output := os.Getenv("PROJECT_ONE_EVIDENCE_ROOT")
	storage := os.Getenv("PROJECT_ONE_TEST_STORAGE_ROOT")
	canonical := func(path string) bool {
		resolved, e := filepath.EvalSymlinks(path)
		return e == nil && filepath.IsAbs(path) && resolved == path
	}
	if !canonical(repository) || !canonical(output) || !canonical(storage) || !strings.HasPrefix(output, storage+string(filepath.Separator)) {
		t.Fatal("explicit canonical owned repository/evidence/storage required")
	}
	for _, name := range []string{"PROJECT_ONE_LICENSE_ADMIN_ASSETS", "PROJECT_ONE_LICENSE_PORTAL_ASSETS"} {
		value := os.Getenv(name)
		if !canonical(value) || !strings.HasPrefix(value, storage+string(filepath.Separator)) {
			t.Fatal("current isolated production assets required")
		}
		if _, e := os.Stat(filepath.Join(value, "index.html")); e != nil {
			t.Fatal(e)
		}
	}
	policy := owner.Config.Policy()
	policy.UI.PollMilliseconds = 1000
	policy.HTTP.RequestSeconds = 47
	owner.Config.Runtime = &policy
	owner.Config.Server.AdminAssets = os.Getenv("PROJECT_ONE_LICENSE_ADMIN_ASSETS")
	owner.Config.Server.PortalAssets = os.Getenv("PROJECT_ONE_LICENSE_PORTAL_ASSETS")
	server := httptest.NewUnstartedServer(nil)
	defer server.Close()
	_, port, e := net.SplitHostPort(server.Listener.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	owner.Config.Server.AdminOrigin = "https://localhost:" + port
	owner.Config.Server.PortalOrigin = "https://127.0.0.1:" + port
	owner.Mail.Config = owner.Config
	handler := transport.New(owner)
	if e = handler.LicenseInitializationError(); e != nil {
		t.Fatal(e)
	}
	trust, e := owner.Config.LicenseTrust()
	if e != nil {
		t.Fatal(e)
	}
	licenses, e := licensing.New(owner, trust, owner.Config.LicensePolicy(), clock.now)
	if e != nil {
		t.Fatal(e)
	}
	handler.Licensing.Service = licenses
	owner.BusinessAdmission = licenses
	if e = handler.Notifications.Service.Start(ctx); e != nil {
		t.Fatal(e)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if e := handler.Notifications.Service.Stop(cleanup); e != nil {
			t.Error(e)
		}
	}()
	server.Config.Handler = handler
	server.StartTLS()
	pin := sha256.Sum256(server.Certificate().RawSubjectPublicKeyInfo)
	c := licenseClaims(t, owner, base)
	c.CustomerReference = "Synthetic <img onerror=alert(1)> license customer"
	c.NotBefore = base.Add(10 * time.Second).Format(time.RFC3339)
	first := licenseInput(t, owner, c, "")
	pending := c
	pending.LicenseID = uuid.NewString()
	pending.ReplacesLicenseID = c.LicenseID
	pending.NotBefore = base.Add(110 * time.Second).Format(time.RFC3339)
	pending.ExpiresAt = base.Add(180 * time.Second).Format(time.RFC3339)
	pending.GraceUntil = base.Add(240 * time.Second).Format(time.RFC3339)
	competing := pending
	competing.LicenseID = uuid.NewString()
	recovery := pending
	recovery.LicenseID = uuid.NewString()
	recovery.ReplacesLicenseID = pending.LicenseID
	recovery.NotBefore = base.Add(230 * time.Second).Format(time.RFC3339)
	recovery.ExpiresAt = base.Add(300 * time.Second).Format(time.RFC3339)
	recovery.GraceUntil = base.Add(360 * time.Second).Format(time.RFC3339)
	memberEmail := "ordinary-license-browser@example.invalid"
	data := map[string]any{"kind": "projectone-license-browser-v1", "repository": repository, "output": filepath.Join(output, "license-browser"), "admin": owner.Config.Server.AdminOrigin, "portal": owner.Config.Server.PortalOrigin, "spki": base64.StdEncoding.EncodeToString(pin[:]), "certificate_file": write("loopback-certificate.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})), "admin_email": "admin@example.invalid", "admin_password": password, "member_email": memberEmail, "member_password": fixture.Password(), "customer": c.CustomerReference, "deployment_id": c.DeploymentID, "first_id": c.LicenseID, "first_sha256": wire.Digest([]byte(first.Artifact)), "pending_id": pending.LicenseID, "recovery_id": recovery.LicenseID, "poll_milliseconds": policy.UI.PollMilliseconds, "action_timeout_milliseconds": 10000, "navigation_timeout_milliseconds": 20000, "cleanup_milliseconds": 5000, "invalid_file": write("invalid-license.json", []byte("invalid signed license")), "first_file": write("first-license.json", []byte(first.Artifact)), "pending_file": write("pending-license.json", []byte(licenseInput(t, owner, pending, c.LicenseID).Artifact)), "competing_file": write("competing-license.json", []byte(licenseInput(t, owner, competing, c.LicenseID).Artifact)), "recovery_file": write("recovery-license.json", []byte(licenseInput(t, owner, recovery, pending.LicenseID).Artifact))}
	raw, e := json.Marshal(data)
	if e != nil {
		t.Fatal(e)
	}
	input := write("browser-input.json", raw)
	requests, childRequest, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	defer requests.Close()
	defer childRequest.Close()
	childReply, replies, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	defer childReply.Close()
	defer replies.Close()
	controlDone := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(requests)
		scanner.Buffer(make([]byte, 128), licenseBrowserControlBytes)
		encoder := json.NewEncoder(replies)
		for scanner.Scan() {
			var request struct {
				Action string `json:"action"`
			}
			if strict.DecodeStrict(scanner.Bytes(), &request) != nil {
				controlDone <- errors.New("LICENSE_BROWSER_PRIVATE_CONTROL_INVALID")
				return
			}
			reply := map[string]any{"ok": true}
			switch request.Action {
			case "activate":
				clock.set(base.Add(11 * time.Second))
			case "grace":
				clock.set(base.Add(61 * time.Second))
			case "expire", "restore_time":
				clock.set(base.Add(241 * time.Second))
			case "rewind":
				clock.set(base.Add(10 * time.Second))
			case "invitation":
				var id, key, delivery string
				var nonce, ciphertext []byte
				e := owner.DB.QueryRow(ctx, "SELECT id::text FROM invitations WHERE email_canonical=$1 AND status='pending'", memberEmail).Scan(&id)
				if e == nil {
					e = owner.DB.QueryRow(ctx, "SELECT id::text,payload_key_id,payload_nonce,payload_ciphertext FROM mail_deliveries WHERE invitation_id=$1 AND state='queued' ORDER BY created_at DESC LIMIT 1", id).Scan(&delivery, &key, &nonce, &ciphertext)
				}
				var payload mail.Payload
				if e == nil {
					var raw []byte
					raw, e = owner.Mail.Keys.Decrypt(key, delivery, "invitation", nonce, ciphertext)
					if e == nil {
						e = json.Unmarshal(raw, &payload)
					}
				}
				_, token, ok := strings.Cut(payload.Link, "#token=")
				if e != nil || !ok || token == "" {
					controlDone <- errors.New("LICENSE_BROWSER_SYNTHETIC_INVITATION_UNAVAILABLE")
					return
				}
				reply["token"] = token
			default:
				controlDone <- errors.New("LICENSE_BROWSER_PRIVATE_CONTROL_UNKNOWN")
				return
			}
			if e := encoder.Encode(reply); e != nil {
				controlDone <- errors.New("LICENSE_BROWSER_PRIVATE_REPLY_FAILED")
				return
			}
		}
		controlDone <- scanner.Err()
	}()
	command := exec.CommandContext(ctx, "node", filepath.Join(repository, "tests/acceptance/licensing/license-flow.mjs"), input)
	command.ExtraFiles = []*os.File{childRequest, childReply}
	log := &browserLog{}
	command.Stdout = log
	command.Stderr = log
	command.WaitDelay = 5 * time.Second
	command.Cancel = func() error {
		if command.Process == nil {
			return nil
		}
		return command.Process.Signal(syscall.SIGTERM)
	}
	if e = command.Start(); e != nil {
		t.Fatal(e)
	}
	childRequest.Close()
	childReply.Close()
	e = command.Wait()
	requests.Close()
	replies.Close()
	select {
	case controlError := <-controlDone:
		if controlError != nil {
			t.Error(controlError)
		}
	case <-time.After(5 * time.Second):
		t.Error("private control reader did not stop")
	}
	if e != nil {
		t.Fatalf("actual browser failed: %v; inspect the bounded public browser result", e)
	}
	resultRaw, e := os.ReadFile(filepath.Join(output, "license-browser/result.json"))
	if e != nil {
		t.Fatal(e)
	}
	var result struct {
		Status        string   `json:"status"`
		Steps         []string `json:"steps"`
		BrowserExited bool     `json:"browser_process_exited"`
	}
	if json.Unmarshal(resultRaw, &result) != nil || result.Status != "passed_actual_license_React_Go_PostgreSQL_browser_with_explicit_test_clock" || len(result.Steps) != 7 || !result.BrowserExited {
		t.Fatal("actual browser/cleanup receipt incomplete")
	}
	var artifacts, imports, invitations, rejections int
	if e = owner.DB.QueryRow(ctx, "SELECT count(*) FROM platform_license_artifacts").Scan(&artifacts); e != nil {
		t.Fatal(e)
	}
	if e = owner.DB.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action='license.imported'").Scan(&imports); e != nil {
		t.Fatal(e)
	}
	if e = owner.DB.QueryRow(ctx, "SELECT count(*) FROM invitations").Scan(&invitations); e != nil {
		t.Fatal(e)
	}
	if e = owner.DB.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action='license.import_rejected'").Scan(&rejections); e != nil {
		t.Fatal(e)
	}
	if artifacts != 3 || imports != 3 || invitations != 1 || rejections != 2 {
		t.Fatal("real browser idempotence or denied-write integrity", artifacts, imports, invitations)
	}
	t.Log(fmt.Sprintf("actual browser7 steps; signed artifacts=%d, successful import audits=%d, invitations=%d; no SMTP/target clock/native-process qualification", artifacts, imports, invitations))
}
