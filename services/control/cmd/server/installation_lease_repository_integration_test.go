//go:build repository_integration

package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/mail"
)

type leaseServingGeneration struct {
	cancel context.CancelFunc
	done   chan struct{}
	result error
}

func startLeaseServingGeneration(t *testing.T, x *serverCompositionStory) *leaseServingGeneration {
	t.Helper()
	ctx, cancel := context.WithCancel(x.ctx)
	generation := &leaseServingGeneration{cancel: cancel, done: make(chan struct{})}
	go func() {
		startup, stop := context.WithTimeout(ctx, config.Seconds(x.c.Policy().HTTP.StartupSeconds))
		defer stop()
		generation.result = serveSelected(ctx, startup, x.c, x.d, x.owner, x.mail)
		close(generation.done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-generation.done:
		case <-time.After(20 * time.Second):
			t.Error("selected serving cleanup did not join")
		}
	})
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-generation.done:
			cancel()
			t.Fatal("selected serving generation refused", generation.result)
		case <-deadline.C:
			cancel()
			t.Fatal("selected serving generation did not listen")
		case <-tick.C:
			req, _ := http.NewRequestWithContext(x.ctx, "GET", x.c.Server.PortalOrigin+"/health/live", nil)
			res, e := x.client.Do(req)
			if e == nil {
				_, _ = io.Copy(io.Discard, res.Body)
				res.Body.Close()
				if res.StatusCode == 200 {
					return generation
				}
			}
		}
	}
}

func TestActualPostgreSQLRestartCannotResumeLostInstallationOwner(t *testing.T) {
	testActualPostgreSQLRestartInstallationOwner(t, false)
}

func TestActualPostgreSQLIdleLeaseLossJoinsBackgroundOwnersWithoutRequests(t *testing.T) {
	testActualPostgreSQLRestartInstallationOwner(t, true)
}

