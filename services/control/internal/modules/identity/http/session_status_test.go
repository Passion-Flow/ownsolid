package http_test

import (
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

const sessionStatus = "/api/v1/me/session"

var background = map[string]string{"X-Ownsolid-Background": "true"}

// idleLeft reads the session's remaining idle time without touching it.
func idleLeft(c *client) int {
	c.t.Helper()
	return int(c.request("GET", sessionStatus, nil, 200, "SessionStatusResponse", background)["idle_remaining_seconds"].(float64))
}

func ageSessions(t *testing.T, s *identity.Service, seconds int) {
	t.Helper()
	if _, e := s.DB.Exec(t.Context(), `UPDATE sessions SET created_at=clock_timestamp()-2*$1::bigint*interval '1 second',last_seen_at=clock_timestamp()-$1::bigint*interval '1 second'`, seconds); e != nil {
		t.Fatal(e)
	}
}

func TestOnlyRequestsAPersonMadeExtendTheSession(t *testing.T) {
	s, _, portal, _, password := setup(t)
	portal.mutate("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "LoginResponse")
	idle := s.Config.Policy().Identity.PortalIdleSeconds
	const aged = 300
	ageSessions(t, s, aged)
	expected := idle - aged
	near := func(got, want int) bool { return got <= want && got >= want-5 }

	for _, path := range []string{"/api/v1/me", "/api/v1/notifications/configuration", "/api/v1/license/status", sessionStatus} {
		portal.request("GET", path, nil, 200, "", background)
		if left := idleLeft(portal); !near(left, expected) {
			t.Fatalf("a background read of %s moved the session: %d left, want about %d", path, left, expected)
		}
	}
	portal.request("GET", "/api/v1/me", nil, 200, "MeResponse", map[string]string{"X-Ownsolid-Background": "false"})
	if left := idleLeft(portal); !near(left, idle) {
		t.Fatalf("an unmarked read is activity: %d left, want about %d", left, idle)
	}

	ageSessions(t, s, aged)
	me := portal.request("GET", "/api/v1/me", nil, 200, "MeResponse", background)
	version := me["user"].(map[string]any)["version"]
	portal.refresh()
	portal.request("PATCH", "/api/v1/me", map[string]any{"display_name": "Marked write", "locale": "en", "version": version}, 200, "MeResponse", background)
	if left := idleLeft(portal); !near(left, idle) {
		t.Fatalf("a write is activity whatever it claims: %d left, want about %d", left, idle)
	}

	// An open tab polling in the background does not outlive the idle timeout.
	ageSessions(t, s, idle+1)
	problemCode(t, portal.request("GET", "/api/v1/notifications/configuration", nil, 401, "", background), "AUTH_SESSION_EXPIRED")
}

func TestSessionStatusReportsWhatIsLeft(t *testing.T) {
	policy := config.DefaultRuntimePolicy()
	policy.Identity.AdminIdleSeconds, policy.Identity.SessionTouchSeconds, policy.Identity.SessionWarningSeconds = 300, 30, 120
	_, _, _, admin, password := setup(t, policy)
	admin.request("GET", sessionStatus, nil, 401, "Problem", nil)
	admin.mutate("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "LoginResponse")
	status := admin.request("GET", sessionStatus, nil, 200, "SessionStatusResponse", nil)
	if status["idle_timeout_seconds"] != float64(300) || status["warning_seconds"] != float64(120) {
		t.Fatal("configured idle timeout and warning", status)
	}
	if left := status["absolute_remaining_seconds"].(float64); left > float64(policy.Identity.AdminAbsoluteSeconds) || left < float64(policy.Identity.AdminAbsoluteSeconds-5) {
		t.Fatal("absolute time left", status)
	}
}

func TestSessionWarningFitsAShortIdleTimeout(t *testing.T) {
	for _, c := range []struct{ idle, touch, want int }{{120, 60, 60}, {120, 100, 20}, {60, 30, 30}, {900, 60, 120}} {
		policy := config.DefaultRuntimePolicy()
		policy.Identity.PortalIdleSeconds, policy.Identity.SessionTouchSeconds = c.idle, c.touch
		_, _, portal, _, password := setup(t, policy)
		portal.mutate("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "LoginResponse")
		if got := portal.request("GET", sessionStatus, nil, 200, "SessionStatusResponse", nil)["warning_seconds"]; got != float64(c.want) {
			t.Fatalf("idle %d touch %d: warn %v seconds before, want %d", c.idle, c.touch, got, c.want)
		}
	}
}
