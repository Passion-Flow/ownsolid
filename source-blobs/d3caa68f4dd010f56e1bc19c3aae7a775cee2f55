//go:build repository_integration

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/localization"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/mail"
)

const (
	startupChildConfiguration    = "PROJECT_ONE_TEST_STARTUP_CHILD_CONFIGURATION"
	startupChildConfigurationSHA = "PROJECT_ONE_TEST_STARTUP_CHILD_CONFIGURATION_SHA256"
	startupChildSeconds          = 10
	startupOutputBytes           = 4096
)

// This identified test child enters the actual command boundary, including its
// fixed stderr/exit behavior. It is not a separately built product executable.
func TestM01CurrentStartupChild(t *testing.T) {
	file := os.Getenv(startupChildConfiguration)
	if file == "" {
		t.Skip("fixed startup child only")
	}
	relative, e := filepath.Rel(os.TempDir(), file)
	if e != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) || !strings.HasPrefix(filepath.Base(file), "startup-input-") {
		t.Fatal("STARTUP_CHILD_PRIVATE_SCOPE_REQUIRED")
	}
	if _, e = config.LoadProtected(file); e != nil {
		t.Fatal("STARTUP_CHILD_PROTECTED_CONFIGURATION_REQUIRED")
	}
	raw, e := os.ReadFile(file)
	if e != nil || len(raw) > config.MaximumProtectedConfigurationBytes {
		t.Fatal("STARTUP_CHILD_CONFIGURATION_READ_FAILED")
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != os.Getenv(startupChildConfigurationSHA) {
		t.Fatal("STARTUP_CHILD_CONFIGURATION_CHANGED")
	}
	os.Args = []string{"control", "serve", "--config", file}
	main()
	t.Fatal("STARTUP_CHILD_UNEXPECTED_COMMAND_SUCCESS")
}

type startupBoundedOutput struct {
	buffer   bytes.Buffer
	overflow bool
}

func (w *startupBoundedOutput) Write(raw []byte) (int, error) {
	length := len(raw)
	if remaining := startupOutputBytes - w.buffer.Len(); len(raw) > remaining {
		w.overflow = true
		raw = raw[:remaining]
	}
	_, _ = w.buffer.Write(raw)
	// Keep draining both pipes even after the retained diagnostic bound is hit.
	// An overflow still fails the refusal assertion after the child is joined.
	return length, nil
}

type startupExecutable struct{ path, digest string }

func startupMeasuredExecutable(t *testing.T) startupExecutable {
	t.Helper()
	path, e := os.Executable()
	if e != nil {
		t.Fatal("STARTUP_MEASURED_TEST_EXECUTABLE_REQUIRED")
	}
	file, e := os.Open(path)
	if e != nil {
		t.Fatal("STARTUP_MEASURED_TEST_EXECUTABLE_UNREADABLE")
	}
	digest := sha256.New()
	_, readError := io.Copy(digest, file)
	if closeError := file.Close(); readError != nil || closeError != nil {
		t.Fatal("STARTUP_MEASURED_TEST_EXECUTABLE_HASH_FAILED")
	}
	return startupExecutable{path, hex.EncodeToString(digest.Sum(nil))}
}

