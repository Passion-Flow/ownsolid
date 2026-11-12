package scheduling

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type authenticatedQuotaFixture struct {
	accounts  *identity.Service
	authority *workspaces.Service
	admin     identity.Auth
	portal    identity.Auth
	store     transaction.Store
	policy    []QuotaNode
	attempt   Attempt
	intent    JobIntent
}

func newAuthenticatedQuotaFixture(t *testing.T) authenticatedQuotaFixture {
	t.Helper()
	accounts := identityfixture.New(t)
	password := identityfixture.Bootstrap(t, accounts)
	admin := identityfixture.Login(t, accounts, "admin@example.invalid", password, "admin")
	portal := identityfixture.Login(t, accounts, "admin@example.invalid", password, "portal")
	store, err := accounts.Repository()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.DB.Exec(t.Context(), testQuotaSQL+testReservationSQL); err != nil {
		t.Fatal(err)
	}
	scope := portal.Me.Workspace
	limit := Resources{CPU: 4000, NVIDIAGPU: 2}
	policy := []QuotaNode{
		{ID: "platform", Level: PlatformQuota, Limits: Quota{limit, limit}},
		{ID: scope.TenantID, ParentID: "platform", TenantID: scope.TenantID, Level: TenantQuota, Limits: Quota{limit, limit}},
		{ID: "department", ParentID: scope.TenantID, TenantID: scope.TenantID, Level: DepartmentQuota, Limits: Quota{limit, limit}},
		{ID: scope.ID, ParentID: "department", TenantID: scope.TenantID, Level: WorkspaceQuota, Limits: Quota{limit, limit}},
	}
	attempt := Attempt{TenantID: scope.TenantID, WorkspaceID: scope.ID, ID: uuid.NewString(),
		Number: 1, Kind: Training, SourceID: uuid.NewString()}
	intent := JobIntent{Version: IntentVersion, Kind: attempt.Kind, SourceID: attempt.SourceID,
		Groups: []ReplicaGroup{{Name: "worker", Replicas: 1, Requests: Resources{CPU: 1000, NVIDIAGPU: 1}}}}
	return authenticatedQuotaFixture{accounts, &workspaces.Service{DB: accounts.DB, Identity: accounts},
		admin, portal, store, policy, attempt, intent}
}

