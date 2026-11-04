//go:build repository_integration && fullserve_browser_integration

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/cookiejar"
	stdmail "net/mail"
	"net/url"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.yaml.in/yaml/v3"
	"golang.org/x/term"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/databaseaccess"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/smtpsink"
	licensewire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func nativeRestoreSnapshot(t *testing.T, ctx context.Context, pool *pgxpool.Pool) string {
	t.Helper()
	// Full synthetic private rows are compared in memory only. Evidence receives
	// their digest, never credential hashes, tokens, recipient data or key bytes.
	var raw string
	e := pool.QueryRow(ctx, `SELECT jsonb_build_object(
		'users',(SELECT jsonb_agg(to_jsonb(u) ORDER BY id) FROM users u),
		'memberships',(SELECT jsonb_agg(to_jsonb(m) ORDER BY user_id,workspace_id) FROM workspace_memberships m),
		'audit',(SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM audit_events a),
		'migrations',(SELECT jsonb_agg(to_jsonb(v) ORDER BY version) FROM schema_migrations v))::text`).Scan(&raw)
	if e != nil {
		t.Fatal("NATIVE_ACCOUNT_RESTORE_SNAPSHOT_FAILED")
	}
	return liveBootstrapDigest([]byte(raw))
}

func nativeRestoreOperator(t *testing.T, x *serverCompositionStory, input nativeAccountRecoveryInput, command, configuration, expected string, extra ...string) map[string]any {
	t.Helper()
	if liveBrowserFileSHA(t, input.ControlFile) != input.ControlSHA256 {
		t.Fatal("NATIVE_ACCOUNT_RESTORE_OPERATOR_CHANGED")
	}
	ctx, cancel := context.WithTimeout(x.ctx, 15*time.Second)
	defer cancel()
	argv := append([]string{command, "--config", configuration}, extra...)
	child := exec.CommandContext(ctx, input.ControlFile, argv...)
	child.Env, child.WaitDelay = liveCLIEnvironment(), 2*time.Second
	stdout, stderr := &liveCLIProbeOutput{}, &liveCLIProbeOutput{}
	child.Stdout, child.Stderr = stdout, stderr
	e := child.Run()
	if child.ProcessState == nil || e != nil || ctx.Err() != nil || child.ProcessState.ExitCode() != 0 || stdout.String() != expected+"\n" || stderr.Len() != 0 {
		t.Fatal("NATIVE_ACCOUNT_RESTORE_OPERATOR_FAILED", command)
	}
	return map[string]any{"command": command, "pid": child.Process.Pid, "joined": true, "exit_code": 0, "accepted_static_output": expected, "configuration_sha256": liveBrowserFileSHA(t, configuration), "stdout_sha256": liveBootstrapDigest(stdout.Bytes()), "stderr_sha256": liveBootstrapDigest(stderr.Bytes())}
}