func startupBusinessDigest(t *testing.T, x *serverCompositionStory) string {
	t.Helper()
	digest := sha256.New()
	for _, table := range []string{"installation_state", "tenants", "workspaces", "users", "workspace_memberships", "password_credentials", "sessions", "invitations", "password_reset_tokens", "mail_deliveries", "audit_events"} {
		var raw string
		if x.f.PostgreSQL.QueryRow(x.ctx, "SELECT COALESCE(jsonb_agg(to_jsonb(v) ORDER BY to_jsonb(v)::text)::text,'[]') FROM "+table+" v").Scan(&raw) != nil {
			t.Fatal("STARTUP_PRIVATE_POSTIMAGE_UNAVAILABLE")
		}
		_, _ = io.WriteString(digest, table+"\n"+raw+"\n")
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func startupLedgerDigest(ctx context.Context, x *serverCompositionStory, hidden bool) (string, error) {
	table := "schema_migrations"
	if hidden {
		table = "startup_hidden_ledger"
	}
	var raw string
	if e := x.f.PostgreSQL.QueryRow(ctx, "SELECT COALESCE(jsonb_agg(to_jsonb(v) ORDER BY version)::text,'[]') FROM "+table+" v").Scan(&raw); e != nil {
		return "", e
	}
	digest := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(digest[:]), nil
}

func startupCorruptLedger(t *testing.T, x *serverCompositionStory) func() {
	t.Helper()
	original, e := startupLedgerDigest(x.ctx, x, false)
	var checksum string
	if e != nil || x.f.PostgreSQL.QueryRow(x.ctx, "SELECT checksum FROM schema_migrations WHERE version=1").Scan(&checksum) != nil || checksum == strings.Repeat("0", 64) {
		t.Fatal("STARTUP_ORIGINAL_LEDGER_REQUIRED")
	}
	tag, e := x.f.PostgreSQL.Exec(x.ctx, "UPDATE schema_migrations SET checksum=repeat('0',64) WHERE version=1 AND checksum=$1", checksum)
	if e != nil || tag.RowsAffected() != 1 {
		t.Fatal("STARTUP_CHANGED_LEDGER_FIXTURE_FAILED")
	}
	attempted := false
	restore := func() {
		if attempted {
			return
		}
		attempted = true
		// Cleanup must remain bounded and usable after the assertion context expires.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		tag, e := x.f.PostgreSQL.Exec(ctx, "UPDATE schema_migrations SET checksum=$1 WHERE version=1 AND checksum=repeat('0',64)", checksum)
		if e != nil || tag.RowsAffected() != 1 {
			t.Error("STARTUP_LEDGER_FIXTURE_RESTORE_FAILED")
			return
		}
		current, e := startupLedgerDigest(ctx, x, false)
		if e != nil || current != original {
			t.Error("STARTUP_LEDGER_FIXTURE_RESTORE_CHANGED_OTHER_ROWS")
		}
	}
	t.Cleanup(restore)
	return restore
}

func startupRefusal(t *testing.T, x *serverCompositionStory, binary startupExecutable, sequence int, c config.Config, code string) {
	t.Helper()
	raw, e := yaml.Marshal(c)
	if e != nil {
		t.Fatal("STARTUP_CONFIGURATION_ENCODING_FAILED")
	}
	file := x.private(t, "startup-input-"+strconv.Itoa(sequence)+".yaml", raw)
	configurationDigest := sha256.Sum256(raw)
	ctx, cancel := context.WithTimeout(x.ctx, startupChildSeconds*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, binary.path, "-test.run=^TestM01CurrentStartupChild$", "-test.count=1", "-test.timeout="+strconv.Itoa(startupChildSeconds)+"s")
	// The test executable needs no provider, proxy, home, login or SQL environment.
	// Preserve only the parent's admitted scratch and Go runtime resource limits.
	for _, key := range []string{"TMPDIR", "GOMEMLIMIT", "GOMAXPROCS"} {
		if value, exists := os.LookupEnv(key); exists {
			child.Env = append(child.Env, key+"="+value)
		}
	}
	child.Env = append(child.Env, startupChildConfiguration+"="+file,
		startupChildConfigurationSHA+"="+hex.EncodeToString(configurationDigest[:]))
	var output startupBoundedOutput
	child.Stdout, child.Stderr = &output, &output
	before := startupBusinessDigest(t, x)
	if e = child.Start(); e != nil {
		t.Fatal("STARTUP_FIXED_CHILD_NOT_STARTED")
	}
	t.Logf("STARTUP_FIXED_CHILD_STARTED case=%d pid=%d executable_sha256=%s configuration_sha256=%x timeout_seconds=%d output_limit_bytes=%d", sequence, child.Process.Pid, binary.digest, configurationDigest, startupChildSeconds, startupOutputBytes)
	e = child.Wait()
	var exited *exec.ExitError
	exit := -1
	joined := child.ProcessState != nil
	if joined {
		exit = child.ProcessState.ExitCode()
	}
	if !errors.As(e, &exited) || ctx.Err() != nil || !joined || !child.ProcessState.Exited() || exit != 1 || output.overflow || output.buffer.String() != code+"\n" {
		t.Fatalf("STARTUP_FIXED_CHILD_REFUSAL_FAILED case=%d exit=%d joined=%t timeout=%t overflow=%t expected_code=%s bounded_output_sha256=%x", sequence, exit, joined, ctx.Err() != nil, output.overflow, code, sha256.Sum256(output.buffer.Bytes()))
	}
	after, e := os.ReadFile(file)
	if e != nil || !bytes.Equal(after, raw) {
		t.Fatal("STARTUP_FIXED_CHILD_CONFIGURATION_CHANGED")
	}
	listener, e := net.Listen("tcp", c.Server.ListenAddress)
	if e != nil {
		t.Fatal("STARTUP_REFUSAL_LEFT_LISTENER")
	}
	if listener.Close() != nil || startupBusinessDigest(t, x) != before {
		t.Fatal("STARTUP_REFUSAL_CHANGED_BUSINESS_AUTHORITY")
	}
	t.Logf("STARTUP_FIXED_CHILD_REFUSED case=%d pid=%d exit=1 joined=true code=%s configuration_sha256=%x business_unchanged=true listener_absent=true", sequence, child.Process.Pid, code, sha256.Sum256(raw))
}

func TestM01CurrentStartupRequiredInputs(t *testing.T) {
	x := serverCompositionSetup(t, false)
	if x.f.PostgreSQL == nil || x.d.Close() != nil {
		t.Fatal("STARTUP_ORIGINAL_POSTGRES_LEASE_RELEASE_REQUIRED")
	}
	binary := startupMeasuredExecutable(t)
	public := x.private(t, "public-synthetic-key", bytes.Repeat([]byte{7}, 32))
	if os.Chmod(public, 0644) != nil {
		t.Fatal("STARTUP_PUBLIC_KEY_FIXTURE_FAILED")
	}
	missingInventory := filepath.Join(x.root, "missing-template-inventory")
	if os.Mkdir(missingInventory, 0700) != nil {
		t.Fatal("STARTUP_PRIVATE_LOCALE_FIXTURE_FAILED")
	}
	for i, item := range []struct {
		code   string
		mutate func(*config.Config)
	}{
		{"AUTH_KEY_INVALID", func(c *config.Config) { c.Security.AuthHMACKeyFile = filepath.Join(x.root, "absent-auth-key") }},
		{"AUTH_KEY_INVALID", func(c *config.Config) { c.Security.AuthHMACKeyFile = public }},
		{"AUTH_KEY_INVALID", func(c *config.Config) {
			c.Security.AuthHMACKeyFile = x.private(t, "short-auth-key", []byte("synthetic"))
		}},
		{"SECRET_FILE_INVALID", func(c *config.Config) { c.Security.MailKeyringFile = filepath.Join(x.root, "absent-mail-keyring") }},
		{"BLOCKLIST_MISSING", func(c *config.Config) { c.Security.PasswordBlocklistFile = filepath.Join(x.root, "absent-blocklist") }},
		{"BLOCKLIST_EMPTY", func(c *config.Config) {
			c.Security.PasswordBlocklistFile = x.private(t, "empty-blocklist", []byte("\n"))
		}},
		{"LOCALIZATION_PACKAGE_INVALID", func(c *config.Config) { c.Localization.PackageDirectory = missingInventory }},
		{"LOCALIZATION_REQUIRED_LOCALE_UNAVAILABLE", func(c *config.Config) { c.Localization.RequiredLocales = []string{"fr"} }},
	} {
		c := x.c
		item.mutate(&c)
		startupRefusal(t, x, binary, i, c, item.code)
	}
	registry, e := localization.New(x.c.Localization, x.c.Policy().Localization)
	if e != nil {
		t.Fatal("STARTUP_REAL_EMBEDDED_REGISTRY_REQUIRED")
	}
	templates, e := mail.LoadTemplatesWithRegistry(registry)
	if e != nil || len(templates) != 6 {
		t.Fatal("STARTUP_REAL_SIX_EMBEDDED_MAIL_TEMPLATES_REQUIRED")
	}
	for _, locale := range []string{"en", "zh-CN"} {
		for _, purpose := range []string{"invitation", "password_reset", "password_changed"} {
			v, exists := templates[locale+"/"+purpose]
			if !exists || v.Subject == "" || v.Text == "" || v.HTML == "" {
				t.Fatal("STARTUP_EMBEDDED_MAIL_TEMPLATE_INCOMPLETE")
			}
		}
	}
	restore := startupCorruptLedger(t, x)
	changed, e := startupLedgerDigest(x.ctx, x, false)
	if e != nil {
		t.Fatal("STARTUP_CHANGED_LEDGER_REQUIRED")
	}
	startupRefusal(t, x, binary, 8, x.c, "SCHEMA_INCOMPATIBLE")
	if current, e := startupLedgerDigest(x.ctx, x, false); e != nil || current != changed {
		t.Fatal("STARTUP_CHANGED_LEDGER_WAS_REPAIRED")
	}
	restore()
	if t.Failed() {
		t.FailNow()
	}
	original, e := startupLedgerDigest(x.ctx, x, false)
	if e != nil {
		t.Fatal("STARTUP_ORIGINAL_LEDGER_REQUIRED")
	}
	if _, e = x.f.PostgreSQL.Exec(x.ctx, "ALTER TABLE schema_migrations RENAME TO startup_hidden_ledger"); e != nil {
		t.Fatal("STARTUP_MISSING_LEDGER_FIXTURE_FAILED")
	}
	restored := false
	restoreMissing := func() {
		if restored {
			return
		}
		restored = true
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, e := x.f.PostgreSQL.Exec(ctx, "ALTER TABLE startup_hidden_ledger RENAME TO schema_migrations"); e != nil {
			t.Error("STARTUP_MISSING_LEDGER_FIXTURE_RESTORE_FAILED")
			return
		}
		if current, e := startupLedgerDigest(ctx, x, false); e != nil || current != original {
			t.Error("STARTUP_MISSING_LEDGER_FIXTURE_CONTENT_CHANGED")
		}
	}
	t.Cleanup(restoreMissing)
	startupRefusal(t, x, binary, 9, x.c, "SCHEMA_UNAVAILABLE")
	var absent bool
	if x.f.PostgreSQL.QueryRow(x.ctx, "SELECT to_regclass('schema_migrations') IS NULL").Scan(&absent) != nil || !absent {
		t.Fatal("STARTUP_MISSING_LEDGER_WAS_CREATED")
	}
	if current, e := startupLedgerDigest(x.ctx, x, true); e != nil || current != original {
		t.Fatal("STARTUP_HIDDEN_LEDGER_WAS_CHANGED")
	}
	restoreMissing()
}

// Keep the advertised authority in the URL so net/http sends the matching SNI
// and verifies that DNS SAN. Only the socket destination is mapped to this
// fixture's loopback listener; no system DNS, proxy or TLS bypass is involved.
func startupConfigureClient(t *testing.T, x *serverCompositionStory) {
	t.Helper()
	base, ok := x.client.Transport.(*http.Transport)
	if !ok || base.TLSClientConfig == nil || base.TLSClientConfig.RootCAs == nil || base.TLSClientConfig.InsecureSkipVerify || base.TLSClientConfig.ServerName != "" {
		t.Fatal("STARTUP_VERIFIED_TLS_CLIENT_REQUIRED")
	}
	address := x.c.Server.ListenAddress
	host, _, e := net.SplitHostPort(address)
	if e != nil || !net.ParseIP(host).IsLoopback() || x.c.Server.EffectiveTransportMode() != config.TransportDirectHTTPS {
		t.Fatal("STARTUP_PRIVATE_HTTPS_LISTENER_REQUIRED")
	}
	targets := make(map[string]bool)
	for _, raw := range []string{x.c.Server.PortalOrigin, x.c.Server.AdminOrigin} {
		origin, e := config.ParseBrowserOrigin(raw)
		if e != nil || origin.Scheme != "https" || net.ParseIP(origin.Hostname) != nil {
			t.Fatal("STARTUP_HTTPS_DNS_ORIGIN_REQUIRED")
		}
		targets[net.JoinHostPort(origin.Hostname, strconv.Itoa(int(origin.Port)))] = true
	}
	if len(targets) != 2 {
		t.Fatal("STARTUP_DISTINCT_ENTRANCES_REQUIRED")
	}
	dialer := &net.Dialer{}
	transport := &http.Transport{
		TLSClientConfig: base.TLSClientConfig.Clone(),
		DialContext: func(ctx context.Context, network, target string) (net.Conn, error) {
			if network != "tcp" || !targets[target] {
				return nil, errors.New("STARTUP_PRIVATE_DIAL_TARGET_REQUIRED")
			}
			return dialer.DialContext(ctx, network, address)
		},
	}
	x.client = &http.Client{
		Transport: transport,
		Timeout:   x.client.Timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("STARTUP_HEALTH_REDIRECT_REFUSED")
		},
	}
	t.Cleanup(transport.CloseIdleConnections)
}

func startupHealthRequest(t *testing.T, ctx context.Context, x *serverCompositionStory, audience, path string) *http.Request {
	t.Helper()
	origin := ""
	switch audience {
	case "portal":
		origin = x.c.Server.PortalOrigin
	case "admin":
		origin = x.c.Server.AdminOrigin
	default:
		t.Fatal("STARTUP_HEALTH_AUDIENCE_INVALID")
	}
	if path != "/health/live" && path != "/health/ready" {
		t.Fatal("STARTUP_HEALTH_PATH_INVALID")
	}
	request, e := http.NewRequestWithContext(ctx, http.MethodGet, origin+path, nil)
	if e != nil {
		t.Fatal("STARTUP_HEALTH_REQUEST_FAILED")
	}
	return request
}

func startupHealth(t *testing.T, x *serverCompositionStory, path string, want int, audiences ...string) {
	t.Helper()
	if len(audiences) == 0 {
		audiences = []string{"portal", "admin"}
	}
	for _, audience := range audiences {
		request := startupHealthRequest(t, x.ctx, x, audience, path)
		response, e := x.client.Do(request)
		if e != nil {
			t.Fatal("STARTUP_HEALTH_VERIFIED_TLS_FAILED")
		}
		raw, e := io.ReadAll(io.LimitReader(response.Body, startupOutputBytes+1))
		closeError := response.Body.Close()
		var body map[string]any
		if e != nil || closeError != nil || response.TLS == nil || len(response.TLS.VerifiedChains) == 0 || response.TLS.ServerName != request.URL.Hostname() || len(raw) > startupOutputBytes || response.StatusCode != want || response.Header.Get("Cache-Control") != "no-store" || len(response.Header.Values("Set-Cookie")) != 0 || json.Unmarshal(raw, &body) != nil {
			t.Fatalf("STARTUP_HEALTH_PUBLIC_CONTRACT_FAILED audience=%s path=%s expected=%d actual=%d", audience, path, want, response.StatusCode)
		}
		if _, e := uuid.Parse(response.Header.Get("X-Request-ID")); e != nil {
			t.Fatal("STARTUP_HEALTH_REQUEST_ID_REQUIRED")
		}
		if want == 503 {
			if response.Header.Get("Content-Type") != "application/problem+json" || len(body) != 5 || body["type"] != "about:blank" || body["title"] != "SERVICE_NOT_READY" || body["code"] != "SERVICE_NOT_READY" || body["status"] != float64(503) || body["request_id"] != response.Header.Get("X-Request-ID") {
				t.Fatal("STARTUP_HEALTH_PRIVATE_DETAILS_EXPOSED")
			}
		} else {
			data, ok := body["data"].(map[string]any)
			if response.Header.Get("Content-Type") != "application/json" || len(body) != 1 || !ok || len(data) != 1 || data["status"] != "ok" {
				t.Fatal("STARTUP_HEALTH_OK_SHAPE_INVALID")
			}
		}
	}
}

// Each generation has its own selected pool, owners, lifecycle and join result.
// A schema fault is fatal to that generation; restoring SQL cannot revive it.
type startupGeneration struct {
	cancel        context.CancelFunc
	done          chan struct{}
	result        error
	selected      *selectedDatabase
	address       string
	number        int
	expectFailure bool
	joined        bool
}

func startupSchemaFailureOnly(e error) bool {
	if e == nil {
		return false
	}
	if joined, ok := e.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !startupSchemaFailureOnly(child) {
				return false
			}
		}
		return true
	}
	return e.Error() == "CONTROL_SCHEMA_VERIFICATION_FAILED"
}

