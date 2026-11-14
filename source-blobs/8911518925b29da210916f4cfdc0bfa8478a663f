//go:build portal_database_integration

package preferences

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/migrations"
	"github.com/google/uuid"
)

func requireCode(t *testing.T, e error, code string) {
	t.Helper()
	if e == nil || e.Error() != code {
		t.Fatalf("expected %s, got %v", code, e)
	}
}

func registerPreferenceUser(t *testing.T, s *identity.Service, admin identity.Auth) identity.Auth {
	t.Helper()
	ctx := context.Background()
	email := uuid.NewString() + "@example.invalid"
	invitation, e := s.Invite(ctx, admin, "", "createInvitation", uuid.NewString(), email, "en", 0, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	password := fixture.Password()
	if e = s.Register(ctx, fixture.InvitationToken(t, s, invitation.ID), "Preference member · \u6d4b\u8bd5", password, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	return fixture.Login(t, s, email, password, "portal")
}

func TestM15ActualPreferenceOwnerConcurrencyAuditRollbackAndDisabledIdentity(t *testing.T) {
	fixture.PreflightOwnedPostgres(t)
	account := fixture.New(t)
	password := fixture.Bootstrap(t, account)
	admin := fixture.Login(t, account, "admin@example.invalid", password, "admin")
	user := registerPreferenceUser(t, account, admin)
	s := &Service{DB: account.DB, Identity: account}
	ctx := context.Background()
	initial, e := s.Get(ctx, user)
	if e != nil || initial.Theme != SystemTheme || initial.Revision != 1 {
		t.Fatal("new account default", e, initial)
	}
	t.Run("invalid_fixed_enum_and_revision_never_mutates", func(t *testing.T) {
		for _, v := range []UpdateRequest{{Theme: new("url(javascript:alert(1))"), ExpectedRevision: 1}, {Theme: new("dark"), ExpectedRevision: 0}, {Theme: new("dark"), ExpectedRevision: MaximumRevision}} {
			_, e := s.Update(ctx, user, v, uuid.NewString())
			requireCode(t, e, "VALIDATION_FAILED")
		}
	})
	t.Run("concurrent_exact_revision_has_one_winner", func(t *testing.T) {
		start := make(chan struct{})
		results := make(chan error, 2)
		var wg sync.WaitGroup
		for _, theme := range []string{LightTheme, DarkTheme} {
			wg.Add(1)
			go func(theme string) {
				defer wg.Done()
				<-start
				_, e := s.Update(ctx, user, UpdateRequest{Theme: &theme, ExpectedRevision: 1}, uuid.NewString())
				results <- e
			}(theme)
		}
		close(start)
		wg.Wait()
		close(results)
		passed, conflicted := 0, 0
		for e := range results {
			if e == nil {
				passed++
			} else if e.Error() == "PREFERENCE_REVISION_CONFLICT" {
				conflicted++
			} else {
				t.Fatal(e)
			}
		}
		if passed != 1 || conflicted != 1 {
			t.Fatal("lost update", passed, conflicted)
		}
		current, e := s.Get(ctx, user)
		if e != nil || current.Revision != 2 {
			t.Fatal(e, current)
		}
		var count int
		if e = account.DB.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action='preference.updated' AND actor_user_id=$1", user.Me.User.ID).Scan(&count); e != nil || count != 1 {
			t.Fatal("mutation/audit cardinality", e, count)
		}
		own, e := s.Get(ctx, admin)
		if e != nil || own.Revision != 1 || own.Theme != SystemTheme {
			t.Fatal("other recipient changed", e, own)
		}
	})
	t.Run("audit_failure_preserves_value_revision_and_new_service_reads_persistence", func(t *testing.T) {
		before, e := s.Get(ctx, user)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = account.DB.Exec(ctx, `CREATE FUNCTION portal_preference_reject_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='preference.updated' THEN RAISE EXCEPTION 'SYNTHETIC_PREFERENCE_AUDIT_REFUSAL';END IF;RETURN NEW;END;$$;CREATE TRIGGER portal_preference_reject_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION portal_preference_reject_audit()`); e != nil {
			t.Fatal(e)
		}
		_, e = s.Update(ctx, user, UpdateRequest{Theme: new(DarkTheme), ExpectedRevision: before.Revision}, uuid.NewString())
		requireCode(t, e, "STORAGE_UNAVAILABLE")
		fresh := &Service{DB: account.DB, Identity: account}
		after, e := fresh.Get(ctx, user)
		if e != nil || after != before {
			t.Fatal("failed audit committed preference", e)
		}
		if _, e = account.DB.Exec(ctx, "DROP TRIGGER portal_preference_reject_audit ON audit_events;DROP FUNCTION portal_preference_reject_audit()"); e != nil {
			t.Fatal(e)
		}
		_, e = account.DB.Exec(ctx, "UPDATE user_preferences SET user_id=$1,revision=revision+1 WHERE user_id=$2", admin.Me.User.ID, user.Me.User.ID)
		if e == nil || !strings.Contains(e.Error(), "PREFERENCE_OWNER_IMMUTABLE") {
			t.Fatal("owner mutation", e)
		}
		if e = s.VerifyStored(ctx); e != nil {
			t.Fatal("stored preference verification", e)
		}
	})
	t.Run("current_identity_rechecked_after_disable", func(t *testing.T) {
		current, e := account.GetUser(ctx, user.Me.User.ID)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = account.SetStatus(ctx, admin, current.ID, "disabled", current.Version, "Isolated preference revocation test", uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		if _, e = s.Get(ctx, user); e == nil {
			t.Fatal("disabled identity read preference")
		}
		if _, e = s.Update(ctx, user, UpdateRequest{Theme: new(LightTheme), ExpectedRevision: 2}, uuid.NewString()); e == nil {
			t.Fatal("disabled identity changed preference")
		}
	})
}

func TestM15ActualPopulated14MigrationRollbackRepeatUpgradeAndUsedDowngradeRefusal(t *testing.T) {
	fixture.PreflightOwnedPostgres(t)
	account := fixture.NewAtVersion(t, 14)
	password := fixture.Bootstrap(t, account)
	ctx := context.Background()
	if e := database.Migrate(ctx, account.DB); e != nil {
		t.Fatal("actual populated14 upgrade", e)
	}
	portal := fixture.Login(t, account, "admin@example.invalid", password, "portal")
	s := &Service{DB: account.DB, Identity: account}
	initial, e := s.Get(ctx, portal)
	if e != nil || initial.Theme != SystemTheme || initial.Revision != 2 {
		t.Fatal("existing user backfill", e, initial)
	}
	down, e := migrations.Files.ReadFile("postgresql/000015_portal.down.sql")
	if e != nil {
		t.Fatal(e)
	}
	tx, e := account.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	localizationDown, e := migrations.Files.ReadFile("postgresql/000016_localization.down.sql")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(ctx, string(localizationDown)); e != nil {
		t.Fatal("unused successor16 rollback", e)
	}
	if _, e = tx.Exec(ctx, "DELETE FROM schema_migrations WHERE version=16"); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(ctx, string(down)); e != nil {
		t.Fatal("unused actual rollback", e)
	}
	if _, e = tx.Exec(ctx, "DELETE FROM schema_migrations WHERE version=15"); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	if e = database.Check(ctx, account.DB); e == nil {
		t.Fatal("old schema accepted")
	}
	if e = database.Migrate(ctx, account.DB); e != nil {
		t.Fatal("actual repeat upgrade", e)
	}
	if e = database.Check(ctx, account.DB); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Update(ctx, portal, UpdateRequest{Theme: new(DarkTheme), ExpectedRevision: 1}, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	tx, e = account.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, string(down)); e == nil || !strings.Contains(e.Error(), "PORTAL_DOWNGRADE_REQUIRES_VERIFIED_BACKUP") {
		t.Fatal("used state discarded", e)
	}
}
