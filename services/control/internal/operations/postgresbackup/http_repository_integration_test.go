//go:build repository_integration && postgres_backup_integration

package postgresbackup

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
)

type restoredHTTPTrace struct {
	Audience       string `json:"audience"`
	URL            string `json:"url"`
	CredentialKind string `json:"credential_kind"`
	TokenSHA256    string `json:"token_sha256,omitempty"`
	Status         int    `json:"status"`
	ProblemCode    string `json:"problem_code,omitempty"`
	BodySHA256     string `json:"body_sha256"`
}

type boundedHTTPLog struct {
	mu        sync.Mutex
	file      *os.File
	written   int
	truncated bool
}

func (w *boundedHTTPLog) Write(raw []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	size := len(raw)
	if w.written+size > 8<<20 {
		w.truncated = true
		return size, nil
	}
	n, e := w.file.Write(raw)
	w.written += n
	return n, e
}

func TestActualRestoredPostgresHTTPReadinessContentAndAuthority(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	in, e := loadRestoredHTTPInputs()
	if e != nil {
		t.Fatal("exact protected HTTP inputs before SQL", e)
	}
	if e = restoredHTTPExecutable(ctx, in.CurrentControlSHA256); e != nil {
		t.Fatal("current executed Control fingerprint", e)
	}
	request, _, e := LoadRestoreRequest("/fixture/request-restore.json", "")
	if e != nil {
		t.Fatal(e)
	}
	var complete RestoreCompletion
	completionBytes, e := readPrivateDocument("/fixture/retained-original-restore-complete.json", 1<<20, &complete)
	if e != nil || digest(completionBytes) != in.NativeCompletionSHA256 {
		t.Fatal(e)
	}
	var manifest Manifest
	manifestBytes, e := readPrivateDocument(filepath.Join(request.BackupRoot, candidateName), 1<<20, &manifest)
	if e != nil || digest(manifestBytes) != in.SourceManifestSHA256 {
		t.Fatal(e)
	}
	for name, sha := range map[string]string{restoreIntentName: complete.IntentSHA256, restoreFilesName: complete.FilesSHA256} {
		raw, e := readPrivateBytes(filepath.Join(request.RestoreRoot, name), 1<<20)
		if e != nil || digest(raw) != sha {
			t.Fatal("original native proof bytes changed", name, e)
		}
	}
	if request.TargetScopeID != in.TargetScopeID || request.SourceManifestSHA256 != in.SourceManifestSHA256 || complete.ManifestSHA256 != in.SourceManifestSHA256 || complete.RuntimeContext.TargetDatabase != request.TargetDatabase {
		t.Fatal("exact retained native target required")
	}
	nativeConfig, e := config.LoadProtected(request.RuntimeConfigurationFile)
	if e != nil {
		t.Fatal(e)
	}
	pool, e := restoredHTTPPool(ctx, nativeConfig.Database.DSNFile, request.TargetDatabase, "m18_restored_"+in.TargetScopeID)
	if e != nil {
		t.Fatal(e)
	}
	var user, workspace string
	var epoch int64
	if e = pool.QueryRow(ctx, `SELECT primary_admin_user_id::text,default_workspace_id::text,(SELECT auth_epoch FROM users WHERE id=i.primary_admin_user_id) FROM installation_state i WHERE id=1 AND bootstrap_complete`).Scan(&user, &workspace, &epoch); e != nil || epoch != 2 {
		pool.Close()
		t.Fatal("exact restored current actor/epoch", e)
	}
	c, configuration, ca, current, prepared, e := loadRestoredHTTPPrepared(ctx, in, pool)
	if e != nil {
		pool.Close()
		t.Fatal("prepared setup verification; partial/uncertain setup never reissued", e)
	}
	if !prepared {
		c, configuration, ca, e = restoredHTTPConfiguration(ctx, in, request, complete, manifest, pool)
		if e != nil {
			pool.Close()
			t.Fatal("real full-profile TLS/license/configuration producer; retain partial intent", e)
		}
		accounts := &identity.Service{DB: pool, Config: c}
		current = map[string]retainedOriginalAuthority{}
		for _, audience := range []string{"portal", "admin"} {
			credential, e := issueRestoredHTTPAuthority(ctx, accounts, pool, filepath.Dir(configuration), "current-"+audience, user, epoch, audience)
			if e != nil {
				pool.Close()
				t.Fatal("current serving credential; never resend uncertain commit", e)
			}
			if _, e = accounts.Authenticate(ctx, credential.Token, audience); e != nil {
				pool.Close()
				t.Fatal(e)
			}
			current[audience] = credential
		}
		if e = commitRestoredHTTPPrepared(ctx, in, c, configuration, pool, current); e != nil {
			pool.Close()
			t.Fatal("durable exact prepared setup checkpoint", e)
		}
	}
	root := filepath.Dir(configuration)
	attemptRoot := filepath.Join(root, "serve-"+in.AttemptID)
	attempt, e := newDirectory(attemptRoot)
	if e != nil {
		pool.Close()
		t.Fatal("new exclusive serving attempt only", e)
	}
	if e = attempt.Close(); e != nil {
		pool.Close()
		t.Fatal(e)
	}
	// Do not consume the restricted role's connection allowance while the real
	// Control process owns its installation lock and serving pool.
	pool.Close()
	if e = waitRestoredHTTPProjection(ctx, in); e != nil {
		t.Fatal("actual distinct-UID read-only journal publisher", e)
	}
	logFile, e := os.OpenFile(filepath.Join(attemptRoot, "control.log"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer logFile.Close()
	log := &boundedHTTPLog{file: logFile}
	command := exec.Command("/operator/control", "serve", "--config", configuration)
	command.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "LC_ALL=C", "TZ=UTC", "TMPDIR=/tmp", "GOMAXPROCS=2", "GOMEMLIMIT=256MiB", "PYTHONDONTWRITEBYTECODE=1", "PYTHONNOUSERSITE=1"}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Stdout = log
	command.Stderr = log
	if e = command.Start(); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	stopped := false
	stop := func() error {
		if stopped {
			return nil
		}
		stopped = true
		select {
		case e := <-done:
			return errors.Join(errors.New("M18_SERVING_EXITED_BEFORE_NORMAL_STOP"), e)
		default:
		}
		if e := command.Process.Signal(syscall.SIGTERM); e != nil {
			return e
		}
		select {
		case e := <-done:
			if log.truncated {
				return errors.Join(e, errors.New("M18_HTTP_LOG_TRUNCATED"))
			}
			return errors.Join(e, logFile.Sync())
		case <-time.After(config.Seconds(c.Policy().HTTP.ShutdownSeconds) + 5*time.Second):
			_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
			<-done
			return errors.New("M18_HTTP_NORMAL_STOP_UNCONFIRMED")
		}
	}
	defer func() {
		if !stopped {
			if e := stop(); e != nil {
				t.Error("owned HTTP process joined after failure", e)
			}
		}
	}()
	client, e := restoredHTTPClient(c, ca)
	if e != nil {
		t.Fatal(e)
	}
	defer client.CloseIdleConnections()
	traces := []restoredHTTPTrace{}
	ready := false
	readyDeadline := time.Now().Add(config.Seconds(c.Policy().HTTP.StartupSeconds) + config.Seconds(c.Policy().HTTP.RequestSeconds))
	for time.Now().Before(readyDeadline) {
		select {
		case e := <-done:
			stopped = true
			t.Fatal("real serving startup exited; no authentication claim", e)
		default:
		}
		body, trace, e := restoredHTTPGet(ctx, client, c, "admin", "/health/ready", "", "readiness")
		if e == nil && trace.Status == 200 && json.Valid(body) {
			traces = append(traces, trace)
			ready = true
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("readiness deadline", ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
	if !ready {
		t.Fatal("full releases real readiness did not pass; no old-authority conclusion")
	}
	for _, audience := range []string{"portal", "admin"} {
		body, trace, e := restoredHTTPGet(ctx, client, c, audience, "/api/v1/me", current[audience].Token, "current_after_restore")
		if e != nil || trace.Status != 200 || !bytes.Contains(body, []byte(user)) || !bytes.Contains(body, []byte(workspace)) {
			t.Fatal("current credential real content control", audience, trace.Status, e)
		}
		traces = append(traces, trace)
	}
	if in.Mode == "populated_positive" {
		var populated struct {
			References populatedReferences `json:"references"`
			Restore    RestoreCompletion   `json:"restore"`
		}
		raw, e := readPrivateBytes("/fixture/actual-populated-receipt.json", 1<<20)
		if e != nil || json.Unmarshal(raw, &populated) != nil || populated.Restore.ManifestSHA256 != in.SourceManifestSHA256 {
			t.Fatal("same successful populated native target required", e)
		}
		for _, audience := range []string{"portal", "admin"} {
			for _, kind := range []string{"models", "datasets"} {
				path := "/api/v1/workspaces/" + workspace + "/" + kind
				if audience == "admin" {
					path = "/api/v1/admin/" + kind + "?workspace_id=" + workspace
				}
				body, trace, e := restoredHTTPGet(ctx, client, c, audience, path, current[audience].Token, "current_populated_content")
				if e != nil || trace.Status != 200 || !json.Valid(body) {
					t.Fatal("actual populated business content", audience, kind, trace.Status, e)
				}
				required := []string{populated.References.Dataset}
				if kind == "models" {
					required = []string{populated.References.BaseModel, populated.References.DerivedModel}
				}
				for _, id := range required {
					if id == "" || !bytes.Contains(body, []byte(id)) {
						t.Fatal("restored known object missing from actual HTTP response", audience, kind)
					}
				}
				traces = append(traces, trace)
			}
		}
	} else {
		source, e := config.LoadProtected("/fixture/configuration/runtime.yaml")
		if e != nil {
			t.Fatal(e)
		}
		sourcePool, e := restoredHTTPPool(ctx, source.Database.DSNFile, manifest.Request.Database, "m18_runtime_"+manifest.Request.SourceScopeID)
		if e != nil {
			t.Fatal(e)
		}
		defer sourcePool.Close()
		sourceAccounts := &identity.Service{DB: sourcePool, Config: source}
		targetPool, e := restoredHTTPPool(ctx, "/fixture/target-operator-dsn", request.TargetDatabase, "projectone_operator")
		if e != nil {
			t.Fatal(e)
		}
		defer targetPool.Close()
		for _, audience := range []string{"portal", "admin"} {
			var old retainedOriginalAuthority
			if _, e = readPrivateDocument("/fixture/original-authority-"+audience+"-private.json", 16<<10, &old); e != nil || old.Audience != audience || old.UserID != user || old.Epoch != 1 {
				t.Fatal("exact original private handoff", e)
			}
			var storedHash []byte
			var revoked bool
			var oldEpoch int64
			if e = targetPool.QueryRow(ctx, `SELECT token_hash,revoked_at IS NOT NULL,auth_epoch FROM sessions WHERE id=$1 AND user_id=$2 AND audience=$3`, old.SessionID, old.UserID, audience).Scan(&storedHash, &revoked, &oldEpoch); e != nil || !revoked || oldEpoch != old.Epoch || !bytes.Equal(storedHash, security.Digest(old.Token)) {
				t.Fatal("exact backed-up revoked original session row", e)
			}
			if _, e = sourceAccounts.Authenticate(ctx, old.Token, audience); e != nil {
				t.Fatal("same original token is still source-valid immediately before HTTP negative", audience, e)
			}
			_, trace, e := restoredHTTPGet(ctx, client, c, audience, "/api/v1/me", old.Token, "exact_original_before_backup")
			if e != nil || trace.Status != http.StatusUnauthorized || trace.ProblemCode != "AUTH_SESSION_EXPIRED" {
				t.Fatal("exact original token served refusal; no404/503/transport substitute", audience, trace.Status, trace.ProblemCode, e)
			}
			traces = append(traces, trace)
			body, positive, e := restoredHTTPGet(ctx, client, c, audience, "/api/v1/me", current[audience].Token, "current_after_negative")
			if e != nil || positive.Status != 200 || !bytes.Contains(body, []byte(user)) {
				t.Fatal("same live service current credential after negative", e)
			}
			traces = append(traces, positive)
		}
	}
	if e = stop(); e != nil {
		t.Fatal("real Control normal0 stop/join", e)
	}
	if e = fixtureJSON(filepath.Join(attemptRoot, "actual-http-receipt.json"), map[string]any{"status": "passed_actual_full_releases_restored_http_profile", "mode": in.Mode, "inputs": in, "setup_reused_without_reissuing_credentials_or_license": prepared, "target_database": request.TargetDatabase, "user_id": user, "workspace_id": workspace, "epoch": epoch, "current_role": "m18_restored_" + in.TargetScopeID, "transport": "direct HTTPS; exact advertised portal.internal/admin.internal DNS/SNI and explicitly pinned loopback dial; trusted fixture CA, no verification bypass", "certificate_sha256": digest(ca), "licensing": "actual signed synthetic whole-platform active license, existing owner import/verification; no bypass", "readiness": "actual Control serve and full original readiness/schema/store/Knowledge/release-view owners", "traces": traces, "normal_stop": 0, "scope": "HTTP Control/Knowledge runtime is explicitly local AMD64 emulation on the retained native PostgreSQL target. Native populated proof and distinct small original-authority proof remain separate. No hardware/provider/Kubernetes acceptance."}); e != nil {
		t.Fatal(e)
	}
}

func restoredHTTPExecutable(ctx context.Context, expected string) error {
	root, e := openPrivateRoot("/operator")
	if e != nil {
		return e
	}
	defer root.Close()
	file, e := root.OpenFile("control", os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return e
	}
	defer file.Close()
	info, e := file.Stat()
	if e != nil {
		return e
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0700 || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 || info.Size() > 128<<20 {
		return errors.New("M18_HTTP_CONTROL_FILE_INVALID")
	}
	sha, n, e := hashStream(ctx, nil, file, 128<<20)
	if e != nil || sha != expected || n != info.Size() {
		return errors.New("M18_HTTP_CONTROL_FINGERPRINT_INVALID")
	}
	return nil
}
func restoredHTTPPool(ctx context.Context, path, database, user string) (*pgxpool.Pool, error) {
	raw, e := readPrivateBytes(path, 16<<10)
	if e != nil {
		return nil, e
	}
	c, e := pgxpool.ParseConfig(strings.TrimSpace(string(raw)))
	if e != nil {
		return nil, e
	}
	if c.ConnConfig.Host != "postgres" || c.ConnConfig.Port != 5432 || c.ConnConfig.Database != database || c.ConnConfig.User != user {
		return nil, errors.New("M18_EXACT_HTTP_DATABASE_ROLE_REQUIRED")
	}
	c.MaxConns = 2
	c.MinConns = 0
	return pgxpool.NewWithConfig(ctx, c)
}
func waitRestoredHTTPProjection(ctx context.Context, in restoredHTTPInputs) error {
	for {
		info, e := os.Lstat(filepath.Join(in.ProjectionDirectory, "current.json"))
		if e == nil {
			st, ok := info.Sys().(*syscall.Stat_t)
			if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0444 || st.Uid != 0 || st.Nlink != 1 {
				return errors.New("M18_HTTP_DISTINCT_PUBLISHER_REQUIRED")
			}
			return nil
		}
		if !os.IsNotExist(e) {
			return e
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}
func restoredHTTPClient(c config.Config, ca []byte) (*http.Client, error) {
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return nil, errors.New("M18_HTTP_REAL_CA_REQUIRED")
	}
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil || network != "tcp" || port != "8080" || host != "portal.internal" && host != "admin.internal" {
			return nil, errors.New("M18_HTTP_EXPLICIT_DIAL_ONLY")
		}
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", "127.0.0.1:8080")
	}}
	return &http.Client{Transport: transport, Timeout: config.Seconds(c.Policy().HTTP.RequestSeconds) + 5*time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}
func restoredHTTPGet(ctx context.Context, client *http.Client, c config.Config, audience, path, token, kind string) ([]byte, restoredHTTPTrace, error) {
	origin := c.Server.PortalOrigin
	if audience == "admin" {
		origin = c.Server.AdminOrigin
	}
	trace := restoredHTTPTrace{Audience: audience, URL: origin + path, CredentialKind: kind}
	if token != "" {
		trace.TokenSHA256 = digest([]byte(token))
	}
	request, e := http.NewRequestWithContext(ctx, http.MethodGet, origin+path, nil)
	if e != nil {
		return nil, trace, e
	}
	request.Header.Set("Origin", origin)
	request.Header.Set("Accept", "application/json")
	policy, e := httpserver.NewCookiePolicy(c.Server.EffectiveTransportMode())
	if e != nil {
		return nil, trace, e
	}
	if token != "" {
		request.AddCookie(&http.Cookie{Name: policy.Name(audience, httpserver.SessionCookie), Value: token, Secure: true, Path: "/"})
	}
	response, e := client.Do(request)
	if e != nil {
		return nil, trace, e
	}
	defer response.Body.Close()
	body, e := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if e != nil || len(body) > 2<<20 {
		return nil, trace, errors.New("M18_HTTP_RESPONSE_BOUND_EXCEEDED")
	}
	trace.Status = response.StatusCode
	trace.BodySHA256 = digest(body)
	if response.StatusCode >= 400 {
		trace.ProblemCode, e = restoredHTTPProblemCode(response.StatusCode, response.Header.Get("Content-Type"), body)
		if e != nil {
			return body, trace, e
		}
	}
	return body, trace, nil
}

func restoredHTTPProblemCode(status int, contentType string, body []byte) (string, error) {
	media, _, e := mime.ParseMediaType(contentType)
	if e != nil || media != "application/problem+json" {
		return "", errors.New("M18_HTTP_PROBLEM_MEDIA_REQUIRED")
	}
	var problem struct {
		Type   string `json:"type"`
		Code   string `json:"code"`
		Status int    `json:"status"`
	}
	if json.Unmarshal(body, &problem) != nil || problem.Type != "about:blank" || problem.Status != status || problem.Code == "" {
		return "", errors.New("M18_HTTP_VERSIONED_PROBLEM_REQUIRED")
	}
	return problem.Code, nil
}

func TestRestoredHTTPProblemDecoderUsesOriginalRFCProblemContract(t *testing.T) {
	recorder := httptest.NewRecorder()
	httpserver.Problem(recorder, "11111111-1111-4111-8111-111111111111", errors.New("AUTH_SESSION_EXPIRED"))
	code, e := restoredHTTPProblemCode(recorder.Code, recorder.Header().Get("Content-Type"), recorder.Body.Bytes())
	if e != nil || recorder.Code != http.StatusUnauthorized || code != "AUTH_SESSION_EXPIRED" {
		t.Fatal("original production problem contract", code, e)
	}
	for _, bad := range []struct {
		status int
		media  string
		body   []byte
	}{{401, "application/json", recorder.Body.Bytes()}, {503, recorder.Header().Get("Content-Type"), recorder.Body.Bytes()}, {401, "application/problem+json", []byte(`{"error":{"code":"AUTH_SESSION_EXPIRED"}}`)}} {
		if _, e = restoredHTTPProblemCode(bad.status, bad.media, bad.body); e == nil {
			t.Fatal("wrong media/status/nested shape accepted")
		}
	}
}