func (run *startupGeneration) join(t *testing.T, cancel bool) {
	t.Helper()
	if run.joined {
		return
	}
	if cancel {
		run.cancel()
	}
	select {
	case <-run.done:
		run.joined = true
		run.cancel()
	case <-time.After(20 * time.Second):
		t.Error("STARTUP_GENERATION_DID_NOT_JOIN")
		return
	}
	if run.expectFailure && !startupSchemaFailureOnly(run.result) || !run.expectFailure && run.result != nil {
		t.Error("STARTUP_GENERATION_UNEXPECTED_STOP_RESULT")
	}
	if run.selected.workers == nil {
		t.Error("STARTUP_GENERATION_WORKER_OWNERSHIP_MISSING")
		return
	}
	select {
	case <-run.selected.workers:
	default:
		t.Error("STARTUP_GENERATION_WORKERS_NOT_JOINED")
		return
	}
	listener, e := net.Listen("tcp", run.address)
	if e != nil {
		t.Error("STARTUP_GENERATION_LISTENER_RETAINED")
		return
	}
	if listener.Close() != nil {
		t.Error("STARTUP_GENERATION_LISTENER_PROBE_CLOSE_FAILED")
		return
	}
	t.Logf("STARTUP_GENERATION_JOINED generation=%d schema_failure=%t workers_joined=true listener_absent=true", run.number, run.expectFailure)
}

