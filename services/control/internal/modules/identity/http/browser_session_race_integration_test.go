package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/cellroute"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type browserRaceResponse struct {
	status  int
	body    []byte
	cookies []*http.Cookie
	err     error
}

func browserRaceMutation(ctx context.Context, c *client, method, path string, body []byte) browserRaceResponse {
	r, err := http.NewRequestWithContext(ctx, method, c.s.URL+cellroute.Public(path, wire.LocalScope()), bytes.NewReader(body))
	if err != nil {
		return browserRaceResponse{err: err}
	}
	r.Host = "admin.example.invalid"
	r.Header.Set("Origin", "https://"+r.Host)
	r.Header.Set("X-CSRF-Token", c.csrf)
	if len(body) != 0 {
		r.Header.Set("Content-Type", "application/json")
	}
	for _, cookie := range c.cookies {
		r.AddCookie(cookie)
	}
	res, err := c.http.Do(r)
	if err != nil {
		return browserRaceResponse{err: err}
	}
	defer res.Body.Close()
	if res.TLS == nil || len(res.TLS.VerifiedChains) == 0 {
		return browserRaceResponse{err: errors.New("expected verified HTTPS")}
	}
	raw, err := io.ReadAll(res.Body)
	return browserRaceResponse{status: res.StatusCode, body: raw, cookies: res.Cookies(), err: err}
}

func waitBrowserSessionBlock(t *testing.T, ctx context.Context, pool *pgxpool.Pool, app string, blocker int32) int32 {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var pid int32
		err := pool.QueryRow(ctx, `SELECT pid FROM pg_stat_activity WHERE datname=current_database()
			AND application_name=$1 AND wait_event_type='Lock' AND $2=ANY(pg_blocking_pids(pid))`, app, blocker).Scan(&pid)
		if err == nil {
			t.Logf("observed %s blocked by PostgreSQL PID %d", app, blocker)
			return pid
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("cannot observe %s waiting on the expected PostgreSQL lock: %v", app, err)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("%s did not wait on the expected PostgreSQL lock", app)
		case <-ticker.C:
		}
	}
}

