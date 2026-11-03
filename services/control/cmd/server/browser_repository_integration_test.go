//go:build repository_integration && fullserve_browser_integration

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"go.yaml.in/yaml/v3"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/databaseaccess"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/smtpsink"
	strict "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	licensewire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
)

// This is a protected test input, not a serving configuration authority. The
// Python owner admits assets/browser/SQL and owns every process/profile/port.
type liveAccountBrowserInput struct {
	ApplicationProfile   string            `json:"application_profile,omitempty"`
	OfflineSandboxFile   string            `json:"offline_sandbox_file,omitempty"`
	OfflineSandboxSHA256 string            `json:"offline_sandbox_sha256,omitempty"`
	Format               string            `json:"format"`
	EndpointFile         string            `json:"endpoint_file"`
	NodeFile             string            `json:"node_file"`
	NodeSHA256           string            `json:"node_sha256"`
	ControlFile          string            `json:"control_file"`
	ControlSHA256        string            `json:"control_sha256"`
	DriverFile           string            `json:"driver_file"`
	DriverSHA256         string            `json:"driver_sha256"`
	CasesFile            string            `json:"cases_file"`
	CasesSHA256          string            `json:"cases_sha256"`
	PortalAssets         string            `json:"portal_assets"`
	AdminAssets          string            `json:"admin_assets"`
	Artifacts            map[string]string `json:"artifacts"`
	ResultDirectory      string            `json:"result_directory"`
	RequestMilliseconds  int               `json:"request_milliseconds"`
	DriverSeconds        int               `json:"driver_seconds"`
	MaximumLogBytes      int64             `json:"maximum_log_bytes"`
}

func liveBrowserFileSHA(t *testing.T, path string) string {
	t.Helper()
	canonical, e := filepath.EvalSymlinks(path)
	if e != nil || !filepath.IsAbs(path) || canonical != path {
		t.Fatal("LIVE_BROWSER_CANONICAL_FILE_REQUIRED")
	}
	f, e := os.Open(path)
	if e != nil {
		t.Fatal("LIVE_BROWSER_FILE_UNAVAILABLE")
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0022 != 0 || st.Size() > 512<<20 {
		t.Fatal("LIVE_BROWSER_BOUNDED_REGULAR_FILE_REQUIRED")
	}
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		t.Fatal("LIVE_BROWSER_FILE_HASH_FAILED")
	}
	return hex.EncodeToString(h.Sum(nil))
}

