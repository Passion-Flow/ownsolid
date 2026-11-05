package identity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/mail"
)

// Resending a failed email (UX-120). A finished email keeps no link: its payload is
// erased when it finishes. A resend therefore issues anew what the email carried: a new
// invitation link through the invitation's own resend, a new password reset link that
// replaces the unused one, or the same password-changed notice. Only the newest email of
// its subject can be resent, and only once it failed; a queued one still retries itself.

const resendMailDelivery = "resendMailDelivery"

// MailDelivery is one email as the delivery list shows it.
type MailDelivery struct {
	ID            string     `json:"id"`
	Purpose       string     `json:"purpose"`
	Recipient     string     `json:"recipient_email"`
	Locale        string     `json:"locale"`
	State         string     `json:"state"`
	AttemptCount  int        `json:"attempt_count"`
	NextAttemptAt *time.Time `json:"next_attempt_at"`
	ErrorCategory *string    `json:"error_category"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	CompletedAt   *time.Time `json:"completed_at"`
}

// ResendMail sends a failed email again and returns the new email. Writes go through the
// Idempotency-Key, so a retried request returns the same new email. PostgreSQL only
// (CMP-201, CMP-202).
func (s *Service) ResendMail(ctx context.Context, a Auth, id, key, request string) (MailDelivery, error) {
	if !sessionActor(a) {
		return MailDelivery{}, failure("AUTH_SESSION_EXPIRED")
	}
	if a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin {
		return MailDelivery{}, failure("ACCESS_DENIED")
	}
	if !sessionID(id) || !sessionID(key) {
		return MailDelivery{}, failure("VALIDATION_FAILED")
	}
	store, e := s.invitationStore(true)
	if e != nil {
		return MailDelivery{}, e
	}
	if store.Dialect() != transaction.PostgreSQL {
		return MailDelivery{}, failure("MAIL_RESEND_UNSUPPORTED")
	}
	ctx, cancel := s.sessionContext(ctx)
	defer cancel()
	payload, _ := json.Marshal([]any{id})
	var result MailDelivery
	e = store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		if e := s.RecheckWithin(ctx, tx, a); e != nil {
			return e
		}
		return s.idempotentWithin(ctx, tx, a.Me.User.ID, resendMailDelivery, key, payload, 200, &result, func() error {
			next, e := s.resendWithin(ctx, tx, a, id, request)
			if e != nil {
				return e
			}
			result, e = mailDeliveryWithin(ctx, tx, next)
			return e
		})
	})
	if e != nil {
		return MailDelivery{}, sessionError(e)
	}
	return result, nil
}

// resendWithin queues the replacement of a failed email and returns its ID.
func (s *Service) resendWithin(ctx context.Context, tx transaction.Executor, a Auth, id, request string) (string, error) {
	var purpose, state string
	var invitation, resetToken, user *string
	e := tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT purpose,state,invitation_id::text,reset_token_id::text,user_id::text FROM mail_deliveries WHERE id=$1 FOR UPDATE`, id)).Scan(&purpose, &state, &invitation, &resetToken, &user)
	if errors.Is(e, sql.ErrNoRows) {
		return "", failure("RESOURCE_NOT_FOUND")
	}
	if e != nil {
		return "", e
	}
	// A newer email of the same subject replaced this one, so its link is the live one.
	var superseded bool
	if e = tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT EXISTS(SELECT 1 FROM mail_deliveries d JOIN mail_deliveries o ON o.id<>d.id AND o.purpose=d.purpose AND (o.created_at,o.id)>(d.created_at,d.id) AND (o.invitation_id=d.invitation_id OR (d.invitation_id IS NULL AND o.user_id=d.user_id)) WHERE d.id=$1)`, id)).Scan(&superseded); e != nil {
		return "", e
	}
	if state != "failed" || superseded {
		return "", failure("MAIL_DELIVERY_NOT_RESENDABLE")
	}
	var next string
	switch {
	case purpose == "invitation" && invitation != nil:
		// The invitation's resend checks that it can still be accepted and rate-limits.
		var version int64
		if e = tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT version FROM invitations WHERE id=$1`, *invitation)).Scan(&version); e != nil {
			return "", e
		}
		var reissued Invitation
		if e = s.inviteWithin(ctx, tx, a, resendInvitation, *invitation, "", "", version, request, &reissued); e != nil {
			return "", e
		}
		if reissued.Delivery == nil {
			return "", failure("STORAGE_UNAVAILABLE")
		}
		next = reissued.Delivery.ID
	case purpose == "password_reset" && resetToken != nil && user != nil:
		if e = s.admitResend(ctx, tx, a); e != nil {
			return "", e
		}
		next, e = s.reissueResetWithin(ctx, tx, *user, *resetToken)
	case purpose == "password_changed" && user != nil:
		if e = s.admitResend(ctx, tx, a); e != nil {
			return "", e
		}
		var email, locale string
		e = tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT email_canonical,locale FROM users WHERE id=$1 AND status='active'`, *user)).Scan(&email, &locale)
		if errors.Is(e, sql.ErrNoRows) {
			return "", failure("MAIL_DELIVERY_NOT_RESENDABLE")
		}
		if e == nil {
			var sent mail.Summary
			sent, e = s.Mail.EnqueueWithin(ctx, tx, mail.Input{Purpose: purpose, Recipient: email, Locale: locale, UserID: *user, Audience: "portal"})
			next = sent.ID
		}
	default:
		return "", failure("STORAGE_UNAVAILABLE")
	}
	if e != nil {
		return "", e
	}
	return next, audit.WriteWithin(ctx, tx, a.Me.User.ID, "", "admin", "mail.resent", "success", "mail_delivery", id, "", request)
}

// reissueResetWithin replaces the unused token of a failed reset email with a new one, sent
// to the account's current address. A disabled account, or one that lost the right to the
// entrance the link was for, gets nothing.
func (s *Service) reissueResetWithin(ctx context.Context, tx transaction.Executor, user, token string) (string, error) {
	var email, locale, audience string
	e := tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT u.email_canonical,u.locale,t.audience FROM password_reset_tokens t JOIN users u ON u.id=t.user_id WHERE t.id=$1 AND u.id=$2 AND u.status='active' AND (t.audience='portal' OR u.is_platform_admin) FOR UPDATE OF u,t`, token, user)).Scan(&email, &locale, &audience)
	if errors.Is(e, sql.ErrNoRows) {
		return "", failure("MAIL_DELIVERY_NOT_RESENDABLE")
	}
	if e != nil {
		return "", e
	}
	if _, e = tx.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `UPDATE password_reset_tokens SET token_hash=NULL,revoked_at=clock_timestamp() WHERE id=$1 AND consumed_at IS NULL AND revoked_at IS NULL`, token)); e != nil {
		return "", e
	}
	return s.issueResetWithin(ctx, tx, user, audience, email, locale)
}

// admitResend applies the administrator's invitation rate to resends of other emails.
func (s *Service) admitResend(ctx context.Context, tx transaction.Executor, a Auth) error {
	rates := s.Config.Policy().Rates
	_, e := s.AdmitWithin(ctx, tx, Limit{Purpose: "admin.mail_resend", Identifier: a.Me.User.ID, Count: rates.AdminInviteCount, Window: config.Seconds(rates.AdminInviteWindowSeconds)})
	return e
}

func mailDeliveryWithin(ctx context.Context, tx transaction.Executor, id string) (MailDelivery, error) {
	var d MailDelivery
	e := tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT id::text,purpose,recipient_email,locale,state,attempt_count,CASE WHEN state='queued' THEN next_attempt_at END,error_category,created_at,updated_at,completed_at FROM mail_deliveries WHERE id=$1`, id)).Scan(&d.ID, &d.Purpose, &d.Recipient, &d.Locale, &d.State, &d.AttemptCount, &d.NextAttemptAt, &d.ErrorCategory, &d.CreatedAt, &d.UpdatedAt, &d.CompletedAt)
	d.CreatedAt, d.UpdatedAt = d.CreatedAt.UTC(), d.UpdatedAt.UTC()
	return d, e
}