func TestBrowserSessionRevocationPasswordChangeRaceHTTPS(t *testing.T) {
	for _, scenario := range []struct {
		name        string
		changeFirst bool
	}{
		{name: "revocation first"},
		{name: "password change first", changeFirst: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			policy := config.DefaultRuntimePolicy()
			policy.HTTP.RequestSeconds = 20
			s, _, portal, template, password := setup(t, policy)
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			const email, managerApp, deviceApp = "admin@example.invalid", "iam111-manager", "iam111-device"
			const cookieName = "__Host-admin_session"
			browser := func(app string) *client {
				t.Helper()
				pg := s.DB.Config()
				pg.ConnConfig.RuntimeParams["application_name"] = app
				pool, err := pgxpool.NewWithConfig(ctx, pg)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(pool.Close)
				replica := *s
				replica.DB = pool
				return newClient(t, accountTLSServer(t, transport.New(&replica)), "admin", template.schemas)
			}
			manager, device := browser(managerApp), browser(deviceApp)
			login := map[string]string{"email": email, "password": password}
			manager.mutate(http.MethodPost, "/api/v1/auth/login", login, 200, "LoginResponse")
			me := device.mutate(http.MethodPost, "/api/v1/auth/login", login, 200, "LoginResponse")
			userID := me["user"].(map[string]any)["id"].(string)
			portal.mutate(http.MethodPost, "/api/v1/auth/login", login, 200, "LoginResponse")
			managerID, deviceID := ownSession(t, manager), ownSession(t, device)
			manager.refresh()
			device.refresh()

			const credentialQuery = `SELECT u.auth_epoch,c.version FROM users u JOIN password_credentials c ON c.user_id=u.id WHERE u.id=$1`
			var originalEpoch, originalVersion int64
			if err := s.DB.QueryRow(ctx, credentialQuery, userID).Scan(&originalEpoch, &originalVersion); err != nil {
				t.Fatal(err)
			}
			hold, err := s.DB.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer hold.Rollback(context.Background())
			var holdPID int32
			if err := hold.QueryRow(ctx, "SELECT pg_backend_pid() FROM sessions WHERE id=$1 FOR UPDATE", deviceID).Scan(&holdPID); err != nil {
				t.Fatal(err)
			}
			next := fixture.Password()
			body, err := json.Marshal(map[string]string{"current_password": password, "new_password": next})
			if err != nil {
				t.Fatal(err)
			}
			revocation := func() browserRaceResponse {
				return browserRaceMutation(ctx, manager, http.MethodDelete, managedSessionsPath+"/"+deviceID, nil)
			}
			change := func() browserRaceResponse {
				return browserRaceMutation(ctx, device, http.MethodPost, passwordChanges, body)
			}
			first, second, firstApp, secondApp := revocation, change, managerApp, deviceApp
			if scenario.changeFirst {
				first, second, firstApp, secondApp = change, revocation, deviceApp, managerApp
			}
			firstResult := make(chan browserRaceResponse, 1)
			secondResult := make(chan browserRaceResponse, 1)
			go func() { firstResult <- first() }()
			firstPID := waitBrowserSessionBlock(t, ctx, s.DB, firstApp, holdPID)
			go func() { secondResult <- second() }()
			waitBrowserSessionBlock(t, ctx, s.DB, secondApp, firstPID)
			if err := hold.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			winner, loser := <-firstResult, <-secondResult
			if winner.err != nil || loser.err != nil {
				t.Fatalf("requests failed on the wire: %v, %v", winner.err, loser.err)
			}
			var problem struct {
				Code string `json:"code"`
			}
			if loser.status != http.StatusUnauthorized || json.Unmarshal(loser.body, &problem) != nil ||
				problem.Code != "AUTH_SESSION_EXPIRED" || len(loser.cookies) != 0 {
				t.Fatalf("stale operation returned %d/%s or changed cookies", loser.status, problem.Code)
			}

			wantEpoch, wantVersion := originalEpoch, originalVersion
			wantLogout, wantChange := 1, 0
			wantDevice, wantManager := "session_revoked", ""
			if scenario.changeFirst {
				wantEpoch, wantVersion = originalEpoch+1, originalVersion+1
				wantLogout, wantChange = 0, 1
				wantDevice, wantManager = "password_changed", "password_changed"
				if winner.status != http.StatusOK || len(winner.cookies) != 1 || winner.cookies[0].Name != cookieName ||
					!winner.cookies[0].Secure || !winner.cookies[0].HttpOnly || winner.cookies[0].Value == device.cookies[cookieName].Value {
					t.Fatal("committed password change did not return one fresh Admin cookie")
				}
				problemCode(t, manager.request(http.MethodGet, managedSessionsPath, nil, 401, "Problem", nil), "AUTH_SESSION_EXPIRED")
				problemCode(t, portal.request(http.MethodGet, managedSessionsPath, nil, 401, "Problem", nil), "AUTH_SESSION_EXPIRED")
			} else {
				if winner.status != http.StatusNoContent || len(winner.body) != 0 || len(winner.cookies) != 0 {
					t.Fatal("committed targeted revoke did not return a bodyless 204")
				}
				if ownSession(t, manager) != managerID {
					t.Fatal("targeted revocation ended the manager's session")
				}
				ownSession(t, portal)
			}
			problemCode(t, device.request(http.MethodGet, managedSessionsPath, nil, 401, "Problem", nil), "AUTH_SESSION_EXPIRED")
			if scenario.changeFirst {
				device.cookies[cookieName] = winner.cookies[0]
				if ownSession(t, device) == deviceID {
					t.Fatal("password change retained the pre-rotation session")
				}
			}
			var epoch, version int64
			if err := s.DB.QueryRow(ctx, credentialQuery, userID).Scan(&epoch, &version); err != nil || epoch != wantEpoch || version != wantVersion {
				t.Fatalf("unexpected credential or epoch change: %d/%d, %v", epoch, version, err)
			}
			var deviceReason, managerReason string
			if err := s.DB.QueryRow(ctx, `SELECT COALESCE(target.revoke_reason,''),COALESCE(manager.revoke_reason,'')
				FROM sessions target CROSS JOIN sessions manager WHERE target.id=$1 AND manager.id=$2`,
				deviceID, managerID).Scan(&deviceReason, &managerReason); err != nil || deviceReason != wantDevice || managerReason != wantManager {
				t.Fatalf("unexpected persisted session outcomes: %s/%s, %v", deviceReason, managerReason, err)
			}
			var logouts, changes int
			if err := s.DB.QueryRow(ctx, `SELECT count(*) FILTER(WHERE action='auth.logout' AND target_id=$2 AND reason='session_revoked'),
				count(*) FILTER(WHERE action='auth.password_changed') FROM audit_events WHERE actor_user_id=$1 AND audience='admin'`,
				userID, deviceID).Scan(&logouts, &changes); err != nil || logouts != wantLogout || changes != wantChange {
				t.Fatalf("unexpected audit outcomes: %d/%d, %v", logouts, changes, err)
			}

			confirm := newClient(t, device.s, "admin", device.schemas)
			expected, obsolete := password, next
			if scenario.changeFirst {
				expected, obsolete = next, password
			}
			problemCode(t, confirm.mutate(http.MethodPost, "/api/v1/auth/login", map[string]string{"email": email, "password": obsolete}, 401, "Problem"), "AUTH_INVALID_CREDENTIALS")
			confirm.mutate(http.MethodPost, "/api/v1/auth/login", map[string]string{"email": email, "password": expected}, 200, "LoginResponse")
		})
	}
}