func startupServeGeneration(t *testing.T, x *serverCompositionStory, number int) *startupGeneration {
	t.Helper()
	ctx, cancel := context.WithCancel(x.ctx)
	run := &startupGeneration{cancel: cancel, done: make(chan struct{}), selected: x.d, address: x.c.Server.ListenAddress, number: number}
	c, owner, outbox := x.c, x.owner, x.mail
	go func() {
		defer close(run.done)
		startup, stop := context.WithTimeout(ctx, config.Seconds(c.Policy().HTTP.StartupSeconds))
		defer stop()
		run.result = serveSelected(ctx, startup, c, run.selected, owner, outbox)
	}()
	t.Cleanup(func() { run.join(t, true) })
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-run.done:
			t.Fatal("STARTUP_GENERATION_ENDED_BEFORE_LISTENING")
		case <-deadline.C:
			t.Fatal("STARTUP_GENERATION_LISTEN_DEADLINE")
		case <-ticker.C:
			request := startupHealthRequest(t, ctx, x, "portal", "/health/live")
			response, e := x.client.Do(request)
			if e != nil {
				continue
			}
			length, readError := io.Copy(io.Discard, io.LimitReader(response.Body, startupOutputBytes+1))
			closeError := response.Body.Close()
			if readError != nil || closeError != nil || length > startupOutputBytes || response.TLS == nil || len(response.TLS.VerifiedChains) == 0 || response.TLS.ServerName != request.URL.Hostname() || response.StatusCode != http.StatusOK {
				t.Fatalf("STARTUP_GENERATION_LIVE_PROBE_REJECTED generation=%d status=%d", number, response.StatusCode)
			}
			t.Logf("STARTUP_GENERATION_LISTENING generation=%d", number)
			return run
		}
	}
}

