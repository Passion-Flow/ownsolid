//go:build cgo && odbc_manager && repository_integration && dameng_repository_integration

package middleware

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	license "github.com/Passion-Flow/ownsolid/pkg/license/v1"
)

func TestActualDamengMiddlewareIntentAndRecovery(t *testing.T) {
	// This input names one real synthetic TLS-protected PostgreSQL/MySQL health
	// endpoint. Production credentials, platform-owned mutation and fallback are
	// excluded by this test's explicit profile admission.
	c, e := LoadFiles(os.Getenv("PROJECT_ONE_DAMENG_MIDDLEWARE_PROFILES"), "")
	if e != nil || len(c.Profiles) != 1 || c.Profiles[0].Kind != CustomerExternal || c.Policy.RequestSeconds > 10 || c.Policy.ProbeSeconds > 10 || c.Policy.OperationSeconds > 60 { t.Fatal("BOUNDED_REAL_SYNTHETIC_HEALTH_PROFILE_REQUIRED") }
	c.Policy.PageSize = 1
	g := newDamengStory(t)
	key, trust, e := license.GenerateKey("synthetic-native-middleware")
	if e != nil { t.Fatal(e) }
	now := time.Now().UTC().Truncate(time.Second).Add(5*time.Second)
	licenses, e := licensing.New(g.owner, trust, license.DefaultPolicy(), func() time.Time { return now })
	if e != nil { t.Fatal(e) }
	g.owner.BusinessAdmission = licenses
	status, e := licenses.StatusAdmin(g.ctx, g.admin)
	if e != nil { t.Fatal("actual native license repository required", e) }
	stamp := func(delta time.Duration) string { return now.Add(delta).Format(time.RFC3339) }
	claims := license.Claims{FormatVersion: 1, LicenseID: uuid.NewString(), CustomerReference: "Synthetic native metadata fixture", DeploymentID: status.DeploymentID, IssuedAt: stamp(-time.Second), NotBefore: stamp(-time.Second), ExpiresAt: stamp(time.Hour), GraceUntil: stamp(2*time.Hour), Scope: license.WholePlatformScope}
	artifact, e := license.Issue(claims, key)
	if e != nil { t.Fatal(e) }
	if _, e = licenses.Import(g.ctx, g.admin, licensing.ImportInput{Artifact: string(artifact), Reason: "Synthetic native acceptance"}, uuid.NewString()); e != nil { t.Fatal(e) }
	s, e := New(g.owner, &workspaces.Service{Identity: g.owner}, c)
	if e != nil { t.Fatal(e) }
	intent := uuid.NewString()
	v, e := s.Register(g.ctx, g.admin, g.workspace, c.Profiles[0].Key, "Synthetic native health", intent, uuid.NewString())
	if e != nil { t.Fatal(e) }
	again, e := s.RegistrationIntent(g.ctx, g.admin, g.workspace, intent)
	if e != nil || again.ID != v.ID { t.Fatal("original registration intent", e) }
	again, e = s.Register(g.ctx, g.admin, g.workspace, c.Profiles[0].Key, "Synthetic native health", intent, uuid.NewString())
	if e != nil || again.ID != v.ID { t.Fatal("duplicate registration", e) }
	if out, e := s.Get(g.ctx, g.admin, uuid.NewString(), v.ID); e == nil || out.ID != "" { t.Fatal("foreign workspace metadata exposed") }
	enqueue := func() Operation {
		t.Helper()
		out, e := s.Enqueue(g.ctx, g.admin, g.workspace, v.ID, OperationInput{Kind: "health", Reason: "Synthetic native observation", IdempotencyKey: uuid.NewString(), Version: v.Version}, uuid.NewString())
		if e != nil { t.Fatal(e) }; return out
	}
	input := OperationInput{Kind: "health", Reason: "Synthetic native observation", IdempotencyKey: uuid.NewString(), Version: v.Version}
	op, e := s.Enqueue(g.ctx, g.admin, g.workspace, v.ID, input, uuid.NewString())
	if e != nil { t.Fatal(e) }
	readIntent, e := s.OperationIntent(g.ctx, g.admin, g.workspace, input.IdempotencyKey)
	if e != nil || readIntent.ID != op.ID { t.Fatal("original operation intent", e) }
	if e := s.Pass(g.ctx); e != nil { t.Fatal("real external endpoint health execution", e) }
	health, e := s.Operation(g.ctx, g.admin, g.workspace, op.ID)
	if e != nil || health.State != "succeeded" || len(health.Result) == 0 { t.Fatal("native health evidence", e) }
	if e := s.VerifyStored(g.ctx); e != nil { t.Fatal("native CLOB evidence and intent hash", e) }
	uncertain := enqueue()
	claim, e := s.candidate(g.ctx)
	if e != nil || claim == nil || claim.ID != uncertain.ID { t.Fatal(e) }
	interrupted, cancel := context.WithCancel(g.ctx)
	drop := &damengDropCommit{Store: g.store, cancel: cancel}
	g.owner.Storage = drop
	started, e := s.claim(interrupted, claim)
	g.owner.Storage = g.store
	cancel()
	if started || e == nil || normalize(e).Error() != string(transaction.CommitUnknown) || !drop.dropped || drop.after != 0 { t.Fatal("uncertain claim executed/replayed", e) }
	if g.count(t, "SELECT count(*) FROM middleware_operations WHERE id=? AND state='running' AND attempt_id IS NOT NULL", uncertain.ID) != 1 { t.Fatal("real original claim was not retained") }
	g.exec(t, "UPDATE middleware_operations SET lease_expires_at=DATEADD(SECOND,-1,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))) WHERE id=? AND state='running'", uncertain.ID)
	if e := s.expire(g.ctx); e != nil { t.Fatal(e) }
	if e := s.Pass(g.ctx); e != nil { t.Fatal(e) }
	read, e := s.Operation(g.ctx, g.admin, g.workspace, uncertain.ID)
	if e != nil || read.State != "unknown" || len(read.Result) != 0 { t.Fatal("expired unknown replayed or published result", e) }
	page, e := s.History(g.ctx, g.admin, g.workspace, v.ID, "")
	if e != nil || len(page.Items) != 1 || page.NextCursor == nil { t.Fatal("native history first page", e) }
	second, e := s.History(g.ctx, g.admin, g.workspace, v.ID, *page.NextCursor)
	if e != nil || len(second.Items) != 1 || second.Items[0].ID == page.Items[0].ID { t.Fatal("native history continuation", e) }
	queued := enqueue()
	if e := g.store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error { return InvalidateAuthoritiesWithin(ctx, q, "Synthetic isolated restore fence") }); e != nil { t.Fatal(e) }
	if g.count(t, "SELECT count(*) FROM middleware_operations WHERE id=? AND state='failed' AND error_code='MIDDLEWARE_RESTORE_INVALIDATED'", queued.ID) != 1 { t.Fatal("native restore did not invalidate authority") }
	// Native constraint/trigger rejection poisons only this owned connection.
	e = g.store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error { _, e := q.Exec(ctx, transaction.SQL(transaction.Dameng, "UPDATE middleware_operations SET state='queued' WHERE id=?", uncertain.ID)); return e })
	if e == nil || g.count(t, "SELECT count(*) FROM middleware_operations WHERE id=? AND state='unknown'", uncertain.ID) != 1 { t.Fatal("terminal native intent reopened") }
	denied := enqueue()
	g.exec(t, "UPDATE users SET status='disabled' WHERE id=?", g.user)
	if e := s.Pass(g.ctx); e != nil { t.Fatal("revocation should record rejection without endpoint effect", e) }
	if g.count(t, "SELECT count(*) FROM middleware_operations WHERE id=? AND state='failed' AND attempt_id IS NULL AND result_json IS NULL", denied.ID) != 1 { t.Fatal("stale actor executed external health") }
	if out, e := s.OperationIntent(g.ctx, g.admin, g.workspace, input.IdempotencyKey); e == nil || out.ID != "" { t.Fatal("revoked session retained intent access") }
}