func liveBrowserInput(t *testing.T) liveAccountBrowserInput {
	t.Helper()
	inputFile := os.Getenv("PROJECT_ONE_FULLSERVE_BROWSER_INPUT")
	parent := filepath.Dir(inputFile)
	state := os.Getenv("PROJECT_ONE_STATE_ROOT")
	if !filepath.IsAbs(state) || filepath.Dir(parent) != filepath.Join(state, "evidence", "m20") || !strings.HasPrefix(filepath.Base(parent), "browser-trust-session-fullserve-account-") || filepath.Base(inputFile) != "browser-input.json" {
		t.Fatal("LIVE_BROWSER_EXCLUSIVE_RECEIPT_SCOPE_REQUIRED")
	}
	raw, e := config.Secret(inputFile)
	if e != nil || len(raw) > 1<<20 {
		t.Fatal("PROTECTED_LIVE_BROWSER_INPUT_REQUIRED")
	}
	var input liveAccountBrowserInput
	if strict.DecodeStrict(raw, &input) != nil || input.Format != "projectone.fullserve.account-browser-input.v2" || input.RequestMilliseconds < 1000 || input.RequestMilliseconds > 15000 || input.DriverSeconds < 30 || input.DriverSeconds > 75 || input.MaximumLogBytes < 4096 || input.MaximumLogBytes > 1<<20 || len(input.Artifacts) < 2 || len(input.Artifacts) > 100 {
		t.Fatal("LIVE_BROWSER_INPUT_INVALID")
	}
	if input.ResultDirectory != filepath.Join(parent, "browser-evidence") || input.EndpointFile != filepath.Join(parent, "private-session", "profile", "DevToolsActivePort") {
		t.Fatal("LIVE_BROWSER_EXACT_OUTPUT_AND_PROFILE_REQUIRED")
	}
	if entries, e := os.ReadDir(input.ResultDirectory); e != nil || len(entries) != 0 {
		t.Fatal("LIVE_BROWSER_EXCLUSIVE_EMPTY_RESULT_REQUIRED")
	}
	for path, digest := range map[string]string{input.NodeFile: input.NodeSHA256, input.DriverFile: input.DriverSHA256, input.CasesFile: input.CasesSHA256, input.ControlFile: input.ControlSHA256} {
		if liveBrowserFileSHA(t, path) != digest {
			t.Fatal("LIVE_BROWSER_EXECUTABLE_INPUT_CHANGED")
		}
	}
	if filepath.Base(input.ControlFile) != "control-native" || filepath.Dir(filepath.Dir(input.ControlFile)) != filepath.Join(state, "evidence", "m20") {
		t.Fatal("LIVE_BROWSER_MEASURED_NATIVE_CONTROL_SCOPE_REQUIRED")
	}
	for _, path := range []string{input.PortalAssets, input.AdminAssets, input.ResultDirectory} {
		canonical, e := filepath.EvalSymlinks(path)
		st, statError := os.Stat(path)
		if e != nil || statError != nil || canonical != path || !filepath.IsAbs(path) || !st.IsDir() || st.Mode().Perm()&0022 != 0 {
			t.Fatal("LIVE_BROWSER_OWNED_DIRECTORY_REQUIRED")
		}
	}
	if st, e := os.Stat(input.ResultDirectory); e != nil || st.Mode().Perm() != 0700 {
		t.Fatal("LIVE_BROWSER_PRIVATE_RESULT_REQUIRED")
	}
	for name, digest := range input.Artifacts {
		parts := strings.SplitN(name, "/", 2)
		if len(parts) != 2 || filepath.Clean(name) != name || filepath.IsAbs(name) || strings.HasPrefix(parts[1], "../") {
			t.Fatal("LIVE_BROWSER_ASSET_NAME_INVALID")
		}
		root := input.PortalAssets
		if parts[0] == "admin-dist" {
			root = input.AdminAssets
		} else if parts[0] != "portal-dist" {
			t.Fatal("LIVE_BROWSER_ASSET_OWNER_INVALID")
		}
		if liveBrowserFileSHA(t, filepath.Join(root, parts[1])) != digest {
			t.Fatal("LIVE_BROWSER_BUILT_ASSET_CHANGED")
		}
	}
	if input.OfflineSandboxFile != "" || input.OfflineSandboxSHA256 != "" {
		policy, e := config.Secret(input.OfflineSandboxFile)
		if e != nil || string(policy) != "(version 1)\n(allow default)\n(deny network-outbound)\n(allow network-outbound (remote ip \"localhost:*\"))\n(allow network-outbound (remote unix-socket))\n" || liveBrowserFileSHA(t, input.OfflineSandboxFile) != input.OfflineSandboxSHA256 {
			t.Fatal("LIVE_BROWSER_FIXED_OFFLINE_POLICY_REQUIRED")
		}
	}
	return input
}

