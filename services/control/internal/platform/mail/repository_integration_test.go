//go:build repository_integration

package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net"
	stdmail "net/mail"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/smtpsink"
)

type mailFixture struct {
	repositoryfixture.Fixture
	outbox *Outbox
}

func newMailFixture(t *testing.T) mailFixture {
	t.Helper()
	f := mailFixture{Fixture: repositoryfixture.New(t)}
	key := make([]byte, 32)
	if _, e := rand.Read(key); e != nil {
		t.Fatal("synthetic key")
	}
	raw, _ := json.Marshal(Keyring{Active: "synthetic", Keys: map[string]string{"synthetic": base64.StdEncoding.EncodeToString(key)}})
	path := filepath.Join(t.TempDir(), "synthetic-keyring.json")
	if e := os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	ring, e := LoadKeyring(path)
	if e != nil {
		t.Fatal(e)
	}
	templates, e := LoadTemplates()
	if e != nil {
		t.Fatal(e)
	}
	f.outbox = &Outbox{Storage: f.Store, Keys: ring, Templates: templates}
	f.outbox.Config.Server.PortalOrigin = "https://portal.example.invalid"
	f.outbox.Config.Server.AdminOrigin = "https://admin.example.invalid"
	f.outbox.Config.SMTP.FromAddress = "platform@example.invalid"
	return f
}
func mailContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	return ctx
}
func (f mailFixture) exec(t *testing.T, pg, my string, args ...any) {
	t.Helper()
	if _, e := f.Store.Exec(mailContext(t), f.Statement(pg, my, args...)); e != nil {
		t.Fatal("actual synthetic SQL", e)
	}
}
func (f mailFixture) seed(t *testing.T, purpose string) Input {
	t.Helper()
	i := Input{Purpose: purpose, Recipient: "recipient@example.invalid", Locale: "en", UserID: f.User, Audience: "portal", Token: "synthetic-token-" + uuid.NewString()}
	hash := sha256.Sum256([]byte(i.Token))
	switch purpose {
	case "invitation":
		i.InvitationID = uuid.NewString()
		i.UserID = ""
		i.Generation = 1
		f.exec(t, `INSERT INTO invitations(id,tenant_id,workspace_id,email_canonical,locale,status,token_hash,created_by,created_at,updated_at,expires_at) VALUES($1,$2,$3,$4,'en','pending',$5,$6,clock_timestamp(),clock_timestamp(),clock_timestamp()+interval '1 hour')`, `INSERT INTO invitations(id,tenant_id,workspace_id,email_canonical,locale,status,token_hash,created_by,created_at,updated_at,expires_at) VALUES(?,?,?,?,'en','pending',?,?,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6),TIMESTAMPADD(HOUR,1,UTC_TIMESTAMP(6)))`, i.InvitationID, f.Tenant, f.Workspace, uuid.NewString()+"@example.invalid", hash[:], f.User)
	case "password_reset":
		i.ResetID = uuid.NewString()
		f.exec(t, `INSERT INTO password_reset_tokens(id,user_id,audience,token_hash,created_at,expires_at) VALUES($1,$2,'portal',$3,clock_timestamp(),clock_timestamp()+interval '1 hour')`, `INSERT INTO password_reset_tokens(id,user_id,audience,token_hash,created_at,expires_at) VALUES(?,?,'portal',?,UTC_TIMESTAMP(6),TIMESTAMPADD(HOUR,1,UTC_TIMESTAMP(6)))`, i.ResetID, f.User, hash[:])
	case "password_changed":
		i.Token = ""
	default:
		t.Fatal("unsupported synthetic purpose")
	}
	return i
}
func (f mailFixture) enqueue(t *testing.T, i Input) Summary {
	t.Helper()
	var summary Summary
	e := f.Store.Within(mailContext(t), false, func(ctx context.Context, tx transaction.Executor) error {
		var e error
		summary, e = f.outbox.EnqueueWithin(ctx, tx, i)
		return e
	})
	if e != nil {
		t.Fatal("actual enqueue", e)
	}
	return summary
}
func (f mailFixture) state(t *testing.T, id, wanted string, attempts int, category string, terminal bool) {
	t.Helper()
	var state, gotCategory string
	var count int
	var nonceNull, cipherNull, leaseNull, complete bool
	e := f.Store.QueryRow(mailContext(t), f.Statement(`SELECT state,attempt_count,COALESCE(error_category,''),payload_nonce IS NULL,payload_ciphertext IS NULL,lease_token IS NULL,completed_at IS NOT NULL FROM mail_deliveries WHERE id=$1`, `SELECT state,attempt_count,COALESCE(error_category,''),payload_nonce IS NULL,payload_ciphertext IS NULL,lease_token IS NULL,completed_at IS NOT NULL FROM mail_deliveries WHERE id=?`, id)).Scan(&state, &count, &gotCategory, &nonceNull, &cipherNull, &leaseNull, &complete)
	if e != nil || state != wanted || count != attempts || gotCategory != category || nonceNull != terminal || cipherNull != terminal || complete != terminal || leaseNull != (wanted != "sending") {
		t.Fatalf("actual state mismatch state=%s count=%d category=%s terminal=%t error=%v", state, count, gotCategory, terminal, e)
	}
}
func (f mailFixture) auditCount(t *testing.T, id string) int {
	t.Helper()
	var count int
	if e := f.Store.QueryRow(mailContext(t), f.Statement("SELECT count(*) FROM audit_events WHERE target_id=$1", "SELECT count(*) FROM audit_events WHERE target_id=?", id)).Scan(&count); e != nil {
		t.Fatal(e)
	}
	return count
}
func (f mailFixture) smtp(t *testing.T, implicit bool) *smtpsink.Sink {
	t.Helper()
	sink := smtpsink.Start(t, implicit, false)
	host, port, e := net.SplitHostPort(sink.Listener.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	f.outbox.Config.SMTP.Host = host
	f.outbox.Config.SMTP.Port, _ = strconv.Atoi(port)
	f.outbox.Config.SMTP.CAFile = sink.CAFile
	f.outbox.Config.SMTP.ConnectTimeoutSeconds = 1
	f.outbox.Config.SMTP.OperationTimeoutSeconds = 5
	f.outbox.Config.SMTP.TLSMode = "starttls_required"
	if implicit {
		f.outbox.Config.SMTP.TLSMode = "implicit_tls"
	}
	return sink
}
func (f mailFixture) claim(t *testing.T) claimed {
	t.Helper()
	c, found, e := f.outbox.claim(mailContext(t), f.Store)
	if e != nil || !found {
		t.Fatal("actual claim", found, e)
	}
	return c
}
func (f mailFixture) finish(t *testing.T, c claimed) {
	t.Helper()
	if e := f.outbox.finish(mailContext(t), f.Store, c, "cancelled", "token_obsolete"); e != nil {
		t.Fatal(e)
	}
}

func TestActualMailRepository(t *testing.T) {
	f := newMailFixture(t)
	t.Run("encrypted_enqueue_and_real_rollback", func(t *testing.T) {
		i := f.seed(t, "invitation")
		d := f.enqueue(t, i)
		var key, message string
		var nonce, cipher []byte
		if e := f.Store.QueryRow(mailContext(t), f.Statement("SELECT payload_key_id,payload_nonce,payload_ciphertext,message_id FROM mail_deliveries WHERE id=$1", "SELECT payload_key_id,payload_nonce,payload_ciphertext,message_id FROM mail_deliveries WHERE id=?", d.ID)).Scan(&key, &nonce, &cipher, &message); e != nil {
			t.Fatal(e)
		}
		plain, e := f.outbox.Keys.Decrypt(key, d.ID, i.Purpose, nonce, cipher)
		if e != nil || bytes.Contains(cipher, []byte(i.Token)) || !bytes.Contains(plain, []byte("https://portal.example.invalid/register#token="+i.Token)) || message != f.outbox.messageID(d.ID) {
			t.Fatal("actual encryption/AAD/link/message identity", e)
		}
		ctx := mailContext(t)
		var rolled Summary
		domain := errors.New("SYNTHETIC_ROLLBACK")
		e = f.Store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
			var e error
			rolled, e = f.outbox.EnqueueWithin(ctx, tx, i)
			if e != nil {
				return e
			}
			return domain
		})
		if e != domain {
			t.Fatal("enqueue rollback identity", e)
		}
		var count int
		if e = f.Store.QueryRow(ctx, f.Statement("SELECT count(*) FROM mail_deliveries WHERE id=$1", "SELECT count(*) FROM mail_deliveries WHERE id=?", rolled.ID)).Scan(&count); e != nil || count != 0 {
			t.Fatal("rolled-back mail persisted", e)
		}
		e = f.Store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
			latest, e := LatestWithin(ctx, tx, i.InvitationID)
			if e != nil {
				return e
			}
			if latest == nil || latest.ID != d.ID {
				return errors.New("ACTUAL_LATEST_MISMATCH")
			}
			return CancelInvitationWithin(ctx, tx, i.InvitationID)
		})
		if e != nil {
			t.Fatal(e)
		}
		f.state(t, d.ID, "cancelled", 0, "token_obsolete", true)
	})
	t.Run("cancellation_scope_atomic_audit_and_idempotence", func(t *testing.T) {
		i, j, r := f.seed(t, "invitation"), f.seed(t, "invitation"), f.seed(t, "password_reset")
		a, b, c := f.enqueue(t, i), f.enqueue(t, j), f.enqueue(t, r)
		notice := f.enqueue(t, f.seed(t, "password_changed"))
		ctx := mailContext(t)
		if e := f.Store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
			if e := CancelInvitationWithin(ctx, tx, i.InvitationID); e != nil {
				return e
			}
			if e := CancelInvitationWithin(ctx, tx, i.InvitationID); e != nil {
				return e
			}
			return CancelUserResetWithin(ctx, tx, f.User)
		}); e != nil {
			t.Fatal(e)
		}
		f.state(t, a.ID, "cancelled", 0, "token_obsolete", true)
		f.state(t, c.ID, "cancelled", 0, "token_obsolete", true)
		f.state(t, b.ID, "queued", 0, "", false)
		f.state(t, notice.ID, "queued", 0, "", false)
		if f.auditCount(t, a.ID) != 1 || f.auditCount(t, c.ID) != 1 {
			t.Fatal("duplicate/missing cancellation audit")
		}
		domain := errors.New("SYNTHETIC_CANCEL_ROLLBACK")
		e := f.Store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
			if e := CancelInvitationWithin(ctx, tx, j.InvitationID); e != nil {
				return e
			}
			return domain
		})
		if e != domain || f.auditCount(t, b.ID) != 0 {
			t.Fatal("cancel rollback audit", e)
		}
		f.state(t, b.ID, "queued", 0, "", false)
		if e = f.Store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
			return CancelInvitationWithin(ctx, tx, j.InvitationID)
		}); e != nil {
			t.Fatal(e)
		}
		f.finish(t, f.claim(t))
	})
	for _, implicit := range []bool{false, true} {
		t.Run("actual_TLS_SMTP_"+strconv.FormatBool(implicit), func(t *testing.T) {
			sink := f.smtp(t, implicit)
			i := f.seed(t, "password_reset")
			d := f.enqueue(t, i)
			if active, e := f.outbox.DispatchOne(mailContext(t)); e != nil || !active {
				t.Fatal("actual TLS delivery", active, e)
			}
			select {
			case message := <-sink.Messages:
				decoded, e := stdmail.ReadMessage(bytes.NewReader(message))
				if e != nil {
					t.Fatal("actual MIME parse")
				}
				media, parameters, e := mime.ParseMediaType(decoded.Header.Get("Content-Type"))
				if e != nil || media != "multipart/alternative" {
					t.Fatal("actual MIME content type")
				}
				part, e := multipart.NewReader(decoded.Body, parameters["boundary"]).NextPart()
				if e != nil || part.Header.Get("Content-Transfer-Encoding") != "base64" {
					t.Fatal("actual MIME part encoding")
				}
				body, e := io.ReadAll(io.LimitReader(base64.NewDecoder(base64.StdEncoding, part), 65537))
				part.Close()
				if e != nil || len(body) > 65536 || !bytes.Contains(body, []byte(i.Token)) || decoded.Header.Get("Message-ID") != "<"+f.outbox.messageID(d.ID)+">" {
					t.Fatal("actual MIME/token/message mismatch")
				}
			case <-time.After(time.Second):
				t.Fatal("actual loopback TLS SMTP missing")
			}
			f.state(t, d.ID, "sent", 1, "", true)
			if f.auditCount(t, d.ID) != 1 {
				t.Fatal("missing terminal audit")
			}
			if active, e := f.outbox.DispatchOne(mailContext(t)); e != nil || active {
				t.Fatal("empty outbox not idle", active, e)
			}
		})
	}
	t.Run("two_workers_one_live_lease_and_SMTP_delivery", func(t *testing.T) {
		sink := f.smtp(t, false)
		d := f.enqueue(t, f.seed(t, "password_changed"))
		type result struct {
			active bool
			e      error
		}
		results := make(chan result, 2)
		start := make(chan struct{})
		ctx := mailContext(t)
		for i := 0; i < 2; i++ {
			go func() { <-start; active, e := f.outbox.DispatchOne(ctx); results <- result{active, e} }()
		}
		close(start)
		activeCount := 0
		for i := 0; i < 2; i++ {
			v := <-results
			if v.e != nil {
				t.Fatal("actual concurrent dispatch", v.e)
			}
			if v.active {
				activeCount++
			}
		}
		if activeCount != 1 || len(sink.Messages) != 1 || f.auditCount(t, d.ID) != 1 {
			t.Fatal("duplicate live lease/delivery", activeCount, len(sink.Messages))
		}
		f.state(t, d.ID, "sent", 1, "", true)
	})
	t.Run("expired_lease_recovery_and_stale_worker_fences", func(t *testing.T) {
		d := f.enqueue(t, f.seed(t, "password_changed"))
		old := f.claim(t)
		f.exec(t, "UPDATE mail_deliveries SET locked_until=clock_timestamp()-interval '1 second' WHERE id=$1", "UPDATE mail_deliveries SET locked_until=TIMESTAMPADD(SECOND,-1,UTC_TIMESTAMP(6)) WHERE id=?", d.ID)
		if ok, e := f.outbox.eligible(mailContext(t), f.Store, old); e != nil || ok {
			t.Fatal("expired worker can send", e)
		}
		f.finish(t, old)
		if e := f.outbox.requeue(mailContext(t), f.Store, old, time.Second, "smtp_unavailable"); e != nil {
			t.Fatal(e)
		}
		f.state(t, d.ID, "sending", 1, "", false)
		if f.auditCount(t, d.ID) != 0 {
			t.Fatal("expired worker completed/requeued before recovery")
		}
		current := f.claim(t)
		if current.ID != old.ID || current.Lease == old.Lease || current.Attempts != 2 {
			t.Fatal("lease not recovered with new identity")
		}
		if ok, e := f.outbox.eligible(mailContext(t), f.Store, old); e != nil || ok {
			t.Fatal("superseded worker can send", e)
		}
		f.finish(t, old)
		if e := f.outbox.requeue(mailContext(t), f.Store, old, time.Second, "smtp_unavailable"); e != nil {
			t.Fatal(e)
		}
		f.state(t, d.ID, "sending", 2, "", false)
		if f.auditCount(t, d.ID) != 0 {
			t.Fatal("stale worker wrote false audit")
		}
		f.finish(t, current)
		f.finish(t, current)
		f.state(t, d.ID, "cancelled", 2, "token_obsolete", true)
		if f.auditCount(t, d.ID) != 1 {
			t.Fatal("repeated finish duplicated audit")
		}
	})
	t.Run("exhausted_unknown_outcome_recovery", func(t *testing.T) {
		d := f.enqueue(t, f.seed(t, "password_changed"))
		c := f.claim(t)
		f.exec(t, "UPDATE mail_deliveries SET attempt_count=$1,locked_until=clock_timestamp()-interval '1 second' WHERE id=$2", "UPDATE mail_deliveries SET attempt_count=?,locked_until=TIMESTAMPADD(SECOND,-1,UTC_TIMESTAMP(6)) WHERE id=?", f.outbox.Config.Policy().Mail.MaxAttempts, c.ID)
		if _, found, e := f.outbox.claim(mailContext(t), f.Store); e != nil || found {
			t.Fatal("exhausted row reclaimed", found, e)
		}
		f.state(t, d.ID, "failed", f.outbox.Config.Policy().Mail.MaxAttempts, "smtp_unknown_outcome", true)
		if f.auditCount(t, d.ID) != 1 {
			t.Fatal("exhaustion audit missing")
		}
	})
	t.Run("transient_failure_real_retry_schedule", func(t *testing.T) {
		sink := f.smtp(t, false)
		sink.Close()
		d := f.enqueue(t, f.seed(t, "password_changed"))
		if active, e := f.outbox.DispatchOne(mailContext(t)); e != nil || !active {
			t.Fatal("actual retry", active, e)
		}
		f.state(t, d.ID, "queued", 1, "smtp_unavailable", false)
		var next, updated time.Time
		if e := f.Store.QueryRow(mailContext(t), f.Statement("SELECT next_attempt_at,updated_at FROM mail_deliveries WHERE id=$1", "SELECT next_attempt_at,updated_at FROM mail_deliveries WHERE id=?", d.ID)).Scan(&next, &updated); e != nil {
			t.Fatal(e)
		}
		policy := f.outbox.Config.Policy().Mail
		delay := next.Sub(updated)
		minimum := time.Duration(policy.Retry1Seconds) * time.Second
		maximum := minimum + minimum*time.Duration(policy.JitterPercent)/100
		if delay < minimum-time.Millisecond || delay > maximum+time.Millisecond {
			t.Fatal("actual retry outside configured range", delay)
		}
		f.exec(t, "UPDATE mail_deliveries SET next_attempt_at=clock_timestamp()-interval '1 second' WHERE id=$1", "UPDATE mail_deliveries SET next_attempt_at=TIMESTAMPADD(SECOND,-1,UTC_TIMESTAMP(6)) WHERE id=?", d.ID)
		f.finish(t, f.claim(t))
	})
	t.Run("permanent_recipient_decrypt_and_template_failures", func(t *testing.T) {
		sink := f.smtp(t, false)
		i := f.seed(t, "password_changed")
		i.Recipient = "recipient@rejected.invalid"
		d := f.enqueue(t, i)
		if _, e := f.outbox.DispatchOne(mailContext(t)); e != nil {
			t.Fatal(e)
		}
		f.state(t, d.ID, "failed", 1, "smtp_recipient_rejected", true)
		d = f.enqueue(t, f.seed(t, "password_changed"))
		f.exec(t, "UPDATE mail_deliveries SET payload_ciphertext=$1 WHERE id=$2", "UPDATE mail_deliveries SET payload_ciphertext=? WHERE id=?", []byte{0x01}, d.ID)
		if _, e := f.outbox.DispatchOne(mailContext(t)); e != nil {
			t.Fatal(e)
		}
		f.state(t, d.ID, "failed", 1, "mail_ciphertext_invalid", true)
		d = f.enqueue(t, f.seed(t, "password_changed"))
		original := f.outbox.Templates
		f.outbox.Templates = map[string]Template{}
		_, e := f.outbox.DispatchOne(mailContext(t))
		f.outbox.Templates = original
		if e != nil {
			t.Fatal(e)
		}
		f.state(t, d.ID, "failed", 1, "mail_template_invalid", true)
		if len(sink.Messages) != 0 {
			t.Fatal("failed payload/template/recipient delivered")
		}
	})
	t.Run("current_token_source_eligibility_suppresses_SMTP", func(t *testing.T) {
		sink := f.smtp(t, false)
		for _, kind := range []string{"workspace_disabled", "tenant_disabled", "generation_changed", "invitation_revoked", "invitation_expired", "reset_revoked", "user_disabled", "reset_expired", "reset_consumed"} {
			t.Run(kind, func(t *testing.T) {
				purpose := "invitation"
				if strings.HasPrefix(kind, "reset_") || kind == "user_disabled" {
					purpose = "password_reset"
				}
				i := f.seed(t, purpose)
				d := f.enqueue(t, i)
				switch kind {
				case "workspace_disabled":
					f.exec(t, "UPDATE workspaces SET status='disabled' WHERE id=$1", "UPDATE workspaces SET status='disabled' WHERE id=?", f.Workspace)
				case "tenant_disabled":
					f.exec(t, "UPDATE tenants SET status='disabled' WHERE id=$1", "UPDATE tenants SET status='disabled' WHERE id=?", f.Tenant)
				case "generation_changed":
					f.exec(t, "UPDATE invitations SET token_generation=token_generation+1 WHERE id=$1", "UPDATE invitations SET token_generation=token_generation+1 WHERE id=?", i.InvitationID)
				case "invitation_revoked":
					f.exec(t, "UPDATE invitations SET status='revoked',token_hash=NULL,revoked_at=clock_timestamp() WHERE id=$1", "UPDATE invitations SET status='revoked',token_hash=NULL,revoked_at=UTC_TIMESTAMP(6) WHERE id=?", i.InvitationID)
				case "invitation_expired":
					f.exec(t, "UPDATE invitations SET created_at=clock_timestamp()-interval '2 hours',expires_at=clock_timestamp()-interval '1 hour' WHERE id=$1", "UPDATE invitations SET created_at=TIMESTAMPADD(HOUR,-2,UTC_TIMESTAMP(6)),expires_at=TIMESTAMPADD(HOUR,-1,UTC_TIMESTAMP(6)) WHERE id=?", i.InvitationID)
				case "reset_revoked":
					f.exec(t, "UPDATE password_reset_tokens SET token_hash=NULL,revoked_at=clock_timestamp() WHERE id=$1", "UPDATE password_reset_tokens SET token_hash=NULL,revoked_at=UTC_TIMESTAMP(6) WHERE id=?", i.ResetID)
				case "reset_expired":
					f.exec(t, "UPDATE password_reset_tokens SET created_at=clock_timestamp()-interval '2 hours',expires_at=clock_timestamp()-interval '1 hour' WHERE id=$1", "UPDATE password_reset_tokens SET created_at=TIMESTAMPADD(HOUR,-2,UTC_TIMESTAMP(6)),expires_at=TIMESTAMPADD(HOUR,-1,UTC_TIMESTAMP(6)) WHERE id=?", i.ResetID)
				case "reset_consumed":
					f.exec(t, "UPDATE password_reset_tokens SET token_hash=NULL,consumed_at=clock_timestamp() WHERE id=$1", "UPDATE password_reset_tokens SET token_hash=NULL,consumed_at=UTC_TIMESTAMP(6) WHERE id=?", i.ResetID)
				case "user_disabled":
					f.exec(t, "UPDATE users SET status='disabled' WHERE id=$1", "UPDATE users SET status='disabled' WHERE id=?", f.User)
				}
				if _, e := f.outbox.DispatchOne(mailContext(t)); e != nil {
					t.Fatal(e)
				}
				f.state(t, d.ID, "cancelled", 1, "token_obsolete", true)
				f.exec(t, "UPDATE workspaces SET status='active' WHERE id=$1", "UPDATE workspaces SET status='active' WHERE id=?", f.Workspace)
				f.exec(t, "UPDATE tenants SET status='active' WHERE id=$1", "UPDATE tenants SET status='active' WHERE id=?", f.Tenant)
				f.exec(t, "UPDATE users SET status='active' WHERE id=$1", "UPDATE users SET status='active' WHERE id=?", f.User)
			})
		}
		if len(sink.Messages) != 0 {
			t.Fatal("obsolete token source delivered")
		}
	})
	t.Run("trusted_storage_authority_conflict_refuses", func(t *testing.T) {
		copy := *f.outbox
		copy.DB = &pgxpool.Pool{}
		if _, e := copy.DispatchOne(mailContext(t)); e == nil || e.Error() != "MAIL_STORAGE_CONFIGURATION_CONFLICT" {
			t.Fatal("ambiguous storage accepted", e)
		}
		copy.Storage = nil
		copy.DB = nil
		if _, e := copy.DispatchOne(mailContext(t)); e == nil || e.Error() != "MAIL_STORAGE_REQUIRED" {
			t.Fatal("missing storage accepted", e)
		}
	})
	if f.PostgreSQL != nil {
		t.Run("existing_PostgreSQL_serving_entrypoints", func(t *testing.T) {
			sink := f.smtp(t, false)
			ctx := mailContext(t)
			i := f.seed(t, "invitation")
			legacy := *f.outbox
			legacy.DB = f.PostgreSQL
			legacy.Storage = nil
			tx, e := f.PostgreSQL.Begin(ctx)
			if e != nil {
				t.Fatal("actual legacy begin")
			}
			defer tx.Rollback(ctx)
			d, e := legacy.Enqueue(ctx, tx, i)
			if e != nil {
				t.Fatal(e)
			}
			latest, e := Latest(ctx, tx, i.InvitationID)
			if e != nil || latest == nil || latest.ID != d.ID {
				t.Fatal("actual legacy latest", e)
			}
			if e = CancelInvitation(ctx, tx, i.InvitationID); e != nil {
				t.Fatal(e)
			}
			if e = CancelUserReset(ctx, tx, f.User); e != nil {
				t.Fatal(e)
			}
			if e = tx.Commit(ctx); e != nil {
				t.Fatal(e)
			}
			f.state(t, d.ID, "cancelled", 0, "token_obsolete", true)
			f.enqueue(t, f.seed(t, "password_changed"))
			if active, e := legacy.DispatchOne(ctx); e != nil || !active || len(sink.Messages) != 1 {
				t.Fatal("actual legacy dispatcher", active, e)
			}
		})
	}
}