func nativeRestoreRecoverAdmin(t *testing.T, x *serverCompositionStory, input nativeAccountRecoveryInput, configuration, password string) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(x.ctx, 15*time.Second)
	defer cancel()
	master, slave, e := pty.Open()
	if e != nil {
		t.Fatal("NATIVE_ACCOUNT_RESTORE_PRIVATE_PTY_FAILED")
	}
	defer master.Close()
	defer slave.Close()
	// This is a new disposable slave, never the user's terminal. ReadPassword
	// restores this already raw/no-ECHO state between its two reads.
	if _, e = term.MakeRaw(int(slave.Fd())); e != nil {
		t.Fatal("NATIVE_ACCOUNT_RESTORE_NO_ECHO_REQUIRED")
	}
	if liveBrowserFileSHA(t, input.ControlFile) != input.ControlSHA256 {
		t.Fatal("NATIVE_ACCOUNT_RESTORE_OPERATOR_CHANGED")
	}
	child := exec.CommandContext(ctx, input.ControlFile, "recover-admin", "--config", configuration, "--reason", "Isolated synthetic restored administrator recovery")
	child.Env, child.WaitDelay = liveCLIEnvironment(), 2*time.Second
	child.Stdin, child.Stdout, child.Stderr = slave, slave, slave
	// No new session/group: the outer Go owner can cancel every descendant.
	if e = child.Start(); e != nil {
		t.Fatal("NATIVE_ACCOUNT_RESTORE_RECOVERY_START_FAILED")
	}
	joined := false
	defer func() {
		if !joined {
			cancel()
			_ = child.Wait()
		}
		_ = slave.Close()
		_ = master.Close()
	}()
	chunks := make(chan []byte, 8)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		defer close(chunks)
		buffer := make([]byte, 1024)
		for {
			n, e := master.Read(buffer)
			if n > 0 {
				raw := append([]byte(nil), buffer[:n]...)
				select {
				case chunks <- raw:
				case <-ctx.Done():
					return
				}
			}
			if e != nil {
				return
			}
		}
	}()
	defer func() {
		cancel()
		_ = slave.Close()
		_ = master.Close()
		if !joined {
			_ = child.Wait()
			joined = true
		}
		select {
		case <-readerDone:
		case <-time.After(2 * time.Second):
			t.Error("NATIVE_ACCOUNT_RESTORE_PTY_READER_NOT_JOINED")
		}
	}()
	var output []byte
	consume := func(raw []byte) {
		t.Helper()
		if len(output)+len(raw) > 8192 {
			t.Fatal("NATIVE_ACCOUNT_RESTORE_PTY_OUTPUT_LIMIT")
		}
		output = append(output, raw...)
		if bytes.Contains(output, []byte(password)) {
			t.Fatal("NATIVE_ACCOUNT_RESTORE_PASSWORD_ECHOED")
		}
	}
	for _, prompt := range []string{"Password: ", "Confirm password: "} {
		for !strings.Contains(string(output), prompt) {
			select {
			case raw, ok := <-chunks:
				if !ok {
					t.Fatal("NATIVE_ACCOUNT_RESTORE_PROMPT_MISSING")
				}
				consume(raw)
			case <-ctx.Done():
				t.Fatal("NATIVE_ACCOUNT_RESTORE_PROMPT_TIMEOUT")
			}
		}
		if _, e = io.WriteString(master, password+"\n"); e != nil {
			t.Fatal("NATIVE_ACCOUNT_RESTORE_PASSWORD_WRITE_FAILED")
		}
	}
	e = child.Wait()
	joined = true
	_ = slave.Close()
	for raw := range chunks {
		consume(raw)
	}
	if e != nil || ctx.Err() != nil || child.ProcessState == nil || child.ProcessState.ExitCode() != 0 || strings.ReplaceAll(string(output), "\r\n", "\n") != "Password: \nConfirm password: \nADMIN_RECOVERY_COMPLETE\n" {
		t.Fatal("NATIVE_ACCOUNT_RESTORE_TTY_RECOVERY_FAILED")
	}
	return map[string]any{"command": "recover-admin", "pid": child.Process.Pid, "joined": true, "exit_code": 0, "accepted_static_output": "ADMIN_RECOVERY_COMPLETE", "private_pty": true, "raw_before_child": true, "password_echoed": false, "prompt_count": 2, "transcript_sha256": liveBootstrapDigest(output)}
}