func testActualPostgreSQLRestartInstallationOwner(t *testing.T, idle bool) {
	container := os.Getenv("PROJECT_ONE_TEST_LEASE_POSTGRES_CONTAINER")
	if os.Getenv("PROJECT_ONE_REPOSITORY_ENGINE") != "postgresql" || !strings.HasPrefix(container, "p1-20261103-platform-composition-") {
		t.Fatal("EXACT_OWNED_POSTGRES_RESTART_FIXTURE_REQUIRED")
	}
	x := serverCompositionSetup(t, false)
	if os.Getenv("GOTMPDIR") != "" || os.Getenv("TMPDIR") == "" {
		t.Fatal("DURABLE_RUNTIME_TMP_WITHOUT_COMPILER_OVERRIDE_REQUIRED")
	}
	if relative, e := filepath.Rel(os.Getenv("TMPDIR"), x.root); e != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		t.Fatal("runtime t.TempDir escaped its declared durable root")
	}
	t.Logf("ACTUAL_RUNTIME_TEMP %s", x.root)
	policy := x.c.Policy()
	policy.HTTP.InstallationLeaseIntervalSeconds = 1
	x.c.Runtime = &policy
	x.owner.Config, x.mail.Config = x.c, x.c
	if e := x.d.Verify(x.ctx); e != nil {
		t.Fatal("initial full selected admission failed", e)
	}
	cookiePolicy, e := httpserver.NewCookiePolicy(x.c.Server.EffectiveTransportMode())
	if e != nil {
		t.Fatal(e)
	}
	cookies := map[string][]*http.Cookie{}
	for _, audience := range []string{"portal", "admin"} {
		var issued identity.IssuedSession
		e = x.d.store.Within(x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
			var issueError error
			issued, issueError = x.owner.IssueSessionWithin(ctx, q, x.f.User, 1, audience, "", uuid.NewString(), nil)
			return issueError
		})
		if e != nil {
			t.Fatal(e)
		}
		cookies[audience] = []*http.Cookie{{Name: cookiePolicy.Name(audience, httpserver.SessionCookie), Value: issued.Token}}
	}
	original := startLeaseServingGeneration(t, x)
	for _, audience := range []string{"portal", "admin"} {
		status, body, _ := x.request(t, audience, "GET", "/api/v1/me", nil, cookies[audience], "")
		if status != 200 || body["data"] == nil {
			t.Fatal("initial actual current session failed", audience, status, body)
		}
	}
	// No readiness probe is made after this point in the old generation.
	var oldPostmaster time.Time
	if e = x.f.PostgreSQL.QueryRow(x.ctx, "SELECT pg_postmaster_start_time()").Scan(&oldPostmaster); e != nil {
		t.Fatal(e)
	}
	faultAt := time.Now()
	// The independent fixture manager may be slow after the real postmaster
	// exits. Observe product termination concurrently, without stretching its
	// configured bound or waiting for a Docker/entrypoint readiness result.
	restartCtx, stop := context.WithTimeout(x.ctx, 60*time.Second)
	command := exec.CommandContext(restartCtx, "docker", "restart", "--timeout=5", container)
	command.WaitDelay = config.Seconds(policy.HTTP.ShutdownSeconds)
	type restartOutcome struct {
		output []byte
		err    error
	}
	restartDone := make(chan restartOutcome, 1)
	go func() {
		output, restartError := command.CombinedOutput()
		restartDone <- restartOutcome{output, restartError}
	}()
	restartFinished := false
	defer func() {
		stop()
		if !restartFinished {
			select {
			case outcome := <-restartDone:
				t.Logf("OWNED_RESTART_CLIENT_CLEANUP error=%v output=%q", outcome.err, string(outcome.output))
			case <-time.After(config.Seconds(policy.HTTP.ShutdownSeconds)):
				t.Error("owned restart client did not join")
			}
		}
	}()
	bound := config.Seconds(policy.HTTP.InstallationLeaseIntervalSeconds + policy.HTTP.RequestSeconds + 2*policy.HTTP.ShutdownSeconds)
	timer := time.NewTimer(bound)
	defer timer.Stop()
	var outcome restartOutcome
	resultChannel := restartDone
	joined := false
	for !joined {
		select {
		case <-original.done:
			joined = true
		case outcome = <-resultChannel:
			restartFinished, resultChannel = true, nil
			if outcome.err != nil {
				t.Fatalf("owned PostgreSQL restart failed: %v %q", outcome.err, string(outcome.output))
			}
		case <-timer.C:
			original.cancel()
			t.Fatal("lost lease original lifecycle did not join within configured bound", bound)
		}
	}
	if e = original.result; e == nil || !strings.Contains(e.Error(), "INSTALLATION_LOCK_LOST") {
		t.Fatal("lost lease did not fail and join original lifecycle", e)
	}
	t.Logf("LEASE_LATCH_CANCEL_JOIN elapsed=%s configured_bound=%s idle=%v", time.Since(faultAt), bound, idle)
	if x.d.workers == nil {
		t.Fatal("actual background owner join signal absent")
	}
	select {
	case <-x.d.workers:
	default:
		t.Fatal("actual background owners were not joined")
	}
	if e = x.d.Verify(x.ctx); e == nil || e.Error() != "INSTALLATION_LOCK_LOST" {
		t.Fatal("original selected owner latch was cleared", e)
	}
	if !restartFinished {
		outcome = <-restartDone
		restartFinished = true
	}
	stop()
	if outcome.err != nil {
		t.Fatalf("owned PostgreSQL restart failed: %v %q", outcome.err, string(outcome.output))
	}
	t.Logf("OWNED_POSTGRES_RESTART_OUTPUT %q manager_elapsed=%s", string(outcome.output), time.Since(faultAt))
	var currentPostmaster time.Time
	deadline := time.Now().Add(15 * time.Second)
	for {
		e = x.f.PostgreSQL.QueryRow(x.ctx, "SELECT pg_postmaster_start_time()").Scan(&currentPostmaster)
		if e == nil && !currentPostmaster.Equal(oldPostmaster) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("same-data PostgreSQL did not restart")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !idle {
		for _, audience := range []string{"portal", "admin"} {
			req, _ := http.NewRequestWithContext(x.ctx, "GET", x.origin(t, audience)+"/api/v1/me", nil)
			req.Header.Set("Origin", x.origin(t, audience))
			for _, cookie := range cookies[audience] {
				req.AddCookie(cookie)
			}
			res, requestError := x.client.Do(req)
			if requestError == nil {
				raw, readError := io.ReadAll(res.Body)
				res.Body.Close()
				var body map[string]any
				if readError != nil || json.Unmarshal(raw, &body) != nil || res.StatusCode != 503 || body["data"] != nil {
					t.Fatal("old installation owner resumed after real PostgreSQL restart", audience, res.StatusCode, string(raw), readError)
				}
				t.Log("OLD_OWNER_REFUSED " + audience + " 503 noDTO")
			} else {
				t.Log("OLD_OWNER_LISTENER_CLOSED " + audience)
			}
		}
	} else {
		t.Log("IDLE_FAULT no HTTP or readiness requests after fault; actual Mail/Retention/SSO/node background owners remain lifecycle-owned")
	}
	if e = x.d.Verify(x.ctx); e == nil || e.Error() != "INSTALLATION_LOCK_LOST" {
		t.Fatal("healthy reconnected business pool erased lost owner latch", e)
	}
	original.cancel()
	conn, dialError := net.DialTimeout("tcp", x.c.Server.ListenAddress, time.Second)
	if dialError == nil {
		conn.Close()
		t.Fatal("old installation listener remained open")
	}
	if e = x.d.Close(); e == nil || e.Error() != "INSTALLATION_LOCK_RELEASE_UNCONFIRMED" {
		t.Fatal("old uncertain unlock outcome was weakened", e)
	}
	// Explicit fresh selection is the sole recovery path. Construct fresh domain
	// and outbox owners; do not reconnect/reacquire/adopt the old pinned session.
	x.d, e = openSelectedDatabase(x.ctx, x.c, "serve")
	if e != nil {
		t.Fatal("fresh explicit installation admission failed", e)
	}
	x.owner = x.d.identity(x.c)
	x.owner.HMACKey, e = config.Secret(x.c.Security.AuthHMACKeyFile)
	if e != nil {
		t.Fatal(e)
	}
	x.owner.Passwords, e = security.NewPasswords(x.c.Security.PasswordBlocklistFile, x.c.Policy().Identity)
	if e != nil {
		t.Fatal(e)
	}
	keys, e := mail.LoadKeyring(x.c.Security.MailKeyringFile)
	if e != nil {
		t.Fatal(e)
	}
	templates, e := mail.LoadTemplates()
	if e != nil {
		t.Fatal(e)
	}
	x.mail = &mail.Outbox{DB: x.d.postgres, Keys: keys, Config: x.c, Templates: templates}
	x.owner.Mail = x.mail
	x.h = identityhttp.New(x.owner)
	if e = x.h.InitializationError(); e != nil {
		t.Fatal(e)
	}
	if e = x.d.Verify(x.ctx); e != nil {
		t.Fatal("fresh full schema/lease admission failed", e)
	}
	x.client.CloseIdleConnections()
	fresh := startLeaseServingGeneration(t, x)
	for _, audience := range []string{"portal", "admin"} {
		status, body, _ := x.request(t, audience, "GET", "/api/v1/me", nil, cookies[audience], "")
		if status != 200 || body["data"] == nil {
			t.Fatal("fresh admitted owner failed current session", audience, status, body)
		}
	}
	fresh.cancel()
	select {
	case <-fresh.done:
		e = fresh.result
		if e != nil {
			t.Fatal("fresh normal lifecycle failed", e)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("fresh lifecycle did not join")
	}
	if e = x.d.Close(); e != nil {
		t.Fatal("fresh pinned installation unlock failed", e)
	}
	t.Log("ACTUAL_PG_RESTART old_owner_no_ready_probe_no200 joined_unlock_unknown_preserved fresh_full_admission_both_current_sessions200")
}