func startupReopenSelected(t *testing.T, x *serverCompositionStory) {
	t.Helper()
	if e := x.d.Close(); e != nil {
		t.Fatal("STARTUP_PRIOR_GENERATION_LEASE_RELEASE_FAILED")
	}
	selected, e := openSelectedDatabase(x.ctx, x.c, "serve")
	if e != nil {
		t.Fatal("STARTUP_RESTORED_GENERATION_OPEN_FAILED")
	}
	t.Cleanup(func() {
		if e := selected.Close(); e != nil {
			t.Error("STARTUP_REOPENED_DATABASE_CLOSE_FAILED")
		}
	})
	owner := selected.identity(x.c)
	owner.HMACKey = append([]byte(nil), x.owner.HMACKey...)
	owner.Passwords = x.owner.Passwords
	outbox := &mail.Outbox{DB: selected.postgres, Keys: x.mail.Keys, Config: x.c, Templates: x.mail.Templates}
	owner.Mail = outbox
	x.d, x.owner, x.mail = selected, owner, outbox
}

func TestM01CurrentUnbootstrappedHealth(t *testing.T) {
	x := serverCompositionSetup(t, true)
	startupConfigureClient(t, x)
	run := startupServeGeneration(t, x, 1)
	before := startupBusinessDigest(t, x)
	startupHealth(t, x, "/health/live", 200)
	startupHealth(t, x, "/health/ready", 503)
	if startupBusinessDigest(t, x) != before {
		t.Fatal("STARTUP_PUBLIC_HEALTH_CHANGED_AUTHORITY")
	}
	run.join(t, true)
}