// The native owner prechecks/records this exact disposable role before launch.
// The fixture operator remains separate for setup and final readback only.
func liveRestrictedPostgres(t *testing.T, x *serverCompositionStory) string {
	t.Helper()
	if x.f.PostgreSQL == nil || x.d.postgres == nil || x.d.pgLease == nil {
		t.Fatal("LIVE_BROWSER_ACTUAL_POSTGRES_REQUIRED")
	}
	schema := x.f.PostgreSQL.Config().ConnConfig.RuntimeParams["search_path"]
	if len(schema) != len("m20_repository_")+32 || !strings.HasPrefix(schema, "m20_repository_") {
		t.Fatal("LIVE_BROWSER_EXACT_SCHEMA_REQUIRED")
	}
	if _, e := hex.DecodeString(strings.TrimPrefix(schema, "m20_repository_")); e != nil {
		t.Fatal("LIVE_BROWSER_EXACT_SCHEMA_REQUIRED")
	}
	role := "m01_browser_" + strings.TrimPrefix(schema, "m20_repository_")
	var exists bool
	if e := x.f.PostgreSQL.QueryRow(x.ctx, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)", role).Scan(&exists); e != nil || exists {
		t.Fatal("LIVE_BROWSER_FRESH_RUNTIME_ROLE_REQUIRED")
	}
	password, e := databaseaccess.NewCredential()
	if e != nil {
		t.Fatal("LIVE_BROWSER_RUNTIME_CREDENTIAL_FAILED")
	}
	raw, e := config.Secret(x.c.Database.DSNFile)
	if e != nil {
		t.Fatal("LIVE_BROWSER_PRIVATE_OPERATOR_INPUT_REQUIRED")
	}
	u, e := url.Parse(strings.TrimSpace(string(raw)))
	if e != nil || u.User == nil || u.Query().Get("sslmode") != "verify-full" || u.Query().Get("search_path") != schema || u.Query().Get("sslrootcert") == "" {
		t.Fatal("LIVE_BROWSER_EXACT_RUNTIME_TLS_REQUIRED")
	}
	u.User = url.UserPassword(role, password)
	runtimeConfig := x.c
	runtimeConfig.Database.DSNFile = x.private(t, "runtime-postgres-dsn", []byte(u.String()))
	raw, e = yaml.Marshal(runtimeConfig)
	if e != nil {
		t.Fatal("LIVE_BROWSER_RUNTIME_CONFIGURATION_FAILED")
	}
	input, e := databaseaccess.LoadRuntime(x.private(t, "runtime-postgres.yaml", raw))
	if e != nil {
		t.Fatal("LIVE_BROWSER_PROTECTED_RUNTIME_INPUT_FAILED", e)
	}
	var oid uint32
	for i := range 2 {
		if status, e := databaseaccess.Prepare(x.ctx, x.f.PostgreSQL, x.d.pgLease, input); e != nil || status != databaseaccess.Verified {
			t.Fatal("LIVE_BROWSER_ACTUAL_RUNTIME_PREPARATION_FAILED", e)
		}
		var current uint32
		if e = x.f.PostgreSQL.QueryRow(x.ctx, "SELECT oid FROM pg_roles WHERE rolname=$1", role).Scan(&current); e != nil || current == 0 || i > 0 && oid != current {
			t.Fatal("LIVE_BROWSER_RUNTIME_ROLE_IDENTITY_CHANGED")
		}
		oid = current
	}
	if status, e := databaseaccess.Check(x.ctx, x.f.PostgreSQL, x.d.pgLease, input); e != nil || status != databaseaccess.Verified {
		t.Fatal("LIVE_BROWSER_ACTUAL_RUNTIME_CHECK_FAILED", e)
	}
	if e = x.d.Close(); e != nil {
		t.Fatal("LIVE_BROWSER_OPERATOR_POOL_CLOSE_FAILED")
	}
	next, e := openSelectedDatabase(x.ctx, runtimeConfig, "serve")
	if e != nil {
		t.Fatal("LIVE_BROWSER_RESTRICTED_SELECTED_OPEN_FAILED", e)
	}
	previous := x.owner
	x.c, x.d = runtimeConfig, next
	x.owner = x.d.identity(x.c)
	x.owner.HMACKey, x.owner.Passwords = previous.HMACKey, previous.Passwords
	x.mail.DB, x.mail.Config = x.d.postgres, x.c
	x.owner.Mail = x.mail
	x.h = identityhttp.New(x.owner)
	if e = x.h.InitializationError(); e != nil {
		t.Fatal("LIVE_BROWSER_RESTRICTED_HTTP_CONSTRUCTION_FAILED")
	}
	liveRuntimeIdentity(t, x, role)
	for _, statement := range []string{"CREATE TABLE forbidden_browser_runtime(id integer)", "TRUNCATE users", "UPDATE schema_migrations SET checksum=checksum WHERE false", "ALTER TABLE users DISABLE TRIGGER ALL"} {
		_, e := x.d.postgres.Exec(x.ctx, statement)
		var permission *pgconn.PgError
		if !errors.As(e, &permission) || permission.Code != "42501" {
			t.Fatal("LIVE_BROWSER_REAL_RUNTIME_PERMISSION_REFUSAL_REQUIRED")
		}
	}
	if e = database.Check(x.ctx, x.d.postgres, x.c.Server.Profile()); e != nil {
		t.Fatal("LIVE_BROWSER_REFUSAL_CHANGED_SCHEMA")
	}
	t.Log("LIVE_BROWSER_RUNTIME_ROLE_ASSERTIONS protected_prepare_repeat_check=true separate_TLS_login=true DDL_TRUNCATE_journal_trigger_refusals=4")
	return role
}

