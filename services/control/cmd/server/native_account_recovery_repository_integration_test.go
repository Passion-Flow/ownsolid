//go:build repository_integration && fullserve_browser_integration

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/smtpsink"
	strict "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	licensewire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
)

type nativeAccountRecoveryInput struct {
	ApplicationProfile string            `json:"application_profile,omitempty"`
	Format             string            `json:"format"`
	Leaf               string            `json:"leaf"`
	PostgresInput      string            `json:"postgres_input"`
	ControlFile        string            `json:"control_file"`
	ControlSHA256      string            `json:"control_sha256"`
	PortalAssets       string            `json:"portal_assets"`
	AdminAssets        string            `json:"admin_assets"`
	Artifacts          map[string]string `json:"artifacts"`
	ResultDirectory    string            `json:"result_directory"`
}

func nativeAccountInput(t *testing.T, leaf string) nativeAccountRecoveryInput {
	t.Helper()
	file := os.Getenv("PROJECT_ONE_TEST_NATIVE_ACCOUNT_RECOVERY")
	raw, e := identityfixture.ReadNativeRecoveryDocument(file)
	var input nativeAccountRecoveryInput
	if e != nil || strict.DecodeStrict(raw, &input) != nil || input.Format != "projectone.native-account-recovery-input.v1" || input.Leaf != leaf {
		t.Fatal("NATIVE_ACCOUNT_RECOVERY_INPUT_INVALID")
	}
	root := filepath.Dir(file)
	state := filepath.Dir(filepath.Dir(filepath.Dir(root)))
	buildName := filepath.Base(filepath.Dir(input.ControlFile))
	approvedBuild := strings.HasPrefix(buildName, "account-recovery-sql-build-") || strings.HasPrefix(buildName, "fullserve-account-sql-build-")
	if input.PostgresInput != filepath.Join(root, "native-postgres.json") || input.ResultDirectory != filepath.Join(root, "native-artifacts") || filepath.Base(input.ControlFile) != "control-native" || filepath.Dir(filepath.Dir(input.ControlFile)) != filepath.Join(state, "evidence", "m20") || !approvedBuild || liveBrowserFileSHA(t, input.ControlFile) != input.ControlSHA256 {
		t.Fatal("NATIVE_ACCOUNT_RECOVERY_ARTIFACT_SCOPE_INVALID")
	}
	for _, path := range []string{input.PortalAssets, input.AdminAssets, input.ResultDirectory} {
		canonical, e := filepath.EvalSymlinks(path)
		info, se := os.Stat(path)
		if e != nil || se != nil || canonical != path || !info.IsDir() || !strings.HasPrefix(path, filepath.Join(state, "evidence", "m20")+string(os.PathSeparator)) {
			t.Fatal("NATIVE_ACCOUNT_RECOVERY_DIRECTORY_INVALID")
		}
	}
	entries, e := os.ReadDir(input.ResultDirectory)
	if e != nil || len(entries) != 0 || len(input.Artifacts) < 2 || len(input.Artifacts) > 4096 {
		t.Fatal("NATIVE_ACCOUNT_RECOVERY_FRESH_ARTIFACTS_REQUIRED")
	}
	for path, digest := range input.Artifacts {
		if !strings.HasPrefix(path, input.PortalAssets+string(os.PathSeparator)) && !strings.HasPrefix(path, input.AdminAssets+string(os.PathSeparator)) || liveBrowserFileSHA(t, path) != digest {
			t.Fatal("NATIVE_ACCOUNT_RECOVERY_PRODUCTION_ASSET_CHANGED")
		}
	}
	for _, dir := range []string{input.PortalAssets, input.AdminAssets} {
		if _, ok := input.Artifacts[filepath.Join(dir, "index.html")]; !ok {
			t.Fatal("NATIVE_ACCOUNT_RECOVERY_ENTRANCE_MISSING")
		}
	}
	return input
}

