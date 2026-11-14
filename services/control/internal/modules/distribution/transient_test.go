package distribution_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestActualTransientOrdinaryKeyIndependentCleanupAfterCancellationAuthorityLossAndCallbackFailure(t *testing.T) {
	for _, kind := range []string{"success", "cancelled", "authority_invalidated", "callback_failed"} {
		t.Run(kind, func(t *testing.T) {
			c := setup(t)
			alias := c.published(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var id string
			e := c.s.WithTransientKey(ctx, c.p, keyInput(alias), 2*time.Second, uuid.NewString(), func(raw string) error {
				parts := strings.Split(raw, ".")
				if len(parts) != 3 {
					t.Fatal("ordinary secret format")
				}
				id = parts[1]
				var active bool
				if e := c.s.DB.QueryRow(context.Background(), "SELECT revoked_at IS NULL FROM api_keys WHERE id=$1", id).Scan(&active); e != nil || !active {
					t.Fatal("ordinary durable key not admitted", e)
				}
				switch kind {
				case "cancelled":
					cancel()
					return context.Canceled
				case "authority_invalidated":
					_, e := c.s.DB.Exec(context.Background(), "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", c.p.Me.User.ID)
					return e
				case "callback_failed":
					return errors.New("SYNTHETIC_CALLBACK_FAILURE")
				}
				return nil
			})
			if (kind == "success" || kind == "authority_invalidated") && e != nil {
				t.Fatal("independent cleanup", e)
			}
			if kind == "cancelled" && !errors.Is(e, context.Canceled) {
				t.Fatal("callback cancellation lost", e)
			}
			if kind == "callback_failed" && (e == nil || e.Error() != "SYNTHETIC_CALLBACK_FAILURE") {
				t.Fatal("callback failure lost", e)
			}
			var revoked bool
			var events int
			if e := c.s.DB.QueryRow(context.Background(), "SELECT revoked_at IS NOT NULL FROM api_keys WHERE id=$1", id).Scan(&revoked); e != nil || !revoked {
				t.Fatal("transient authority leaked", e)
			}
			if e := c.s.DB.QueryRow(context.Background(), "SELECT count(*) FROM audit_events WHERE action='gateway.api_key_revoked' AND target_id=$1", id).Scan(&events); e != nil || events != 1 {
				t.Fatal("cleanup audit", e)
			}
		})
	}
}
func TestActualTransientCleanupAuditFailureIsExplicitAndDoesNotClaimRevocation(t *testing.T) {
	c := setup(t)
	alias := c.published(t)
	var id string
	e := c.s.WithTransientKey(context.Background(), c.p, keyInput(alias), 2*time.Second, uuid.NewString(), func(raw string) error {
		id = strings.Split(raw, ".")[1]
		_, e := c.s.DB.Exec(context.Background(), `CREATE FUNCTION transient_audit_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='gateway.api_key_revoked' THEN RAISE EXCEPTION 'synthetic audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER transient_audit_failure BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION transient_audit_failure()`)
		return e
	})
	check(t, e, "TRANSIENT_KEY_CLEANUP_FAILED")
	var active bool
	if e = c.s.DB.QueryRow(context.Background(), "SELECT revoked_at IS NULL AND expires_at>clock_timestamp() FROM api_keys WHERE id=$1", id).Scan(&active); e != nil || !active {
		t.Fatal("failed cleanup incorrectly claimed revocation", e)
	}
	if _, e = c.s.DB.Exec(context.Background(), "DROP TRIGGER transient_audit_failure ON audit_events"); e != nil {
		t.Fatal(e)
	}
	if _, e = c.s.RevokeKey(context.Background(), c.p, alias.WorkspaceID, id, "Explicit recovery after synthetic cleanup failure", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
}
