package identity

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type backgroundReadKey struct{}

// WithoutSessionTouch marks a read nobody asked for, such as a page's poll. It still
// authenticates, but it does not count as activity, so an open tab cannot hold a session
// past its idle timeout (ADR 0024).
func WithoutSessionTouch(ctx context.Context) context.Context {
	return context.WithValue(ctx, backgroundReadKey{}, true)
}

func touchesSession(ctx context.Context) bool {
	marked, _ := ctx.Value(backgroundReadKey{}).(bool)
	return !marked
}

// SessionStatus says how long a session has left. Durations rather than times, so a
// browser with a wrong clock still counts down correctly.
type SessionStatus struct {
	IdleTimeoutSeconds       int `json:"idle_timeout_seconds"`
	IdleRemainingSeconds     int `json:"idle_remaining_seconds"`
	AbsoluteRemainingSeconds int `json:"absolute_remaining_seconds"`
	WarningSeconds           int `json:"warning_seconds"`
}

// warningWindow is how long before the idle timeout to warn: the configured time, or less
// when the idle timeout is short. It always leaves the touch interval before the warning,
// so a request made during the warning moves the session's last activity.
func warningWindow(configured, idle, touch int) int {
	return max(1, min(configured, idle/2, idle-touch))
}

// SessionStatus reads what is left of the authenticated session.
func (s *Service) SessionStatus(ctx context.Context, a Auth) (SessionStatus, error) {
	var out SessionStatus
	if !sessionActor(a) {
		return out, failure("AUTH_SESSION_EXPIRED")
	}
	store, e := s.Repository()
	if e != nil {
		return out, e
	}
	ctx, cancel := s.sessionContext(ctx)
	defer cancel()
	var lastSeen, absolute, now time.Time
	e = store.QueryRow(ctx, identitySQL(store,
		"SELECT last_seen_at,absolute_expires_at,clock_timestamp() FROM sessions WHERE id=$1 AND revoked_at IS NULL",
		"SELECT last_seen_at,absolute_expires_at,UTC_TIMESTAMP(6) FROM sessions WHERE id=? AND revoked_at IS NULL",
		`SELECT LAST_SEEN_AT,ABSOLUTE_EXPIRES_AT,CURRENT_TIMESTAMP(6) FROM SESSIONS WHERE ID=? AND REVOKED_AT IS NULL`, a.SessionID)).Scan(&lastSeen, &absolute, &now)
	if errors.Is(e, sql.ErrNoRows) {
		return out, failure("AUTH_SESSION_EXPIRED")
	}
	if e != nil {
		return out, sessionError(e)
	}
	idle := s.Idle(a.Me.Audience)
	seconds := func(left time.Duration) int { return max(0, int(left/time.Second)) }
	policy := s.Config.Policy().Identity
	return SessionStatus{
		IdleTimeoutSeconds:       int(idle / time.Second),
		IdleRemainingSeconds:     seconds(lastSeen.Add(idle).Sub(now)),
		AbsoluteRemainingSeconds: seconds(absolute.Sub(now)),
		WarningSeconds:           warningWindow(policy.SessionWarningSeconds, int(idle/time.Second), policy.SessionTouchSeconds),
	}, nil
}
