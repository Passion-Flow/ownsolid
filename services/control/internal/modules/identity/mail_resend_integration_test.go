package identity_test

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

// failDelivery finishes an email the way the outbox does when it gives up.
func failDelivery(t *testing.T, s *identity.Service, id string) {
	t.Helper()
	if _, e := s.DB.Exec(t.Context(), `UPDATE mail_deliveries SET state='failed',error_category='smtp_unavailable',attempt_count=5,payload_key_id=NULL,payload_nonce=NULL,payload_ciphertext=NULL,completed_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1`, id); e != nil {
		t.Fatal(e)
	}
}

func latestDelivery(t *testing.T, s *identity.Service, purpose string) string {
	t.Helper()
	var id string
	if e := s.DB.QueryRow(t.Context(), `SELECT id::text FROM mail_deliveries WHERE purpose=$1 ORDER BY created_at DESC,id DESC LIMIT 1`, purpose).Scan(&id); e != nil {
		t.Fatal(e)
	}
	return id
}

func requireProblem(t *testing.T, e error, code string) {
	t.Helper()
	if e == nil || e.Error() != code {
		t.Fatalf("expected %s, got %v", code, e)
	}
}

func TestResendMailReissuesWhatAFailedEmailCarried(t *testing.T) {
	s := fixture.New(t)
	password := fixture.Bootstrap(t, s)
	admin := fixture.Login(t, s, "admin@example.invalid", password, "admin")
	ctx := context.Background()

	t.Run("invitation gets a new link once, and retries return the same email", func(t *testing.T) {
		invited, e := s.Invite(ctx, admin, "", "createInvitation", uuid.NewString(), "resend-invite@example.invalid", "en", 0, uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		failed := invited.Delivery.ID
		_, e = s.ResendMail(ctx, admin, failed, uuid.NewString(), uuid.NewString())
		requireProblem(t, e, "MAIL_DELIVERY_NOT_RESENDABLE") // still queued: the outbox retries it
		failDelivery(t, s, failed)

		key := uuid.NewString()
		results := make(chan identity.MailDelivery, 2)
		errs := make(chan error, 2)
		var wg sync.WaitGroup
		for range 2 {
			wg.Go(func() {
				d, e := s.ResendMail(ctx, admin, failed, key, uuid.NewString())
				results <- d
				errs <- e
			})
		}
		wg.Wait()
		close(results)
		close(errs)
		var ids []string
		for e := range errs {
			if e != nil {
				t.Fatal("same key must replay, not fail", e)
			}
		}
		for d := range results {
			ids = append(ids, d.ID)
			if d.State != "queued" || d.Purpose != "invitation" || d.Recipient != "resend-invite@example.invalid" {
				t.Fatal("new email", d)
			}
		}
		if ids[0] != ids[1] || ids[0] == failed {
			t.Fatal("one new email per key", ids, failed)
		}
		var generation int
		if e = s.DB.QueryRow(ctx, `SELECT token_generation FROM invitations WHERE id=$1`, invited.ID).Scan(&generation); e != nil || generation != 2 {
			t.Fatal("invitation link not reissued", generation, e)
		}
		_, e = s.ResendMail(ctx, admin, failed, uuid.NewString(), uuid.NewString())
		requireProblem(t, e, "MAIL_DELIVERY_NOT_RESENDABLE") // superseded by the new email
		_, e = s.ResendMail(ctx, admin, failed, key, uuid.NewString())
		if e != nil {
			t.Fatal("replay after the write", e)
		}
	})

	t.Run("a revoked invitation is not sent again", func(t *testing.T) {
		invited, e := s.Invite(ctx, admin, "", "createInvitation", uuid.NewString(), "resend-revoked@example.invalid", "en", 0, uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		failDelivery(t, s, invited.Delivery.ID)
		var version int64
		if e = s.DB.QueryRow(ctx, `SELECT version FROM invitations WHERE id=$1`, invited.ID).Scan(&version); e != nil {
			t.Fatal(e)
		}
		if _, e = s.RevokeInvitation(ctx, admin, invited.ID, version, "Synthetic revocation", uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		_, e = s.ResendMail(ctx, admin, invited.Delivery.ID, uuid.NewString(), uuid.NewString())
		requireProblem(t, e, "INVITATION_STATE_CONFLICT")
	})

	t.Run("password reset replaces the unused link", func(t *testing.T) {
		if e := s.RequestReset(ctx, "admin@example.invalid", "admin", "192.0.2.10", uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		failed := latestDelivery(t, s, "password_reset")
		failDelivery(t, s, failed)
		var oldToken string
		if e := s.DB.QueryRow(ctx, `SELECT reset_token_id::text FROM mail_deliveries WHERE id=$1`, failed).Scan(&oldToken); e != nil {
			t.Fatal(e)
		}
		d, e := s.ResendMail(ctx, admin, failed, uuid.NewString(), uuid.NewString())
		if e != nil || d.Purpose != "password_reset" || d.State != "queued" {
			t.Fatal(e, d)
		}
		var oldRevoked, newLive bool
		if e = s.DB.QueryRow(ctx, `SELECT (SELECT revoked_at IS NOT NULL FROM password_reset_tokens WHERE id=$1),(SELECT t.token_hash IS NOT NULL AND t.audience='admin' FROM mail_deliveries m JOIN password_reset_tokens t ON t.id=m.reset_token_id WHERE m.id=$2)`, oldToken, d.ID).Scan(&oldRevoked, &newLive); e != nil || !oldRevoked || !newLive {
			t.Fatal("reset link not replaced", oldRevoked, newLive, e)
		}
		var audits int
		if e = s.DB.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='mail.resent' AND target_id=$1`, failed).Scan(&audits); e != nil || audits != 1 {
			t.Fatal("resend not audited", audits, e)
		}
	})

	t.Run("password-changed notice is sent again", func(t *testing.T) {
		failed := uuid.NewString()
		if _, e := s.DB.Exec(ctx, `INSERT INTO mail_deliveries(id,purpose,recipient_email,locale,user_id,state,attempt_count,next_attempt_at,error_category,message_id,created_at,updated_at,completed_at) SELECT $1::uuid,'password_changed',email_canonical,locale,id,'failed',5,clock_timestamp(),'smtp_auth_rejected',$1::text||'@example.invalid',clock_timestamp(),clock_timestamp(),clock_timestamp() FROM users WHERE email_canonical='admin@example.invalid'`, failed); e != nil {
			t.Fatal(e)
		}
		d, e := s.ResendMail(ctx, admin, failed, uuid.NewString(), uuid.NewString())
		if e != nil || d.Purpose != "password_changed" || d.State != "queued" || d.Recipient != "admin@example.invalid" {
			t.Fatal(e, d)
		}
	})

	t.Run("refusals", func(t *testing.T) {
		_, e := s.ResendMail(ctx, admin, uuid.NewString(), uuid.NewString(), uuid.NewString())
		requireProblem(t, e, "RESOURCE_NOT_FOUND")
		_, e = s.ResendMail(ctx, admin, "not-a-uuid", uuid.NewString(), uuid.NewString())
		requireProblem(t, e, "VALIDATION_FAILED")
		portal := fixture.Login(t, s, "admin@example.invalid", password, "portal")
		_, e = s.ResendMail(ctx, portal, uuid.NewString(), uuid.NewString(), uuid.NewString())
		requireProblem(t, e, "ACCESS_DENIED")
	})
}
