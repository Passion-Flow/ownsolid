package identity_test

import (
	"context"
	_ "embed"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

//go:embed testdata/service_principals.sql
var servicePrincipalFixtureSQL string

func TestServicePrincipalRepository(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	s := identityfixture.New(t)
	password := identityfixture.Bootstrap(t, s)
	const adminEmail = "admin@example.invalid"
	_, token, _, err := s.Login(ctx, adminEmail, password, "admin", "", uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	admin, err := s.Authenticate(ctx, token, "admin")
	if err != nil {
		t.Fatal(err)
	}
	portal := identityfixture.Login(t, s, adminEmail, password, "portal")
	store, err := s.Repository()
	if err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := s.DB.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(servicePrincipalFixtureSQL)
	var tenant, sibling string
	if err := s.DB.QueryRow(ctx, "SELECT tenant_id::text,default_workspace_id::text FROM installation_state WHERE id=1").Scan(&tenant, &sibling); err != nil {
		t.Fatal(err)
	}
	workspace, foreignTenant, foreignWorkspace := uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec("INSERT INTO tenants(id,name,status) VALUES($1,'Foreign','active')", foreignTenant)
	exec(`INSERT INTO workspaces(id,tenant_id,name,status)
		VALUES($1,$2,'Principal workspace','active'),($3,$4,'Foreign workspace','active')`,
		workspace, tenant, foreignWorkspace, foreignTenant)
	scope := identity.ServicePrincipalScope{TenantID: tenant, WorkspaceID: workspace}
	otherScope := identity.ServicePrincipalScope{TenantID: tenant, WorkspaceID: sibling}
	foreignScope := identity.ServicePrincipalScope{TenantID: foreignTenant, WorkspaceID: foreignWorkspace}
	mismatch := identity.ServicePrincipalScope{TenantID: tenant, WorkspaceID: foreignWorkspace}
	newIntent := func() identity.ServicePrincipalCreate {
		return identity.ServicePrincipalCreate{DisplayName: "Synthetic workload", ExpiresAt: time.Now().UTC().Add(time.Hour), IdempotencyKey: uuid.NewString()}
	}
	create := func(actor identity.Auth, scope identity.ServicePrincipalScope, input identity.ServicePrincipalCreate) (identity.ServicePrincipal, error) {
		var p identity.ServicePrincipal
		err := store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
			var err error
			p, err = s.CreateServicePrincipalWithin(ctx, tx, actor, scope, input, uuid.NewString())
			return err
		})
		return p, err
	}
	createOwned := func(input identity.ServicePrincipalCreate) identity.ServicePrincipal {
		t.Helper()
		p, err := create(admin, scope, input)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	revoke := func(ref identity.ServicePrincipalRef, reason string) (identity.ServicePrincipal, error) {
		var p identity.ServicePrincipal
		err := store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
			var err error
			p, err = s.RevokeServicePrincipalWithin(ctx, tx, admin, ref, reason, uuid.NewString())
			return err
		})
		return p, err
	}
	recheck := func(ref identity.ServicePrincipalRef) error {
		return store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
			return s.RecheckServicePrincipalWithin(ctx, tx, ref)
		})
	}
	want := func(err error, code string) {
		t.Helper()
		if err == nil || err.Error() != code {
			t.Fatalf("expected %s, got %v", code, err)
		}
	}
	const unavailable = "SERVICE_PRINCIPAL_UNAVAILABLE"
	const reason = "Synthetic operator withdrawal"

	input := newIntent()
	p := createOwned(input)
	if p.Ref.Scope != scope || p.Ref.ID == admin.Me.User.ID || p.CreatedBy != admin.Me.User.ID ||
		p.RevokedAt != nil || !p.ExpiresAt.Equal(input.ExpiresAt.Truncate(time.Microsecond)) {
		t.Fatal("created principal lost its independent identity or provenance")
	}
	if replay := createOwned(input); replay.Ref != p.Ref || !replay.CreatedAt.Equal(p.CreatedAt) {
		t.Fatal("creation retry generated a new principal")
	}
	other, err := create(admin, otherScope, input)
	if err != nil || other.Ref.ID == p.Ref.ID || other.Ref.Scope != otherScope {
		t.Fatal("creation idempotency escaped the workspace", err)
	}
	conflict := input
	conflict.DisplayName = "Different workload"
	_, err = create(admin, scope, conflict)
	want(err, "IDEMPOTENCY_CONFLICT")
	_, err = create(portal, scope, newIntent())
	want(err, "ACCESS_DENIED")
	foreignClaim := admin
	foreignClaim.Me.Workspace = &identity.Workspace{ID: foreignWorkspace, TenantID: foreignTenant, Role: "owner"}
	for _, foreign := range []identity.ServicePrincipalScope{foreignScope, mismatch} {
		_, err := create(foreignClaim, foreign, newIntent())
		want(err, "RESOURCE_NOT_FOUND")
	}
	if err := recheck(p.Ref); err != nil {
		t.Fatal(err)
	}
	want(s.RecheckServicePrincipalWithin(ctx, store, p.Ref), "IDENTITY_TRANSACTION_REQUIRED")
	if _, err := s.Authenticate(ctx, p.Ref.ID, "admin"); err == nil || err.Error() != "AUTH_REQUIRED" {
		t.Fatalf("principal ID accepted as browser authority: %v", err)
	}

	// Even a real row in the foreign scope cannot become installation authority.
	foreignID := uuid.NewString()
	exec(`INSERT INTO service_principals(id,tenant_id,workspace_id,display_name,created_by,creation_key,created_at,expires_at)
		VALUES($1,$2,$3,'Foreign principal',$4,$5,clock_timestamp(),clock_timestamp()+interval '1 hour')`,
		foreignID, foreignTenant, foreignWorkspace, admin.Me.User.ID, uuid.NewString())
	for _, ref := range []identity.ServicePrincipalRef{
		{Scope: foreignScope, ID: foreignID},
		{Scope: foreignScope, ID: p.Ref.ID},
		{Scope: mismatch, ID: p.Ref.ID},
		{Scope: otherScope, ID: p.Ref.ID},
		{Scope: scope, ID: uuid.NewString()},
	} {
		want(recheck(ref), unavailable)
		_, err := revoke(ref, reason)
		want(err, "RESOURCE_NOT_FOUND")
	}
	if err := recheck(p.Ref); err != nil {
		t.Fatal("foreign attempts altered local authority", err)
	}

	expiredInput := newIntent()
	expiredInput.ExpiresAt = time.Now().Add(-time.Minute)
	_, err = create(admin, scope, expiredInput)
	want(err, "VALIDATION_FAILED")
	expired := createOwned(newIntent())
	exec(`UPDATE service_principals SET created_at=clock_timestamp()-interval '1 hour',expires_at=clock_timestamp()-interval '1 minute'
		WHERE id=$1`, expired.Ref.ID)
	want(recheck(expired.Ref), unavailable)

	exec("ALTER TABLE audit_events ADD CONSTRAINT fixture_refuses_principal_revoke CHECK(action<>'identity.service_principal_revoked') NOT VALID")
	if _, err := revoke(p.Ref, reason); err == nil {
		t.Fatal("revocation unexpectedly succeeded without its audit")
	}
	if err := recheck(p.Ref); err != nil {
		t.Fatal("failed audit left the principal revoked", err)
	}
	exec("ALTER TABLE audit_events DROP CONSTRAINT fixture_refuses_principal_revoke")

	exec("UPDATE workspaces SET status='disabled' WHERE id=$1", workspace)
	want(recheck(p.Ref), unavailable)
	_, err = create(admin, scope, newIntent())
	want(err, "RESOURCE_NOT_FOUND")
	revoked, err := revoke(p.Ref, reason)
	if err != nil || revoked.RevokedAt == nil || revoked.RevokedBy == nil || *revoked.RevokedBy != admin.Me.User.ID {
		t.Fatalf("cannot revoke a principal in an archived workspace: %v", err)
	}
	exec("UPDATE workspaces SET status='active' WHERE id=$1", workspace)
	want(recheck(p.Ref), unavailable)
	replayedRevoke, err := revoke(p.Ref, "Different retry explanation")
	if err != nil || replayedRevoke.RevokedAt == nil || !replayedRevoke.RevokedAt.Equal(*revoked.RevokedAt) {
		t.Fatal("revocation retry changed the outcome", err)
	}
	if replay := createOwned(input); replay.Ref != p.Ref || replay.RevokedAt == nil {
		t.Fatal("creation retry reactivated a revoked principal")
	}

	var events, exact, identities int
	if err := s.DB.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE actor_user_id=$2 AND subject_user_id IS NULL
		AND tenant_id=$3 AND workspace_id=$4 AND target_type='service_principal' AND audience='admin'
		AND metadata='{}'::jsonb AND identifier_digest IS NULL
		AND (action='identity.service_principal_created' AND reason IS NULL OR action='identity.service_principal_revoked' AND reason=$5))
		FROM audit_events WHERE target_id=$1`, p.Ref.ID, admin.Me.User.ID, tenant, workspace, reason).Scan(&events, &exact); err != nil || events != 2 || exact != 2 {
		t.Fatalf("expected one exact audit per transition, got %d/%d: %v", exact, events, err)
	}
	if err := s.DB.QueryRow(ctx, "SELECT (SELECT count(*) FROM users WHERE id=$1)+(SELECT count(*) FROM sessions WHERE user_id=$1)", p.Ref.ID).Scan(&identities); err != nil || identities != 0 {
		t.Fatalf("workload created a browser identity: %v", err)
	}

	concurrent := createOwned(newIntent())
	holding, err := s.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holding.Rollback(ctx)
	if err := s.RecheckServicePrincipalWithin(ctx, transaction.PostgreSQLTransaction(holding), concurrent.Ref); err != nil {
		t.Fatal(err)
	}
	err = whilePrincipalLocked(t, ctx, s.DB, holding, func() error { return holding.Commit(ctx) }, func(tx transaction.Executor) error {
		_, err := s.RevokeServicePrincipalWithin(ctx, tx, admin, concurrent.Ref, reason, uuid.NewString())
		return err
	})
	if err != nil {
		t.Fatal("serialized revoke failed", err)
	}
	want(recheck(concurrent.Ref), unavailable)

	short := newIntent()
	short.ExpiresAt = time.Now().UTC().Add(2 * time.Second)
	expiresWhileWaiting := createOwned(short)
	locker, err := s.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer locker.Rollback(ctx)
	if _, err := locker.Exec(ctx, "SELECT id FROM service_principals WHERE id=$1 FOR UPDATE", expiresWhileWaiting.Ref.ID); err != nil {
		t.Fatal(err)
	}
	err = whilePrincipalLocked(t, ctx, s.DB, locker, func() error {
		var stillLive bool
		if err := locker.QueryRow(ctx, "SELECT clock_timestamp()<$1", expiresWhileWaiting.ExpiresAt).Scan(&stillLive); err != nil {
			return err
		}
		if !stillLive {
			return errors.New("principal expired before the competing check was observed waiting")
		}
		if _, err := locker.Exec(ctx, "SELECT pg_sleep_until($1)", expiresWhileWaiting.ExpiresAt); err != nil {
			return err
		}
		return locker.Commit(ctx)
	}, func(tx transaction.Executor) error {
		return s.RecheckServicePrincipalWithin(ctx, tx, expiresWhileWaiting.Ref)
	})
	want(err, unavailable)

	if err := s.Logout(ctx, token, "admin", uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	_, err = create(admin, scope, newIntent())
	want(err, "AUTH_SESSION_EXPIRED")
	if err := recheck(other.Ref); err != nil {
		t.Fatal("a workload depended on its creator's browser session", err)
	}
}

func whilePrincipalLocked(t *testing.T, ctx context.Context, pool *pgxpool.Pool, holder pgx.Tx, release func() error, action func(transaction.Executor) error) error {
	t.Helper()
	waiting, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	holderPID, waitingPID := holder.Conn().PgConn().PID(), waiting.Conn().PgConn().PID()
	done := make(chan error, 1)
	go func() {
		err := action(transaction.PostgreSQLTransaction(waiting))
		if err == nil {
			err = waiting.Commit(ctx)
		}
		_ = waiting.Rollback(context.WithoutCancel(ctx))
		done <- err
	}()
	joined := false
	defer func() {
		_ = holder.Rollback(context.WithoutCancel(ctx))
		if !joined {
			<-done
		}
	}()
	deadline, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ticks := time.NewTicker(10 * time.Millisecond)
	defer ticks.Stop()
	for {
		var blocked bool
		if err := pool.QueryRow(deadline, "SELECT $1::int=ANY(pg_blocking_pids($2::int))", int32(holderPID), int32(waitingPID)).Scan(&blocked); err != nil {
			t.Fatal("could not observe the principal fence", err)
		}
		if blocked {
			break
		}
		select {
		case err := <-done:
			joined = true
			t.Fatalf("authority operation crossed the held principal lock: %v", err)
		case <-deadline.Done():
			t.Fatal(errors.New("principal lock did not block the competing operation"))
		case <-ticks.C:
		}
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	err = <-done
	joined = true
	return err
}