func TestActualNativeAccountPostgresRestore(t *testing.T) {
	input := nativeAccountInput(t, "postgres_restore")
	x := serverCompositionSetup(t, true)
	source := x.f.PostgreSQL
	_, port, e := net.SplitHostPort(x.c.Server.ListenAddress)
	if e != nil {
		t.Fatal("NATIVE_ACCOUNT_RESTORE_LISTENER_INVALID")
	}
	x.c.Server.TransportMode = config.TransportHTTP
	x.c.Server.PortalOrigin, x.c.Server.AdminOrigin = "http://localhost:"+port, "http://127.0.0.1:"+port
	x.c.Server.TLSCertificateFile, x.c.Server.TLSPrivateKeyFile = "", ""
	x.c.Server.PortalAssets, x.c.Server.AdminAssets = input.PortalAssets, input.AdminAssets
	sink := smtpsink.Start(t, false, false)
	host, smtpPort, e := net.SplitHostPort(sink.Listener.Addr().String())
	if e != nil {
		t.Fatal("NATIVE_ACCOUNT_RESTORE_SMTP_INVALID")
	}
	x.c.SMTP.Host, x.c.SMTP.CAFile = host, sink.CAFile
	x.c.SMTP.Port, _ = strconv.Atoi(smtpPort)
	scopeArtifact, _ := json.Marshal(map[string]any{"format": "projectone.native-account-restore-scope.v1", "api_port": port, "smtp_port": smtpPort})
	liveWriteBrowserEvidence(t, filepath.Join(input.ResultDirectory, "restore-scope.json"), scopeArtifact)
	x.owner.Config, x.mail.Config = x.c, x.c
	password, recoveredPassword := identityfixture.Password(), identityfixture.Password()
	if e = x.owner.Bootstrap(x.ctx, "admin@example.invalid", "Restore administrator", password, "Synthetic native restore", "Default"); e != nil {
		t.Fatal("NATIVE_ACCOUNT_RESTORE_BOOTSTRAP_FAILED")
	}
	if e = source.QueryRow(x.ctx, "SELECT tenant_id::text,default_workspace_id::text,primary_admin_user_id::text FROM installation_state WHERE id=1").Scan(&x.f.Tenant, &x.f.Workspace, &x.f.User); e != nil {
		t.Fatal("NATIVE_ACCOUNT_RESTORE_INSTALLATION_FAILED")
	}
	admin := identityfixture.Login(t, x.owner, "admin@example.invalid", password, "admin")
	state, e := x.h.Licensing.Service.StatusAdmin(x.ctx, admin)
	if e != nil {
		t.Fatal("NATIVE_ACCOUNT_RESTORE_LICENSE_FAILED")
	}
	now := time.Now().UTC().Truncate(time.Second)
	artifact, e := licensewire.Issue(licensewire.Claims{FormatVersion: 1, LicenseID: uuid.NewString(), CustomerReference: "Synthetic native restore", DeploymentID: state.DeploymentID, IssuedAt: now.Add(-time.Minute).Format(time.RFC3339), NotBefore: now.Add(-time.Minute).Format(time.RFC3339), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339), GraceUntil: now.Add(2 * time.Hour).Format(time.RFC3339), Scope: licensewire.WholePlatformScope}, x.key)
	if e != nil {
		t.Fatal("NATIVE_ACCOUNT_RESTORE_LICENSE_FAILED")
	}
	if _, e = x.h.Licensing.Service.Import(x.ctx, admin, licensing.ImportInput{Artifact: string(artifact), Reason: "Synthetic native restore"}, uuid.NewString()); e != nil {
		t.Fatal("NATIVE_ACCOUNT_RESTORE_LICENSE_FAILED")
	}
	oldSessions := map[string]string{}
	for _, audience := range []string{"portal", "admin"} {
		_, token, _, err := x.owner.Login(x.ctx, "admin@example.invalid", password, audience, "", uuid.NewString())
		if err != nil {
			t.Fatal("NATIVE_ACCOUNT_RESTORE_SEED_SESSION_FAILED")
		}
		oldSessions[audience] = token
	}
	invitation, e := x.owner.Invite(x.ctx, admin, "", "createInvitation", uuid.NewString(), "old-restore@example.invalid", "en", 0, uuid.NewString())
	if e != nil {
		t.Fatal("NATIVE_ACCOUNT_RESTORE_SEED_INVITATION_FAILED")
	}
	oldInvitation := identityfixture.InvitationToken(t, x.owner, invitation.ID)
	if e = x.owner.RequestReset(x.ctx, "admin@example.invalid", "portal", "", uuid.NewString()); e != nil {
		t.Fatal("NATIVE_ACCOUNT_RESTORE_SEED_RESET_FAILED")
	}
	oldReset := identityfixture.ResetToken(t, x.owner, "admin@example.invalid")
	before := nativeRestoreSnapshot(t, x.ctx, source)
	owned := identityfixture.OwnedNativePostgres(t, source, input.PostgresInput)
	owned.RegisterNativeSourceCleanup(t, source)
	archive := owned.DumpSchema(t, x.ctx)
	archiveFile := x.private(t, "native-account-backup.dump", archive)
	backupFacts, _ := json.Marshal(map[string]any{"format": "projectone.native-account-backup-facts.v1", "database": owned.Database, "schema": owned.Schema, "archive_sha256": liveBootstrapDigest(archive), "archive_bytes": len(archive), "state_sha256": before, "tenant_id": x.f.Tenant})
	backupFile := x.private(t, "native-account-backup-facts.json", backupFacts)
	restored := owned.RestoreFresh(t, x.ctx, source, archive, true)
	if e = database.Check(x.ctx, restored); e != nil || nativeRestoreSnapshot(t, x.ctx, restored) != before {
		t.Fatal("NATIVE_ACCOUNT_RESTORE_DATA_MISMATCH")
	}
	var version, initialAudits int
	if e = restored.QueryRow(x.ctx, "SELECT (SELECT max(version) FROM schema_migrations),(SELECT count(*) FROM audit_events)").Scan(&version, &initialAudits); e != nil {
		t.Fatal("NATIVE_ACCOUNT_RESTORE_BASELINE_FAILED")
	}
	raw, e := config.Secret(x.c.Database.DSNFile)
	if e != nil {
		t.Fatal("NATIVE_ACCOUNT_RESTORE_OPERATOR_INPUT_FAILED")
	}
	u, e := url.Parse(strings.TrimSpace(string(raw)))
	if e != nil || u.User == nil {
		t.Fatal("NATIVE_ACCOUNT_RESTORE_OPERATOR_INPUT_FAILED")
	}
	u.Path = "/" + restored.Config().ConnConfig.Database
	operatorConfig := x.c
	operatorConfig.Database.DSNFile = x.private(t, "restored-operator-dsn", []byte(u.String()))
	raw, e = yaml.Marshal(operatorConfig)
	if e != nil {
		t.Fatal("NATIVE_ACCOUNT_RESTORE_CONFIGURATION_FAILED")
	}
	operatorFile := x.private(t, "restored-operator.yaml", raw)
	role := "m01_browser_" + strings.TrimPrefix(owned.Schema, "m20_repository_")
	runtimePassword, e := databaseaccess.NewCredential()
	if e != nil {
		t.Fatal("NATIVE_ACCOUNT_RESTORE_RUNTIME_SECRET_FAILED")
	}
	u.User = url.UserPassword(role, runtimePassword)
	runtimeConfig := operatorConfig
	runtimeConfig.Database.DSNFile = x.private(t, "restored-runtime-dsn", []byte(u.String()))
	raw, e = yaml.Marshal(runtimeConfig)
	if e != nil {
		t.Fatal("NATIVE_ACCOUNT_RESTORE_CONFIGURATION_FAILED")
	}
	runtimeFile := x.private(t, "restored-runtime.yaml", raw)
	contextValue := release.RestoredRuntimeContext{FormatVersion: 1, RestoreID: strings.ReplaceAll(uuid.NewString(), "-", ""), SourceScopeID: strings.TrimPrefix(owned.Schema, "m20_repository_"), TargetScopeID: strings.TrimSuffix(strings.TrimPrefix(restored.Config().ConnConfig.Database, "m12_restore_"), "_test"), BackupManifestSHA256: liveBrowserFileSHA(t, backupFile), SQLArchiveSHA256: liveBrowserFileSHA(t, archiveFile), TenantID: x.f.Tenant, SchemaVersion: version, TargetDatabase: restored.Config().ConnConfig.Database, TargetOperator: restored.Config().ConnConfig.User, Reason: "Isolated actual native account logical restore"}
	if !contextValue.Valid() {
		t.Fatal("NATIVE_ACCOUNT_RESTORE_CONTEXT_INVALID")
	}
	raw, _ = json.Marshal(contextValue)
	contextFile := x.private(t, "restored-runtime-context.json", raw)
	if e = x.d.Close(); e != nil {
		t.Fatal("NATIVE_ACCOUNT_RESTORE_SOURCE_LEASE_RELEASE_FAILED")
	}
	commands := []map[string]any{}
	recordCommands := func() {
		raw, _ := json.Marshal(map[string]any{"format": "projectone.native-account-restore-operators.v1", "commands": commands})
		liveWriteBrowserEvidence(t, filepath.Join(input.ResultDirectory, "restore-operators.json"), raw)
	}
	for _, call := range []struct {
		command, status string
		extra           []string
	}{
		{"check-schema", "SCHEMA_CURRENT", nil},
		{"prepare-restored-runtime-access", databaseaccess.Verified, []string{"--runtime-config", runtimeFile, "--restore-context", contextFile}},
		{"check-runtime-access", databaseaccess.Verified, []string{"--runtime-config", runtimeFile}},
		{"invalidate-restored-auth", "RESTORED_AUTH_INVALIDATED", []string{"--reason", "Isolated actual native account restore invalidation"}},
	} {
		commands = append(commands, nativeRestoreOperator(t, x, input, call.command, operatorFile, call.status, call.extra...))
		recordCommands()
	}
	var liveSessions, liveInvitations, liveResets, pending, invalidAudits int
	if e = restored.QueryRow(x.ctx, `SELECT (SELECT count(*) FROM sessions WHERE revoked_at IS NULL),(SELECT count(*) FROM invitations WHERE status='pending'),(SELECT count(*) FROM password_reset_tokens WHERE consumed_at IS NULL AND revoked_at IS NULL),(SELECT count(*) FROM mail_deliveries WHERE state IN ('queued','sending')),(SELECT count(*) FROM audit_events WHERE action='operator.auth_invalidated')`).Scan(&liveSessions, &liveInvitations, &liveResets, &pending, &invalidAudits); e != nil || liveSessions+liveInvitations+liveResets+pending != 0 || invalidAudits != 1 {
		t.Fatal("NATIVE_ACCOUNT_RESTORE_OLD_AUTHORITY_REMAINED")
	}
	commands = append(commands, nativeRestoreRecoverAdmin(t, x, input, operatorFile, recoveredPassword))
	recordCommands()
	x.c, x.f.PostgreSQL = runtimeConfig, restored
	native := liveAccountBrowserInput{ApplicationProfile: input.ApplicationProfile, ControlFile: input.ControlFile, ControlSHA256: input.ControlSHA256, ResultDirectory: input.ResultDirectory, MaximumLogBytes: 1 << 20}
	stop, pid := liveCLIHTTPStart(t, x, native, runtimeFile, role, "initial")
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Transport: &http.Transport{}, Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	cookies, _ := httpserver.NewCookiePolicy(config.TransportHTTP)
	request := func(audience, method, path string, payload any, csrf, oldSession string, want int) map[string]any {
		t.Helper()
		origin := runtimeConfig.Server.PortalOrigin
		if audience == "admin" {
			origin = runtimeConfig.Server.AdminOrigin
		}
		var body []byte
		if payload != nil {
			body, _ = json.Marshal(payload)
		}
		r, err := http.NewRequestWithContext(x.ctx, method, origin+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal("NATIVE_ACCOUNT_RESTORE_REQUEST_INVALID")
		}
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", uuid.NewString())
		if csrf != "" {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		selected := client
		if oldSession != "" {
			selected = &http.Client{Transport: client.Transport, Timeout: client.Timeout}
			r.AddCookie(&http.Cookie{Name: cookies.Name(audience, httpserver.SessionCookie), Value: oldSession})
		}
		response, err := selected.Do(r)
		if err != nil {
			t.Fatal("NATIVE_ACCOUNT_RESTORE_HTTP_FAILED")
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		if err != nil || len(raw) == 1<<20 || response.StatusCode != want {
			t.Fatal("NATIVE_ACCOUNT_RESTORE_HTTP_STATUS_FAILED", path, want, response.StatusCode)
		}
		var envelope map[string]any
		if json.Unmarshal(raw, &envelope) != nil {
			t.Fatal("NATIVE_ACCOUNT_RESTORE_HTTP_JSON_FAILED")
		}
		if want >= 400 {
			return envelope
		}
		data, ok := envelope["data"].(map[string]any)
		if !ok {
			t.Fatal("NATIVE_ACCOUNT_RESTORE_HTTP_ENVELOPE_FAILED")
		}
		return data
	}
	mutate := func(audience, path string, payload any, want int) map[string]any {
		t.Helper()
		data := request(audience, "GET", "/api/v1/auth/csrf", nil, "", "", 200)
		token, ok := data["token"].(string)
		if !ok || token == "" {
			t.Fatal("NATIVE_ACCOUNT_RESTORE_CSRF_FAILED")
		}
		return request(audience, "POST", path, payload, token, "", want)
	}
	for _, audience := range []string{"portal", "admin"} {
		request(audience, "GET", "/api/v1/me", nil, "", oldSessions[audience], 401)
		mutate(audience, "/api/v1/auth/login", map[string]string{"email": "admin@example.invalid", "password": password}, 401)
		mutate(audience, "/api/v1/auth/login", map[string]string{"email": "admin@example.invalid", "password": recoveredPassword}, 200)
		request(audience, "GET", "/api/v1/me", nil, "", "", 200)
	}
	if problem := mutate("portal", "/api/v1/auth/invitation-validations", map[string]string{"token": oldInvitation}, 400); problem["code"] != "TOKEN_UNUSABLE" {
		t.Fatal("NATIVE_ACCOUNT_RESTORE_OLD_INVITATION_ERROR_MISMATCH")
	}
	if problem := mutate("portal", "/api/v1/auth/password-resets", map[string]string{"token": oldReset, "password": identityfixture.Password()}, 400); problem["code"] != "TOKEN_UNUSABLE" {
		t.Fatal("NATIVE_ACCOUNT_RESTORE_OLD_RESET_ERROR_MISMATCH")
	}
	newInvitation := mutate("admin", "/api/v1/admin/invitations", map[string]string{"email": "new-restore@example.invalid", "locale": "en"}, 201)
	if id, ok := newInvitation["id"].(string); !ok || id == "" {
		t.Fatal("NATIVE_ACCOUNT_RESTORE_NEW_INVITATION_MISSING")
	}
	var newToken string
	select {
	case message := <-sink.Messages:
		decoded, err := stdmail.ReadMessage(bytes.NewReader(message))
		if err != nil || decoded.Header.Get("To") != "new-restore@example.invalid" {
			t.Fatal("NATIVE_ACCOUNT_RESTORE_OLD_MAIL_RESUMED")
		}
		media, parameters, err := mime.ParseMediaType(decoded.Header.Get("Content-Type"))
		if err != nil || media != "multipart/alternative" {
			t.Fatal("NATIVE_ACCOUNT_RESTORE_MAIL_MIME_INVALID")
		}
		part, err := multipart.NewReader(decoded.Body, parameters["boundary"]).NextPart()
		if err != nil || part.Header.Get("Content-Transfer-Encoding") != "base64" {
			t.Fatal("NATIVE_ACCOUNT_RESTORE_MAIL_MIME_INVALID")
		}
		body, err := io.ReadAll(io.LimitReader(base64.NewDecoder(base64.StdEncoding, part), 65537))
		_ = part.Close()
		if err != nil || len(body) > 65536 {
			t.Fatal("NATIVE_ACCOUNT_RESTORE_MAIL_BODY_INVALID")
		}
		for _, field := range strings.Fields(string(body)) {
			if strings.HasPrefix(field, runtimeConfig.Server.PortalOrigin+"/register#token=") {
				if newToken != "" {
					t.Fatal("NATIVE_ACCOUNT_RESTORE_MAIL_LINK_AMBIGUOUS")
				}
				newToken = strings.TrimPrefix(field, runtimeConfig.Server.PortalOrigin+"/register#token=")
			}
		}
		if newToken == "" {
			t.Fatal("NATIVE_ACCOUNT_RESTORE_MAIL_LINK_MISSING")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("NATIVE_ACCOUNT_RESTORE_NEW_MAIL_NOT_DELIVERED")
	}
	mutate("portal", "/api/v1/auth/invitation-validations", map[string]string{"token": newToken}, 200)
	newUserPassword := identityfixture.Password()
	mutate("portal", "/api/v1/auth/register", map[string]string{"token": newToken, "display_name": "Restored synthetic member", "password": newUserPassword}, 201)
	mutate("portal", "/api/v1/auth/login", map[string]string{"email": "new-restore@example.invalid", "password": newUserPassword}, 200)
	request("portal", "GET", "/api/v1/me", nil, "", "", 200)
	stop()
	var finalAudits, recoveryAudits, sentNew, cancelledOld int
	if e = restored.QueryRow(x.ctx, `SELECT (SELECT count(*) FROM audit_events),(SELECT count(*) FROM audit_events WHERE action='operator.admin_recovered'),(SELECT count(*) FROM mail_deliveries WHERE recipient_email='new-restore@example.invalid' AND state='sent'),(SELECT count(*) FROM mail_deliveries WHERE state='cancelled' AND error_category='restore_invalidated')`).Scan(&finalAudits, &recoveryAudits, &sentNew, &cancelledOld); e != nil || finalAudits <= initialAudits || recoveryAudits != 1 || sentNew != 1 || cancelledOld != 2 {
		t.Fatal("NATIVE_ACCOUNT_RESTORE_FINAL_STATE_FAILED")
	}
	result, _ := json.Marshal(map[string]any{"format": "projectone.native-account-postgres-restore.v1", "status": "passed_actual_native_account_logical_restore_operators_only", "archive_bytes": len(archive), "archive_sha256": liveBootstrapDigest(archive), "backup_facts_sha256": liveBootstrapDigest(backupFacts), "source_restored_state_sha256": before, "schema_version": version, "old_sessions_links_pending_invalidated": true, "operator_invalidation_audits": invalidAudits, "operator_recovery_audits": recoveryAudits, "initial_audits": initialAudits, "final_audits": finalAudits, "cancelled_old_mail": cancelledOld, "sent_new_invitation": sentNew, "new_invitation_registration_login": true, "native_control_pid": pid, "runtime_role": role, "api_port": port, "smtp_port": smtpPort})
	liveWriteBrowserEvidence(t, filepath.Join(input.ResultDirectory, "postgres-restore-result.json"), result)
	t.Log("NATIVE_ACCOUNT_POSTGRES_RESTORE_ASSERTIONS actual_custom_dump_fresh_restore=true users_memberships_audit_migrations_equal=true native_operators=5 old_authority_rejected=true actual_TTY_admin_recovery=true new_TLS_mail_registration_login=true")
}