func (f authenticatedQuotaFixture) replace(t *testing.T, revision int64, policy []QuotaNode) {
	t.Helper()
	if err := pgx.BeginFunc(t.Context(), f.accounts.DB, func(tx pgx.Tx) error {
		_, _, err := ReplaceQuotaWithin(t.Context(), tx, revision, policy)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func (f authenticatedQuotaFixture) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.accounts.DB.Exec(t.Context(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func (f authenticatedQuotaFixture) read(t *testing.T, actor identity.Auth) WorkspaceQuotaStatus {
	t.Helper()
	out, err := ReadWorkspaceQuotaStatus(t.Context(), f.authority, actor, f.attempt.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPostgresWorkspaceQuotaStatusAuthorityAndAccounting(t *testing.T) {
	f := newAuthenticatedQuotaFixture(t)
	workspace := f.attempt.WorkspaceID
	want := WorkspaceQuotaStatus{WorkspaceID: workspace, State: WorkspaceQuotaUnconfigured,
		Reason: &WorkspaceQuotaReason{Code: "SCHEDULING_QUOTA_UNCONFIGURED", Action: ConfigureQuotaPolicy}}
	for _, actor := range []identity.Auth{f.admin, f.portal} {
		if got := f.read(t, actor); !reflect.DeepEqual(got, want) {
			t.Fatalf("missing policy invented allocation: %+v", got)
		}
	}

	f.replace(t, 0, f.policy[:3])
	want.PolicyRevision, want.State = 1, WorkspaceQuotaUnallocated
	want.Reason = &WorkspaceQuotaReason{Code: "SCHEDULING_QUOTA_UNALLOCATED", Action: ConfigureWorkspaceQuota}
	if got := f.read(t, f.portal); !reflect.DeepEqual(got, want) {
		t.Fatalf("missing allocation: %+v", got)
	}
	f.replace(t, 1, f.policy)
	want = WorkspaceQuotaStatus{WorkspaceID: workspace, PolicyRevision: 2, State: WorkspaceQuotaConfigured,
		Resources: []WorkspaceQuotaResource{
			{Resource: CPU, Guaranteed: "4000", HardLimit: "4000", Reserved: "0", UnreservedQuota: "4000"},
			{Resource: NVIDIAGPU, Guaranteed: "2", HardLimit: "2", Reserved: "0", UnreservedQuota: "2"},
		}}
	for _, actor := range []identity.Auth{f.admin, f.portal} {
		if got := f.read(t, actor); !reflect.DeepEqual(got, want) {
			t.Fatalf("configured accounting: %+v", got)
		}
	}
	// Roles come from current membership. Resource allocation is readable by
	// ordinary members without granting them policy mutation authority.
	f.exec(t, "UPDATE workspace_memberships SET role='viewer' WHERE tenant_id=$1 AND workspace_id=$2 AND user_id=$3",
		f.attempt.TenantID, workspace, f.portal.Me.User.ID)
	if got := f.read(t, f.portal); !reflect.DeepEqual(got, want) {
		t.Fatalf("current viewer cannot read own allocation: %+v", got)
	}

	foreignTenant, foreignWorkspace := uuid.NewString(), uuid.NewString()
	f.exec(t, "INSERT INTO tenants(id,name,status) VALUES($1,'Synthetic foreign tenant','active')", foreignTenant)
	f.exec(t, "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Synthetic foreign workspace','active')", foreignWorkspace, foreignTenant)
	f.exec(t, "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,'owner')",
		f.portal.Me.User.ID, foreignTenant, foreignWorkspace)
	privateWorkspace := uuid.NewString()
	f.exec(t, "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Synthetic private workspace','active')",
		privateWorkspace, f.attempt.TenantID)
	for _, actor := range []identity.Auth{f.admin, f.portal} {
		for _, selector := range []string{foreignWorkspace, uuid.NewString()} {
			out, err := ReadWorkspaceQuotaStatus(t.Context(), f.authority, actor, selector)
			if err == nil || err.Error() != "RESOURCE_NOT_FOUND" || !reflect.DeepEqual(out, WorkspaceQuotaStatus{}) {
				t.Fatalf("foreign/missing scope exposed configuration: %+v, %v", out, err)
			}
		}
	}
	if out, err := ReadWorkspaceQuotaStatus(t.Context(), f.authority, f.portal, privateWorkspace); err == nil || err.Error() != "RESOURCE_NOT_FOUND" || !reflect.DeepEqual(out, WorkspaceQuotaStatus{}) {
		t.Fatalf("nonmember exposed another allocation: %+v, %v", out, err)
	}
	spoofed := f.portal
	cached := *f.portal.Me.Workspace
	cached.ID, cached.TenantID, cached.Role = foreignWorkspace, foreignTenant, "owner"
	spoofed.Me.Workspace = &cached
	if got := f.read(t, spoofed); !reflect.DeepEqual(got, want) {
		t.Fatalf("cached actor scope replaced live scope: %+v", got)
	}

	if changed, err := reserveInTransaction(t.Context(), f.store, f.attempt, f.intent); err != nil || !changed {
		t.Fatalf("committed reservation: %v, %v", changed, err)
	}
	want.Resources[0].Reserved, want.Resources[0].UnreservedQuota = "1000", "3000"
	want.Resources[1].Reserved, want.Resources[1].UnreservedQuota = "1", "1"
	if got := f.read(t, f.portal); !reflect.DeepEqual(got, want) {
		t.Fatalf("committed usage missing: %+v", got)
	}
	rolledBack := errors.New("SYNTHETIC_OWNER_ROLLBACK")
	next := f.attempt
	next.ID, next.SourceID = uuid.NewString(), uuid.NewString()
	intent := f.intent
	intent.SourceID = next.SourceID
	if err := ownerTransaction(t.Context(), f.store, func(ctx context.Context, q transaction.Executor) error {
		if _, err := ReserveWithin(ctx, q, next, intent); err != nil {
			return err
		}
		return rolledBack
	}); !errors.Is(err, rolledBack) {
		t.Fatal(err)
	}
	if got := f.read(t, f.portal); !reflect.DeepEqual(got, want) {
		t.Fatalf("rolled-back usage was counted: %+v", got)
	}

	// Run the real callback on the selected store. Inject only its outcome,
	// never simulated scope or reservation rows.
	for _, failure := range []error{errors.New("SYNTHETIC_PRIVATE_FAILURE"),
		errors.Join(context.Canceled, &transaction.Error{Kind: transaction.CommitUnknown})} {
		fault := &quotaStatusFaultStore{Store: f.store, failure: failure}
		accountCopy := *f.accounts
		accountCopy.DB, accountCopy.Storage = nil, fault
		out, err := ReadWorkspaceQuotaStatus(t.Context(), &workspaces.Service{Identity: &accountCopy}, f.portal, workspace)
		if err == nil || !fault.reached || !reflect.DeepEqual(out, WorkspaceQuotaStatus{}) {
			t.Fatalf("failed selected transaction exposed payload: %+v, %v", out, err)
		}
		if _, unknown := transaction.CommitOutcome(failure); unknown && err.Error() != string(transaction.CommitUnknown) {
			t.Fatalf("uncertain read became cancellation: %v", err)
		}
	}

	if changed, err := reserveInTransaction(t.Context(), f.store, next, intent); err != nil || !changed {
		t.Fatalf("second committed reservation: %v, %v", changed, err)
	}
	// A positive row must not hide a negative or fractional row in SUM.
	for _, corrupt := range []string{
		`{"cpu":5000}`, `{"cpu":-1000}`, `{"cpu":-0.5}`,
		`{"cpu":"1000"}`, `{"cpu":9223372036854775808}`, `{"unknown":1}`,
	} {
		f.exec(t, "UPDATE scheduling_reservations SET demand=$1::jsonb WHERE attempt_id=$2", corrupt, f.attempt.ID)
		out, err := ReadWorkspaceQuotaStatus(t.Context(), f.authority, f.portal, workspace)
		if !errors.Is(err, ErrQuotaPolicyUnavailable) || !reflect.DeepEqual(out, WorkspaceQuotaStatus{}) {
			t.Fatalf("invalid usage exposed quantities: %+v, %v", out, err)
		}
	}
	f.exec(t, "UPDATE scheduling_reservations SET demand=$1::jsonb WHERE attempt_id=$2",
		`{"cpu":1000,"nvidia.com/gpu":1}`, f.attempt.ID)
	f.exec(t, "INSERT INTO cells(id,site_id,display_name) VALUES('foreign',local_site_id(),'Synthetic foreign cell')")
	f.exec(t, "UPDATE scheduling_reservations SET cell_id='foreign' WHERE attempt_id=$1", f.attempt.ID)
	if out, err := ReadWorkspaceQuotaStatus(t.Context(), f.authority, f.portal, workspace); !errors.Is(err, ErrQuotaPolicyUnavailable) || !reflect.DeepEqual(out, WorkspaceQuotaStatus{}) {
		t.Fatalf("foreign-cell usage was treated as local: %+v, %v", out, err)
	}
	f.exec(t, "UPDATE scheduling_reservations SET cell_id=local_cell_id() WHERE attempt_id=$1", f.attempt.ID)
	if changed, err := releaseInTransaction(t.Context(), f.store, next); err != nil || !changed {
		t.Fatalf("second committed release: %v, %v", changed, err)
	}

	if changed, err := releaseInTransaction(t.Context(), f.store, f.attempt); err != nil || !changed {
		t.Fatalf("committed release: %v, %v", changed, err)
	}
	want.Resources[0].Reserved, want.Resources[0].UnreservedQuota = "0", "4000"
	want.Resources[1].Reserved, want.Resources[1].UnreservedQuota = "0", "2"
	if got := f.read(t, f.portal); !reflect.DeepEqual(got, want) {
		t.Fatalf("released quota remained held: %+v", got)
	}
	var reservations int
	if err := f.accounts.DB.QueryRow(t.Context(), "SELECT count(*) FROM scheduling_reservations").Scan(&reservations); err != nil || reservations != 2 {
		t.Fatalf("read or rolled-back reserve changed durable rows: %d, %v", reservations, err)
	}
	f.exec(t, "DELETE FROM workspace_memberships WHERE tenant_id=$1 AND workspace_id=$2 AND user_id=$3",
		f.attempt.TenantID, workspace, f.portal.Me.User.ID)
	if out, err := ReadWorkspaceQuotaStatus(t.Context(), f.authority, f.portal, workspace); err == nil || err.Error() != "RESOURCE_NOT_FOUND" || !reflect.DeepEqual(out, WorkspaceQuotaStatus{}) {
		t.Fatalf("removed member retained cached authority: %+v, %v", out, err)
	}
	f.exec(t, "UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='logout' WHERE id=$1", f.admin.SessionID)
	if out, err := ReadWorkspaceQuotaStatus(t.Context(), f.authority, f.admin, workspace); err == nil || err.Error() != "AUTH_SESSION_EXPIRED" || !reflect.DeepEqual(out, WorkspaceQuotaStatus{}) {
		t.Fatalf("revoked session exposed quota: %+v, %v", out, err)
	}
}

type quotaStatusFaultStore struct {
	transaction.Store
	before  func(context.Context, transaction.Executor) error
	failure error
	reached bool
}

func (s *quotaStatusFaultStore) Within(ctx context.Context, read bool, fn func(context.Context, transaction.Executor) error) error {
	return s.Store.Within(ctx, read, func(ctx context.Context, q transaction.Executor) error {
		if s.before != nil {
			if err := s.before(ctx, q); err != nil {
				return err
			}
		}
		if err := fn(ctx, q); err != nil {
			return err
		}
		s.reached = true
		return s.failure
	})
}

func TestPostgresWorkspaceQuotaStatusWaitsForReservationCommit(t *testing.T) {
	f := newAuthenticatedQuotaFixture(t)
	f.replace(t, 0, f.policy)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	hold, err := f.accounts.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Rollback(ctx)
	if _, err := ReserveWithin(ctx, transaction.PostgreSQLTransaction(hold), f.attempt, f.intent); err != nil {
		t.Fatal(err)
	}
	entered := make(chan int64, 1)
	probe := &quotaStatusFaultStore{Store: f.store, before: func(ctx context.Context, q transaction.Executor) error {
		var pid int64
		if err := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT pg_backend_pid()")).Scan(&pid); err != nil {
			return err
		}
		entered <- pid
		return nil
	}}
	copy := *f.accounts
	copy.DB, copy.Storage = nil, probe
	type result struct {
		out WorkspaceQuotaStatus
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := ReadWorkspaceQuotaStatus(ctx, &workspaces.Service{Identity: &copy}, f.portal, f.attempt.WorkspaceID)
		done <- result{out, err}
	}()
	var pid int64
	select {
	case pid = <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var blockers int
		if err := f.accounts.DB.QueryRow(ctx, "SELECT cardinality(pg_blocking_pids($1::integer))", pid).Scan(&blockers); err != nil {
			t.Fatal(err)
		}
		if blockers > 0 {
			break
		}
		select {
		case got := <-done:
			t.Fatalf("read bypassed reservation fence: %+v, %v", got.out, got.err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
	if err := hold.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if got.err != nil || len(got.out.Resources) != 2 || got.out.Resources[0].Reserved != "1000" ||
			got.out.Resources[1].Reserved != "1" {
			t.Fatalf("read did not see the committed hold after waiting: %+v, %v", got.out, got.err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