// TestActualNativeAccountPostgresRestart is one fixed real SQL/API profile.
// It needs admitted production files, not a Chrome/Node process or browser trust.
func TestActualNativeAccountPostgresRestart(t *testing.T) {
	input := nativeAccountInput(t, "postgres_restart")
	x := serverCompositionSetup(t, true)
	_, port, e := net.SplitHostPort(x.c.Server.ListenAddress)
	if e != nil {
		t.Fatal("NATIVE_ACCOUNT_RECOVERY_LISTENER_INVALID")
	}
	x.c.Server.TransportMode = config.TransportHTTP
	x.c.Server.PortalOrigin, x.c.Server.AdminOrigin = "http://localhost:"+port, "http://127.0.0.1:"+port
	x.c.Server.TLSCertificateFile, x.c.Server.TLSPrivateKeyFile = "", ""
	x.c.Server.PortalAssets, x.c.Server.AdminAssets = input.PortalAssets, input.AdminAssets
	policy := config.DefaultRuntimePolicy()
	policy.Mail.PollMilliseconds, policy.Mail.Retry1Seconds, policy.Mail.Retry2Seconds, policy.Mail.Retry3Seconds, policy.Mail.Retry4Seconds, policy.Mail.JitterPercent = 100, 120, 120, 120, 120, 0
	x.c.Runtime = &policy
	sink := smtpsink.Start(t, false, false)
	sink.SetTemporaryFailure(true)
	host, smtpPort, e := net.SplitHostPort(sink.Listener.Addr().String())
	if e != nil {
		t.Fatal("NATIVE_ACCOUNT_RECOVERY_SMTP_INVALID")
	}
	x.c.SMTP.Host, x.c.SMTP.CAFile = host, sink.CAFile
	x.c.SMTP.Port, e = strconv.Atoi(smtpPort)
	if e != nil {
		t.Fatal("NATIVE_ACCOUNT_RECOVERY_SMTP_INVALID")
	}
	x.owner.Config, x.mail.Config = x.c, x.c
	role := liveRestrictedPostgres(t, x)
	password, newPassword := identityfixture.Password(), identityfixture.Password()
	if x.owner.Bootstrap(x.ctx, "admin@example.invalid", "Native recovery administrator", password, "Synthetic native recovery", "Default") != nil {
		t.Fatal("NATIVE_ACCOUNT_RECOVERY_BOOTSTRAP_FAILED")
	}
	if x.f.PostgreSQL.QueryRow(x.ctx, "SELECT tenant_id::text,default_workspace_id::text,primary_admin_user_id::text FROM installation_state WHERE id=1").Scan(&x.f.Tenant, &x.f.Workspace, &x.f.User) != nil {
		t.Fatal("NATIVE_ACCOUNT_RECOVERY_IDENTITY_FAILED")
	}
	admin := identityfixture.Login(t, x.owner, "admin@example.invalid", password, "admin")
	state, e := x.h.Licensing.Service.StatusAdmin(x.ctx, admin)
	if e != nil {
		t.Fatal("NATIVE_ACCOUNT_RECOVERY_LICENSE_STATE_FAILED")
	}
	now := time.Now().UTC().Truncate(time.Second)
	artifact, e := licensewire.Issue(licensewire.Claims{FormatVersion: 1, LicenseID: uuid.NewString(), CustomerReference: "Synthetic native recovery", DeploymentID: state.DeploymentID, IssuedAt: now.Add(-time.Minute).Format(time.RFC3339), NotBefore: now.Add(-time.Minute).Format(time.RFC3339), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339), GraceUntil: now.Add(2 * time.Hour).Format(time.RFC3339), Scope: licensewire.WholePlatformScope}, x.key)
	if e != nil {
		t.Fatal("NATIVE_ACCOUNT_RECOVERY_LICENSE_FAILED")
	}
	if _, e = x.h.Licensing.Service.Import(x.ctx, admin, licensing.ImportInput{Artifact: string(artifact), Reason: "Synthetic isolated native recovery"}, uuid.NewString()); e != nil {
		t.Fatal("NATIVE_ACCOUNT_RECOVERY_LICENSE_FAILED")
	}
	owned := identityfixture.OwnedNativePostgres(t, x.f.PostgreSQL, input.PostgresInput)
	configuration, e := yaml.Marshal(x.c)
	if e != nil {
		t.Fatal("NATIVE_ACCOUNT_RECOVERY_CONFIGURATION_FAILED")
	}
	configurationFile := x.private(t, "native-serving.yaml", configuration)
	if x.d.Close() != nil {
		t.Fatal("NATIVE_ACCOUNT_RECOVERY_GO_LEASE_RELEASE_FAILED")
	}
	native := liveAccountBrowserInput{ControlFile: input.ControlFile, ControlSHA256: input.ControlSHA256, ResultDirectory: input.ResultDirectory, MaximumLogBytes: 1 << 20}
	stopInitial, initialPID := liveCLIHTTPStart(t, x, native, configurationFile, role, "initial")
	jar, e := cookiejar.New(nil)
	if e != nil {
		t.Fatal("NATIVE_ACCOUNT_RECOVERY_COOKIE_STORE_FAILED")
	}
	client := &http.Client{Jar: jar, Transport: &http.Transport{}, Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	origin := func(audience string) string {
		if audience == "portal" {
			return x.c.Server.PortalOrigin
		}
		if audience == "admin" {
			return x.c.Server.AdminOrigin
		}
		t.Fatal("NATIVE_ACCOUNT_RECOVERY_AUDIENCE_INVALID")
		return ""
	}
	perform := func(audience, method, path string, payload any, csrf string, cookies []*http.Cookie, want int) map[string]any {
		t.Helper()
		var raw []byte
		if payload != nil {
			raw, e = json.Marshal(payload)
			if e != nil {
				t.Fatal("NATIVE_ACCOUNT_RECOVERY_REQUEST_INVALID")
			}
		}
		request, e := http.NewRequestWithContext(x.ctx, method, origin(audience)+path, bytes.NewReader(raw))
		if e != nil {
			t.Fatal("NATIVE_ACCOUNT_RECOVERY_REQUEST_INVALID")
		}
		request.Header.Set("Origin", origin(audience))
		if payload != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		if csrf != "" {
			request.Header.Set("X-CSRF-Token", csrf)
		}
		request.Header.Set("Idempotency-Key", uuid.NewString())
		selected := client
		if cookies != nil {
			selected = &http.Client{Transport: client.Transport, Timeout: client.Timeout}
			for _, cookie := range cookies {
				request.AddCookie(cookie)
			}
		}
		response, e := selected.Do(request)
		if e != nil {
			t.Fatal("NATIVE_ACCOUNT_RECOVERY_ACTUAL_REQUEST_FAILED")
		}
		defer response.Body.Close()
		body, e := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		if e != nil || len(body) == 1<<20 || response.StatusCode != want {
			t.Fatal("NATIVE_ACCOUNT_RECOVERY_ACTUAL_STATUS_FAILED", method, path, want, response.StatusCode)
		}
		if want == http.StatusTooManyRequests {
			retry, e := strconv.Atoi(response.Header.Get("Retry-After"))
			if e != nil || retry <= 0 || response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("Content-Type") != "application/problem+json" || response.Header.Get("X-Request-ID") == "" {
				t.Fatal("NATIVE_ACCOUNT_RECOVERY_ACTUAL_RATE_HEADERS_FAILED")
			}
		}
		if want == http.StatusNoContent {
			return nil
		}
		var envelope map[string]any
		if json.Unmarshal(body, &envelope) != nil {
			t.Fatal("NATIVE_ACCOUNT_RECOVERY_RESPONSE_INVALID")
		}
		if want >= 400 {
			return envelope
		}
		data, ok := envelope["data"].(map[string]any)
		if !ok {
			t.Fatal("NATIVE_ACCOUNT_RECOVERY_ENVELOPE_INVALID")
		}
		return data
	}
	write := func(audience, path string, payload any, want int) map[string]any {
		t.Helper()
		data := perform(audience, "GET", "/api/v1/auth/csrf", nil, "", nil, 200)
		token, ok := data["token"].(string)
		if !ok || token == "" {
			t.Fatal("NATIVE_ACCOUNT_RECOVERY_CSRF_MISSING")
		}
		return perform(audience, "POST", path, payload, token, nil, want)
	}
	for _, audience := range []string{"portal", "admin"} {
		write(audience, "/api/v1/auth/login", map[string]string{"email": "admin@example.invalid", "password": password}, 200)
	}
	oldCookies := map[string][]*http.Cookie{}
	for _, audience := range []string{"portal", "admin"} {
		u, _ := url.Parse(origin(audience))
		oldCookies[audience] = jar.Cookies(u)
		if len(oldCookies[audience]) == 0 {
			t.Fatal("NATIVE_ACCOUNT_RECOVERY_SESSION_MISSING")
		}
	}
	write("portal", "/api/v1/auth/password-reset-requests", map[string]string{"email": "admin@example.invalid"}, 202)
	view := *x.owner
	view.DB = x.f.PostgreSQL
	token := identityfixture.ResetToken(t, &view, "admin@example.invalid")
	write("portal", "/api/v1/auth/password-resets", map[string]string{"token": token, "password": newPassword}, 200)
	for _, audience := range []string{"portal", "admin"} {
		perform(audience, "GET", "/api/v1/me", nil, "", oldCookies[audience], 401)
	}
	write("admin", "/api/v1/auth/login", map[string]string{"email": "admin@example.invalid", "password": newPassword}, 200)
	write("admin", "/api/v1/admin/invitations", map[string]string{"email": "restart-recipient@example.invalid", "locale": "en"}, 201)
	rates := nativePrepareRateRestart(t, x.ctx, x.f.PostgreSQL, policy, x.owner.HMACKey, password, perform)
	pendingMail := nativeCapturePendingMail(t, x.ctx, x.f.PostgreSQL, x.mail.Keys, x.c.Server.PortalOrigin)
	stopInitial()
	rateBefore := nativeRateFingerprint(t, x.ctx, x.f.PostgreSQL)
	var pendingBefore int
	if x.f.PostgreSQL.QueryRow(x.ctx, "SELECT count(*) FROM mail_deliveries WHERE state IN ('queued','sending')").Scan(&pendingBefore) != nil || pendingBefore != len(pendingMail.tokens) || pendingBefore < 2 {
		t.Fatal("NATIVE_ACCOUNT_RECOVERY_PENDING_MAIL_REQUIRED")
	}
	owned.RestartNative(t, x.ctx)
	deadline := time.Now().Add(10 * time.Second)
	for x.f.PostgreSQL.Ping(x.ctx) != nil {
		if time.Now().After(deadline) {
			t.Fatal("NATIVE_ACCOUNT_RECOVERY_DATABASE_NOT_READY")
		}
		time.Sleep(50 * time.Millisecond)
	}
	var pendingAfter int
	var user string
	if x.f.PostgreSQL.QueryRow(x.ctx, "SELECT primary_admin_user_id::text,(SELECT count(*) FROM mail_deliveries WHERE state IN ('queued','sending')) FROM installation_state WHERE id=1").Scan(&user, &pendingAfter) != nil || user != x.f.User || pendingAfter != pendingBefore {
		t.Fatal("NATIVE_ACCOUNT_RECOVERY_DURABLE_STATE_CHANGED")
	}
	if database.Check(x.ctx, x.f.PostgreSQL) != nil {
		t.Fatal("NATIVE_ACCOUNT_RECOVERY_SCHEMA_CHANGED")
	}
	rateAfter := nativeRateFingerprint(t, x.ctx, x.f.PostgreSQL)
	if rateAfter != rateBefore {
		t.Fatal("NATIVE_ACCOUNT_RECOVERY_RATE_STATE_CHANGED_ACROSS_RESTART")
	}
	stopRestarted, restartedPID := liveCLIHTTPStart(t, x, native, configurationFile, role, "restarted")
	if restartedPID == initialPID {
		t.Fatal("NATIVE_ACCOUNT_RECOVERY_CONTROL_PID_REUSED")
	}
	rates.after = rates.limited(t)
	sink.SetTemporaryFailure(false)
	if _, e := x.f.PostgreSQL.Exec(x.ctx, "UPDATE mail_deliveries SET next_attempt_at=clock_timestamp()-interval '1 second' WHERE state='queued'"); e != nil {
		t.Fatal("NATIVE_ACCOUNT_RECOVERY_MAIL_CLOCK_FIXTURE_FAILED")
	}
	pendingMail.receive(t, sink)
	var sent, sentResets, sentTotal int
	deadline = time.Now().Add(5 * time.Second)
	for {
		if x.f.PostgreSQL.QueryRow(x.ctx, "SELECT (SELECT count(*) FROM mail_deliveries WHERE state='sent' AND purpose='invitation'),(SELECT count(*) FROM mail_deliveries WHERE state='sent' AND purpose='password_reset'),(SELECT count(*) FROM mail_deliveries WHERE state='sent')").Scan(&sent, &sentResets, &sentTotal) == nil && sent >= 1 && sentResets >= 1 && sentTotal >= pendingBefore {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("NATIVE_ACCOUNT_RECOVERY_BOTH_MAIL_STATES_NOT_COMMITTED")
		}
		time.Sleep(50 * time.Millisecond)
	}
	write("portal", "/api/v1/auth/password-resets", map[string]string{"token": pendingMail.resetToken, "password": newPassword}, 200)
	if write("portal", "/api/v1/auth/password-resets", map[string]string{"token": pendingMail.resetToken, "password": newPassword}, 400)["code"] != "TOKEN_UNUSABLE" {
		t.Fatal("NATIVE_ACCOUNT_RECOVERY_RECEIVED_RESET_REPLAY_ALLOWED")
	}
	rates.expireAndRecover(t)
	for _, audience := range []string{"portal", "admin"} {
		perform(audience, "GET", "/api/v1/me", nil, "", oldCookies[audience], 401)
		write(audience, "/api/v1/auth/login", map[string]string{"email": "admin@example.invalid", "password": password}, 401)
		write(audience, "/api/v1/auth/login", map[string]string{"email": "admin@example.invalid", "password": newPassword}, 200)
		perform(audience, "GET", "/api/v1/me", nil, "", nil, 200)
	}
	stopRestarted()
	result, _ := json.Marshal(map[string]any{"format": "projectone.native-account-postgres-restart.v2", "status": "passed_actual_native_control_postgres_restart_revocation_pending_mail_only", "pending_before": pendingBefore, "pending_after": pendingAfter, "sent_invitations": sent, "sent_resets": sentResets, "verified_pending_receipts": len(pendingMail.tokens), "recovered_reset_consumed_once": true, "rate_checks": rates.result(rateBefore, rateAfter), "same_user_id": true, "old_portal_session_rejected": true, "old_admin_session_rejected": true, "old_password_rejected": true, "new_password_succeeded": true, "native_control_pids": []int{initialPID, restartedPID}, "runtime_role": role, "api_port": port, "smtp_port": smtpPort})
	liveWriteBrowserEvidence(t, filepath.Join(input.ResultDirectory, "postgres-restart-result.json"), result)
	t.Log("NATIVE_ACCOUNT_POSTGRES_RESTART_ASSERTIONS actual_native_control_phases=2 actual_owned_postgres_restart=true same_account=true old_sessions_password_rejected=true new_password_valid=true pending_mail_received_and_committed=true")
	t.Log("NATIVE_ACCOUNT_POSTGRES_RATE_ASSERTIONS canonical_default_limits=true exact_counter_digest_persisted=true before_after_429_and_quiet202=true real_expiry_admission=true pending_invitation_reset_MIME_receipts=true")
}