func liveRuntimeIdentity(t *testing.T, x *serverCompositionStory, role string) {
	t.Helper()
	var user, session string
	var elevated, membership, encrypted bool
	if e := x.d.postgres.QueryRow(x.ctx, `SELECT current_user::text,session_user::text,
	 (SELECT rolsuper OR rolinherit OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls OR NOT rolcanlogin OR rolconnlimit<>$1 FROM pg_roles WHERE rolname=current_user),
	 EXISTS(SELECT 1 FROM pg_auth_members WHERE member=(SELECT oid FROM pg_roles WHERE rolname=current_user) OR roleid=(SELECT oid FROM pg_roles WHERE rolname=current_user)),
	 (SELECT ssl FROM pg_stat_ssl WHERE pid=pg_backend_pid())`, x.c.Database.MaxOpenConnections).Scan(&user, &session, &elevated, &membership, &encrypted); e != nil || user != role || session != role || elevated || membership || !encrypted {
		t.Fatal("LIVE_BROWSER_ACTUAL_RESTRICTED_TLS_IDENTITY_REQUIRED")
	}
}

func TestActualFullServeHTTPAccountBrowser(t *testing.T) {
	liveActualAccountBrowser(t, false)
}

// The fixed pagination root shares real serving custody; the original24 journey remains unchanged.
func liveActualAccountBrowser(t *testing.T, pagination bool) {
	input := liveBrowserInput(t)
	x := serverCompositionSetup(t, true)
	if input.ApplicationProfile == "account" || input.ApplicationProfile == "workspaces" {
		liveAccountOnlySchema(t, x, input)
	} else if input.ApplicationProfile != "" && input.ApplicationProfile != "platform" {
		t.Fatal("LIVE_BROWSER_APPLICATION_PROFILE_INVALID")
	}
	if pagination {
		policy := x.c.Policy()
		policy.UI.PageSize = livePaginationPageSize
		x.c.Runtime = &policy
	}
	_, port, e := net.SplitHostPort(x.c.Server.ListenAddress)
	if e != nil {
		t.Fatal(e)
	}
	x.c.Server.TransportMode = config.TransportHTTP
	x.c.Server.PortalOrigin = "http://localhost:" + port
	x.c.Server.AdminOrigin = "http://127.0.0.1:" + port
	x.c.Server.TLSCertificateFile, x.c.Server.TLSPrivateKeyFile = "", ""
	x.c.Server.PortalAssets, x.c.Server.AdminAssets = input.PortalAssets, input.AdminAssets
	sink := smtpsink.Start(t, false, false)
	host, smtpPort, _ := net.SplitHostPort(sink.Listener.Addr().String())
	x.c.SMTP.Host, x.c.SMTP.CAFile = host, sink.CAFile
	fmtPort, e := strconv.Atoi(smtpPort)
	if e != nil {
		t.Fatal(e)
	}
	x.c.SMTP.Port = fmtPort
	x.owner.Config, x.mail.Config = x.c, x.c
	operatorConfiguration := x.c
	runtimeRole := liveRestrictedPostgres(t, x)
	password := liveNativeBootstrap(t, x, input, operatorConfiguration)
	if e = x.f.PostgreSQL.QueryRow(x.ctx, "SELECT tenant_id::text,default_workspace_id::text,primary_admin_user_id::text FROM installation_state WHERE id=1").Scan(&x.f.Tenant, &x.f.Workspace, &x.f.User); e != nil {
		t.Fatal("LIVE_BROWSER_BOOTSTRAP_IDENTITY_FAILED")
	}
	a := identityfixture.Login(t, x.owner, "admin@example.invalid", password, "admin")
	state, e := x.h.Licensing.Service.StatusAdmin(x.ctx, a)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC().Truncate(time.Second)
	artifact, e := licensewire.Issue(licensewire.Claims{FormatVersion: 1, LicenseID: uuid.NewString(), CustomerReference: "Synthetic live browser fixture", DeploymentID: state.DeploymentID, IssuedAt: now.Add(-time.Minute).Format(time.RFC3339), NotBefore: now.Add(-time.Minute).Format(time.RFC3339), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339), GraceUntil: now.Add(2 * time.Hour).Format(time.RFC3339), Scope: licensewire.WholePlatformScope}, x.key)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = x.h.Licensing.Service.Import(x.ctx, a, licensing.ImportInput{Artifact: string(artifact), Reason: "Synthetic isolated actual browser license"}, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	var paging *livePaginationFixture
	if pagination {
		paging = liveSeedPagination(t, x)
	}
	inbox := filepath.Join(x.root, "inbox")
	if e = os.Mkdir(inbox, 0700); e != nil {
		t.Fatal(e)
	}
	mailContext, stopMailbox := context.WithCancel(x.ctx)
	mailDone := make(chan struct{})
	var received atomic.Int64
	go func() {
		defer close(mailDone)
		for {
			select {
			case <-mailContext.Done():
				return
			case raw := <-sink.Messages:
				name := strconv.FormatInt(time.Now().UnixNano(), 10) + "-" + uuid.NewString() + ".eml"
				if e := os.WriteFile(filepath.Join(inbox, name), raw, 0600); e != nil {
					return
				}
				received.Add(1)
			}
		}
	}()
	t.Cleanup(func() { stopMailbox(); <-mailDone })
	rawConfiguration, e := yaml.Marshal(x.c)
	if e != nil {
		t.Fatal("LIVE_BROWSER_NATIVE_CONFIGURATION_FAILED")
	}
	configuration := x.private(t, "native-serving.yaml", rawConfiguration)
	if e = x.d.Close(); e != nil {
		t.Fatal("LIVE_BROWSER_NATIVE_INSTALLATION_LEASE_RELEASE_FAILED")
	}
	stopServing, firstPID := liveCLIHTTPStart(t, x, input, configuration, runtimeRole, "initial")
	projection, e := json.Marshal(map[string]any{"transport": "http", "portal": x.c.Server.PortalOrigin, "admin": x.c.Server.AdminOrigin, "listen_address": x.c.Server.ListenAddress, "fixture": "native_bootstrap_and_serve_CLI_separate_restricted_runtime_role_privileged_migration_and_final_readback"})
	if e != nil {
		t.Fatal("LIVE_BROWSER_PROJECTION_WRITE_FAILED")
	}
	liveWriteBrowserEvidence(t, filepath.Join(input.ResultDirectory, "server-projection.json"), projection)
	restartRequest, restartComplete := filepath.Join(x.root, "restart.request"), filepath.Join(x.root, "restart.complete")
	manifest := map[string]any{"portal": x.c.Server.PortalOrigin, "admin": x.c.Server.AdminOrigin, "admin_email": "admin@example.invalid", "admin_password": password, "member_email": "alice@example.invalid", "member_password": identityfixture.Password(), "new_password": identityfixture.Password(), "inbox": inbox, "output": input.ResultDirectory, "endpoint_file": input.EndpointFile, "request_milliseconds": input.RequestMilliseconds, "cases_file": input.CasesFile, "restart_request": restartRequest, "restart_complete": restartComplete}
	if pagination {
		manifest["pagination"] = paging
		manifest["member_email"], manifest["member_password"] = livePaginationMemberEmail, password
	}
	raw, e := json.Marshal(manifest)
	if e != nil {
		t.Fatal(e)
	}
	manifestFile := x.private(t, "live-browser.json", raw)
	driverContext, stopDriver := context.WithTimeout(x.ctx, config.Seconds(input.DriverSeconds))
	defer stopDriver()
	command := exec.CommandContext(driverContext, input.NodeFile, input.DriverFile, manifestFile)
	command.WaitDelay = 2 * time.Second
	command.Env = append(os.Environ(), "NODE_OPTIONS=--max-old-space-size=128")
	log, e := os.OpenFile(filepath.Join(input.ResultDirectory, "driver.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer log.Close()
	writer := &liveBrowserBoundedLog{file: log, remaining: input.MaximumLogBytes}
	command.Stdout, command.Stderr = writer, writer
	driverDone := make(chan error, 1)
	go func() { driverDone <- command.Run() }()
	driverReturned := false
	t.Cleanup(func() {
		stopDriver()
		if !driverReturned {
			select {
			case <-driverDone:
			case <-time.After(5 * time.Second):
				t.Error("LIVE_BROWSER_DRIVER_JOIN_TIMEOUT")
			}
		}
	})
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	restarted := false
driverLoop:
	for {
		select {
		case e := <-driverDone:
			driverReturned = true
			if e != nil {
				t.Fatal("LIVE_BROWSER_DRIVER_FAILED_SEE_PRIVATE_REDACTED_RESULT")
			}
			break driverLoop
		case <-ticker.C:
			if restarted {
				continue
			}
			request, readError := os.ReadFile(restartRequest)
			if os.IsNotExist(readError) {
				continue
			}
			if readError != nil || string(request) != "restart\n" {
				t.Fatal("LIVE_BROWSER_RESTART_REQUEST_INVALID")
			}
			stopServing()
			var nextPID int
			stopServing, nextPID = liveCLIHTTPStart(t, x, input, configuration, runtimeRole, "restarted")
			if nextPID == firstPID {
				t.Fatal("LIVE_BROWSER_NATIVE_RESTART_REUSED_PID")
			}
			if e = os.WriteFile(restartComplete, []byte("{\"status\":\"restarted_same_selected_database\"}\n"), 0600); e != nil {
				t.Fatal(e)
			}
			restarted = true
		}
	}
	stopServing()
	x.d, e = openSelectedDatabase(x.ctx, x.c, "serve")
	if e != nil {
		t.Fatal("LIVE_BROWSER_NATIVE_FINAL_RESTRICTED_OPEN_FAILED")
	}
	liveRuntimeIdentity(t, x, runtimeRole)
	if pagination {
		if !restarted || log.Sync() != nil {
			t.Fatal("LIVE_PAGINATION_RESTART_OR_LOG_INCOMPLETE")
		}
		liveReadbackPagination(t, x, input, paging)
		return
	}
	if !restarted || received.Load() < 4 || log.Sync() != nil {
		t.Fatal("LIVE_BROWSER_RESTART_OR_ACTUAL_TLS_MAIL_UNPROVEN")
	}
	var users, disabled, enabled, revoked, sent int64
	var locale, display, stateName string
	if e = x.f.PostgreSQL.QueryRow(x.ctx, "SELECT count(*) FROM users").Scan(&users); e != nil || users != 3 {
		t.Fatal("LIVE_BROWSER_ACTUAL_ACCOUNT_COUNT_INVALID")
	}
	if e = x.f.PostgreSQL.QueryRow(x.ctx, "SELECT locale,display_name,status FROM users WHERE email_canonical='alice@example.invalid'").Scan(&locale, &display, &stateName); e != nil || !reflect.DeepEqual([]string{locale, display, stateName}, []string{"zh-CN", "Alice updated", "active"}) {
		t.Fatal("LIVE_BROWSER_ACTUAL_PROFILE_NOT_PERSISTED")
	}
	if e = x.f.PostgreSQL.QueryRow(x.ctx, "SELECT count(*) FILTER(WHERE action='account.disabled'),count(*) FILTER(WHERE action='account.enabled') FROM audit_events").Scan(&disabled, &enabled); e != nil || disabled < 1 || enabled < 1 {
		t.Fatal("LIVE_BROWSER_ACTUAL_STATE_AUDIT_MISSING")
	}
	if e = x.f.PostgreSQL.QueryRow(x.ctx, "SELECT count(*) FROM sessions WHERE revoked_at IS NOT NULL").Scan(&revoked); e != nil || revoked < 4 {
		t.Fatal("LIVE_BROWSER_ACTUAL_REVOCATION_MISSING")
	}
	if e = x.f.PostgreSQL.QueryRow(x.ctx, "SELECT count(*) FROM mail_deliveries WHERE state='sent'").Scan(&sent); e != nil || sent < 4 {
		t.Fatal("LIVE_BROWSER_ACTUAL_MAIL_RECEIPTS_MISSING")
	}
	t.Logf("LIVE_BROWSER_SQL_ASSERTIONS users=%d state_audits=%d/%d revoked=%d sent=%d received=%d same_database_restart=true restricted_role_after_restart=true actual_native_CLI_processes=2", users, disabled, enabled, revoked, sent, received.Load())
}

type liveBrowserBoundedLog struct {
	file      *os.File
	remaining int64
}

func (w *liveBrowserBoundedLog) Write(raw []byte) (int, error) {
	if int64(len(raw)) > w.remaining {
		return 0, io.ErrShortWrite
	}
	w.remaining -= int64(len(raw))
	return w.file.Write(raw)
}