func TestM01CurrentSchemaHealthRecovery(t *testing.T) {
	x := serverCompositionSetup(t, false)
	startupConfigureClient(t, x)
	run := startupServeGeneration(t, x, 1)
	before := startupBusinessDigest(t, x)
	for i, audience := range []string{"portal", "admin"} {
		startupHealth(t, x, "/health/ready", 200)
		restore := startupCorruptLedger(t, x)
		changed, e := startupLedgerDigest(x.ctx, x, false)
		if e != nil {
			t.Fatal("STARTUP_HEALTH_CHANGED_LEDGER_REQUIRED")
		}
		startupHealth(t, x, "/health/live", 200)
		run.expectFailure = true
		// The first schema check cancels this real generation. Exercise the other
		// audience in the next generation instead of racing a second stopped socket.
		startupHealth(t, x, "/health/ready", 503, audience)
		run.join(t, false)
		if t.Failed() {
			t.FailNow()
		}
		if current, e := startupLedgerDigest(x.ctx, x, false); e != nil || current != changed {
			t.Fatal("STARTUP_HEALTH_CHANGED_LEDGER_WAS_REPAIRED")
		}
		restore()
		if t.Failed() {
			t.FailNow()
		}
		startupReopenSelected(t, x)
		run = startupServeGeneration(t, x, i+2)
	}
	startupHealth(t, x, "/health/live", 200)
	startupHealth(t, x, "/health/ready", 200)
	if startupBusinessDigest(t, x) != before {
		t.Fatal("STARTUP_HEALTH_RECOVERY_CHANGED_AUTHORITY")
	}
	run.join(t, true)
}
