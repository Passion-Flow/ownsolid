package identity_test

import (
	"context"
	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/retention"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"testing"
	"time"
)

func TestConfiguredIdentityPolicyUsesRealDatabase(t *testing.T) {
	s := fixture.New(t)
	ctx := context.Background()
	p := config.DefaultRuntimePolicy()
	p.Identity.InvitationTTLSeconds = 7200
	p.Identity.ResetTTLSeconds = 120
	p.Identity.AdminIdleSeconds = 120
	p.Identity.AdminAbsoluteSeconds = 600
	p.Identity.SessionTouchSeconds = 10
	p.Identity.IdempotencySeconds = 3600
	p.Identity.MaxLiveResetTokens = 1
	s.Config.Runtime = &p
	s.Mail.Config.Runtime = &p
	password := fixture.Bootstrap(t, s)
	a := fixture.Login(t, s, "admin@example.invalid", password, "admin")
	var lifespan float64
	if err := s.DB.QueryRow(ctx, "SELECT extract(epoch FROM absolute_expires_at-created_at) FROM sessions WHERE id=$1", a.SessionID).Scan(&lifespan); err != nil {
		t.Fatal(err)
	}
	if lifespan < 599 || lifespan > 601 {
		t.Fatal("configured session lifetime not used", lifespan)
	}
	invite, err := s.Invite(ctx, a, "", "createInvitation", uuid.NewString(), "policy@example.invalid", "en", 0, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if remaining := time.Until(invite.ExpiresAt); remaining < 7190*time.Second || remaining > 7201*time.Second {
		t.Fatal("invitation TTL not configured", remaining)
	}
	if err = s.DB.QueryRow(ctx, "SELECT extract(epoch FROM expires_at-created_at) FROM idempotency_records WHERE actor_user_id=$1", a.Me.User.ID).Scan(&lifespan); err != nil {
		t.Fatal(err)
	}
	if lifespan < 3599 || lifespan > 3601 {
		t.Fatal("idempotency TTL not configured", lifespan)
	}
	if err = s.RequestReset(ctx, "admin@example.invalid", "admin", "synthetic-ip", uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.QueryRow(ctx, "SELECT extract(epoch FROM expires_at-created_at) FROM password_reset_tokens WHERE user_id=$1", a.Me.User.ID).Scan(&lifespan); err != nil {
		t.Fatal(err)
	}
	if lifespan < 119 || lifespan > 121 {
		t.Fatal("reset TTL not configured", lifespan)
	}
	// Session token is not exposed by the Auth structure; use a separate known issued token.
	_, token, _, err := s.Login(ctx, "admin@example.invalid", password, "admin", "", uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(ctx, "UPDATE sessions SET created_at=clock_timestamp()-interval '122 seconds',last_seen_at=clock_timestamp()-interval '121 seconds'"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Authenticate(ctx, token, "admin"); err == nil || err.Error() != "AUTH_SESSION_EXPIRED" {
		t.Fatal("configured idle expiry not enforced", err)
	}
}

func TestConfiguredRetentionPreservesDefaultThenHonorsOverride(t *testing.T) {
	s := fixture.New(t)
	ctx := context.Background()
	fixture.Bootstrap(t, s)
	// Rows leave with their partition, so give them one that ends between the two retentions.
	boundary := time.Now().UTC().Add(-60 * time.Hour).Format(time.RFC3339)
	nextMonth := time.Now().UTC().AddDate(0, 1, 1-time.Now().UTC().Day()).Format("2006-01") + "-01 00:00:00+00"
	for _, statement := range []string{
		"CREATE TEMP TABLE kept_audit AS SELECT * FROM audit_events",
		"ALTER TABLE audit_events DETACH PARTITION audit_events_p000000",
		"DROP TABLE audit_events_p000000",
		"CREATE TABLE audit_events_old (LIKE audit_events INCLUDING DEFAULTS INCLUDING CONSTRAINTS)",
		"ALTER TABLE audit_events ATTACH PARTITION audit_events_old FOR VALUES FROM (MINVALUE) TO ('" + boundary + "')",
		"CREATE TABLE audit_events_new (LIKE audit_events INCLUDING DEFAULTS INCLUDING CONSTRAINTS)",
		"ALTER TABLE audit_events ATTACH PARTITION audit_events_new FOR VALUES FROM ('" + boundary + "') TO ('" + nextMonth + "')",
		"INSERT INTO audit_events SELECT * FROM kept_audit",
		"UPDATE audit_events SET occurred_at=clock_timestamp()-interval '3 days'",
	} {
		if _, err := s.DB.Exec(ctx, statement); err != nil {
			t.Fatal(statement, err)
		}
	}
	if err := retention.Pass(ctx, s.DB); err != nil {
		t.Fatal(err)
	}
	if count(t, s, "SELECT count(*) FROM audit_events WHERE occurred_at<clock_timestamp()-interval '2 days'") == 0 {
		t.Fatal("default retention deleted recent audit")
	}
	policy := config.DefaultRuntimePolicy().Retention
	policy.AuditDays = 2
	policy.BatchSize = 1
	if err := retention.Pass(ctx, s.DB, policy); err != nil {
		t.Fatal(err)
	}
	if count(t, s, "SELECT count(*) FROM audit_events WHERE occurred_at<clock_timestamp()-interval '2 days'") != 0 {
		t.Fatal("configured retention did not delete expired audit")
	}
}
