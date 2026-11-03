//go:build repository_integration

package identity_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
)

func TestActualIdentityBootstrapRepository(t *testing.T) {
	f := repositoryfixture.NewUnbootstrapped(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "synthetic-blocklist")
	if e := os.WriteFile(path, []byte("passwordpasswordpassword\n"), 0600); e != nil {
		t.Fatal(e)
	}
	passwords, e := security.NewPasswords(path)
	if e != nil {
		t.Fatal(e)
	}
	s := &identity.Service{Passwords: passwords}
	if f.PostgreSQL != nil {
		s.DB = f.PostgreSQL
	} else {
		s.Storage = f.Store
	}
	password := "Synthetic bootstrap phrase " + uuid.NewString()
	bootstrap := func(ctx context.Context) error {
		return s.Bootstrap(ctx, " Bootstrap@Example.invalid ", " Operator \u4e16\u754c ", password, " Installation \u4e16\u754c ", " Workspace \u7a7a\u95f4 ")
	}
	count := func(table string) int {
		t.Helper()
		var n int
		if e := f.Store.QueryRow(ctx, f.Statement("SELECT count(*) FROM "+table, "SELECT count(*) FROM `"+table+"`")).Scan(&n); e != nil {
			t.Fatal("actual count", e)
		}
		return n
	}
	empty := func() {
		t.Helper()
		for _, table := range []string{"tenants", "workspaces", "users", "password_credentials", "workspace_memberships", "audit_events", "sessions", "mail_deliveries"} {
			if count(table) != 0 {
				t.Fatal("partial bootstrap survived", table)
			}
		}
		var complete bool
		if e := f.Store.QueryRow(ctx, f.Statement("SELECT bootstrap_complete FROM installation_state WHERE id=1", "SELECT bootstrap_complete FROM installation_state WHERE id=1")).Scan(&complete); e != nil || complete {
			t.Fatal("installation state changed on failure", e)
		}
	}
	t.Run("invalid_blocked_cancelled_and_missing_configuration_no_writes", func(t *testing.T) {
		for _, input := range []struct {
			email, name, password string
			code                  string
		}{{"invalid", "Operator", password, "VALIDATION_FAILED"}, {"bootstrap@example.invalid", "", password, "VALIDATION_FAILED"}, {"bootstrap@example.invalid", "Operator", "short", "PASSWORD_POLICY_VIOLATION"}, {"bootstrap@example.invalid", "Operator", "passwordpasswordpassword", "PASSWORD_BLOCKED"}} {
			if e := s.Bootstrap(ctx, input.email, input.name, input.password, "Installation", "Workspace"); e == nil || e.Error() != input.code {
				t.Fatal("bootstrap input policy", e)
			}
			empty()
		}
		cancelled, done := context.WithCancel(ctx)
		done()
		if e := bootstrap(cancelled); !errors.Is(e, context.Canceled) {
			t.Fatal("cancelled bootstrap", e)
		}
		empty()
		bad := *s
		bad.DB = nil
		bad.Storage = nil
		if e := bad.Bootstrap(ctx, "bootstrap@example.invalid", "Operator", password, "Installation", "Workspace"); e == nil || e.Error() != "IDENTITY_STORAGE_REQUIRED" {
			t.Fatal("missing storage accepted", e)
		}
		bad.DB = &pgxpool.Pool{}
		bad.Storage = f.Store
		if e := bad.Bootstrap(ctx, "bootstrap@example.invalid", "Operator", password, "Installation", "Workspace"); e == nil || e.Error() != "IDENTITY_STORAGE_CONFIGURATION_CONFLICT" {
			t.Fatal("ambiguous storage accepted", e)
		}
		bad.Passwords = nil
		if e := bad.Bootstrap(ctx, "bootstrap@example.invalid", "Operator", password, "Installation", "Workspace"); e == nil || e.Error() != "IDENTITY_BOOTSTRAP_CONFIGURATION_REQUIRED" {
			t.Fatal("missing password configuration accepted", e)
		}
		empty()
	})
	if f.Store.Dialect() == transaction.PostgreSQL {
		if _, e := f.Store.Exec(ctx, f.Statement(`CREATE FUNCTION repository_bootstrap_failure() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'SYNTHETIC_BOOTSTRAP_FAILURE'; END$$;`, "")); e != nil {
			t.Fatal("actual failure function", e)
		}
	}
	for _, step := range []struct{ table, event string }{{"tenants", "INSERT"}, {"workspaces", "INSERT"}, {"users", "INSERT"}, {"password_credentials", "INSERT"}, {"workspace_memberships", "INSERT"}, {"installation_state", "UPDATE"}, {"audit_events", "INSERT"}} {
		t.Run("actual_rollback_at_"+step.table, func(t *testing.T) {
			pg := "CREATE TRIGGER repository_bootstrap_failure BEFORE " + step.event + " ON " + step.table + " FOR EACH ROW EXECUTE FUNCTION repository_bootstrap_failure()"
			my := "CREATE TRIGGER repository_bootstrap_failure BEFORE " + step.event + " ON `" + step.table + "` FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='SYNTHETIC_BOOTSTRAP_FAILURE'"
			if _, e := f.Store.Exec(ctx, f.Statement(pg, my)); e != nil {
				t.Fatal("actual failure trigger", e)
			}
			e := bootstrap(ctx)
			_, drop := f.Store.Exec(ctx, f.Statement("DROP TRIGGER repository_bootstrap_failure ON "+step.table, "DROP TRIGGER repository_bootstrap_failure"))
			if drop != nil {
				t.Fatal("exact trigger cleanup", drop)
			}
			if e == nil || e.Error() != "STORAGE_UNAVAILABLE" {
				t.Fatal("actual durable failure did not safely abort", e)
			}
			empty()
		})
	}
	t.Run("two_contenders_exactly_one_bootstrap_and_repeat_refusal", func(t *testing.T) {
		start := make(chan struct{})
		results := make(chan error, 2)
		for n := 0; n < 2; n++ {
			go func() { <-start; results <- bootstrap(ctx) }()
		}
		close(start)
		wins := 0
		for n := 0; n < 2; n++ {
			e := <-results
			if e == nil {
				wins++
			} else if e.Error() != "BOOTSTRAP_ALREADY_COMPLETE" {
				t.Fatal("actual contender", e)
			}
		}
		if wins != 1 {
			t.Fatal("multiple bootstrap winners", wins)
		}
		for _, table := range []string{"tenants", "workspaces", "users", "password_credentials", "workspace_memberships", "audit_events"} {
			if count(table) != 1 {
				t.Fatal("duplicate or missing bootstrap row", table)
			}
		}
		if e := bootstrap(ctx); e == nil || e.Error() != "BOOTSTRAP_ALREADY_COMPLETE" {
			t.Fatal("bootstrap repeated", e)
		}
		if count("sessions") != 0 || count("mail_deliveries") != 0 || count("audit_events") != 1 {
			t.Fatal("unexpected bootstrap session/mail/audit")
		}
	})
	t.Run("readback_real_hash_ids_scope_and_operator_audit", func(t *testing.T) {
		var tenant, workspace, user, email, display, name, tenantName, workspaceName, role, locale, status, phc string
		var admin bool
		var verified *time.Time
		var version, epoch int64
		pg := `SELECT t.id::text,w.id::text,u.id::text,u.email_canonical,u.email_display,u.display_name,t.name,w.name,m.role,u.locale,u.status,u.is_platform_admin,u.email_verified_at,u.version,u.auth_epoch,c.password_phc FROM installation_state i JOIN tenants t ON t.id=i.tenant_id JOIN workspaces w ON w.id=i.default_workspace_id AND w.tenant_id=t.id JOIN users u ON u.id=i.primary_admin_user_id JOIN password_credentials c ON c.user_id=u.id JOIN workspace_memberships m ON m.user_id=u.id AND m.tenant_id=t.id AND m.workspace_id=w.id WHERE i.id=1 AND i.bootstrap_complete`
		my := `SELECT t.id,w.id,u.id,u.email_canonical,u.email_display,u.display_name,t.name,w.name,m.role,u.locale,u.status,u.is_platform_admin,u.email_verified_at,u.version,u.auth_epoch,c.password_phc FROM installation_state i JOIN tenants t ON t.id=i.tenant_id JOIN workspaces w ON w.id=i.default_workspace_id AND w.tenant_id=t.id JOIN users u ON u.id=i.primary_admin_user_id JOIN password_credentials c ON c.user_id=u.id JOIN workspace_memberships m ON m.user_id=u.id AND m.tenant_id=t.id AND m.workspace_id=w.id WHERE i.id=1 AND i.bootstrap_complete`
		if e := f.Store.QueryRow(ctx, f.Statement(pg, my)).Scan(&tenant, &workspace, &user, &email, &display, &name, &tenantName, &workspaceName, &role, &locale, &status, &admin, &verified, &version, &epoch, &phc); e != nil {
			t.Fatal("actual bootstrap readback", e)
		}
		for _, id := range []string{tenant, workspace, user} {
			parsed, e := uuid.Parse(id)
			if e != nil || parsed.String() != id || parsed.Version() != 4 {
				t.Fatal("actual canonical bootstrap UUID")
			}
		}
		if email != "bootstrap@example.invalid" || display != "Bootstrap@Example.invalid" || name != "Operator \u4e16\u754c" || tenantName != "Installation \u4e16\u754c" || workspaceName != "Workspace \u7a7a\u95f4" || role != "owner" || locale != "en" || status != "active" || !admin || verified != nil || version != 1 || epoch != 1 || !strings.HasPrefix(phc, "$argon2id$") {
			t.Fatal("actual bootstrap policy/readback mismatch")
		}
		if ok, e := passwords.Verify(ctx, phc, password); e != nil || !ok {
			t.Fatal("stored real Argon2 credential not usable", e)
		}
		var scope, auditWorkspace, subject, audience, action, outcome string
		var actor *string
		if e := f.Store.QueryRow(ctx, f.Statement("SELECT tenant_id::text,workspace_id::text,subject_user_id::text,actor_user_id::text,audience,action,outcome FROM audit_events", "SELECT tenant_id,workspace_id,subject_user_id,actor_user_id,audience,action,outcome FROM audit_events")).Scan(&scope, &auditWorkspace, &subject, &actor, &audience, &action, &outcome); e != nil || scope != tenant || auditWorkspace != workspace || subject != user || actor != nil || audience != "operator" || action != "installation.bootstrapped" || outcome != "success" {
			t.Fatal("actual bootstrap audit mismatch", e)
		}
	})
}
