//go:build repository_integration

package releases

import (
	"context"
	"github.com/google/uuid"
	"os"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/localization"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	"testing"
	"time"
)

// The harness supplies an already published synthetic projection owned by another
// existing UID and its protected binding config. This test never changes accounts,
// chmods production state or bypasses readSnapshot's actual EUID check.
func TestActualReleaseViewReadAuditAndCurrentAdminRevocation(t *testing.T) {
	c, e := Load(os.Getenv("PROJECT_ONE_RELEASE_VIEW_TEST_CONFIG"))
	if e != nil {
		t.Fatal("explicit protected distinct-UID projection fixture required", e)
	}
	if _, e = readSnapshot(c); e != nil {
		t.Fatal("actual publisher/runtime isolation required", e)
	}
	f := repositoryfixture.New(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	owner := &identity.Service{Storage: f.Store}
	if f.PostgreSQL != nil {
		owner.Storage = nil
		owner.DB = f.PostgreSQL
	}
	var issued identity.IssuedSession
	if e = f.Store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		issued, e = owner.IssueSessionWithin(ctx, q, f.User, 1, "admin", "", uuid.NewString(), nil)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	a, e := owner.Authenticate(ctx, issued.Token, "admin")
	if e != nil {
		t.Fatal(e)
	}
	locales, e := localization.Default()
	if e != nil {
		t.Fatal(e)
	}
	s, e := New(owner, locales, c)
	if e != nil {
		t.Fatal(e)
	}
	request := uuid.NewString()
	current, e := s.Current(ctx, a, request)
	if e != nil || current.LiveHealth || current.AppliedSchema < 1 || len(current.Locales) < 1 {
		t.Fatal("actual selected read", e)
	}
	var count int
	if e = f.Store.QueryRow(ctx, f.Statement("SELECT count(*) FROM audit_events WHERE request_id=$1 AND action='release.current.read' AND target_id IS NULL", "SELECT count(*) FROM audit_events WHERE request_id=? AND action='release.current.read' AND target_id IS NULL", request)).Scan(&count); e != nil || count != 1 {
		t.Fatal("read audit", count, e)
	}
	if _, e = s.History(ctx, a, "", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if _, e = f.Store.Exec(ctx, f.Statement("UPDATE users SET is_platform_admin=false WHERE id=$1", "UPDATE users SET is_platform_admin=false WHERE id=?", f.User)); e != nil {
		t.Fatal(e)
	}
	denied := uuid.NewString()
	out, e := s.Current(ctx, a, denied)
	if e == nil || out.Snapshot.ScopeID != "" {
		t.Fatal("stale admin returned data", e)
	}
	if e = f.Store.QueryRow(ctx, f.Statement("SELECT count(*) FROM audit_events WHERE request_id=$1", "SELECT count(*) FROM audit_events WHERE request_id=?", denied)).Scan(&count); e != nil || count != 0 {
		t.Fatal("denied read committed audit", e)
	}
}
