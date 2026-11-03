package sso

import (
	"context"
	"database/sql"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func sourceSQL(q transaction.Executor, pg, my, dm string, args ...any) transaction.Statement {
	if q == nil {
		return transaction.Statement{}
	}
	switch q.Dialect() {
	case transaction.PostgreSQL:
		return transaction.SQL(transaction.PostgreSQL, pg, args...)
	case transaction.MySQL:
		return transaction.SQL(transaction.MySQL, my, args...)
	case transaction.Dameng:
		return transaction.SQL(transaction.Dameng, dm, args...)
	default:
		return transaction.Statement{}
	}
}

func sourceValid(source identity.SessionSource, user string) bool {
	return canonicalID(user) && canonicalID(source.TenantID) && canonicalID(source.ConnectorID) && canonicalID(source.BindingID) && canonicalID(source.FlowID) && source.AuthVersion > 0 && source.AuthVersion <= identity.MaximumAuthVersion
}

// CheckSessionSource retains the actual pgx caller transaction or performs an
// unlocked read through the trusted owning store. It never opens a transaction.
func (s *Service) CheckSessionSource(ctx context.Context, tx pgx.Tx, source identity.SessionSource, user string) error {
	if s == nil || !s.enabled || !sourceValid(source, user) {
		return fail("AUTH_SESSION_EXPIRED")
	}
	store, e := s.Identity.Repository()
	if e != nil {
		return e
	}
	if tx != nil {
		if store.Dialect() != transaction.PostgreSQL {
			return fail("SSO_TRANSACTION_STORAGE_MISMATCH")
		}
		return s.checkSource(ctx, transaction.PostgreSQLTransaction(tx), true, source, user)
	}
	return s.checkSource(ctx, store, false, source, user)
}

// CheckSessionSourceWithin holds connector then binding shared locks through the
// caller's real transaction. The caller must acquire its user locks first.
func (s *Service) CheckSessionSourceWithin(ctx context.Context, tx transaction.Executor, source identity.SessionSource, user string) error {
	if s == nil || !s.enabled || !sourceValid(source, user) {
		return fail("AUTH_SESSION_EXPIRED")
	}
	store, e := s.Identity.Repository()
	if e != nil {
		return e
	}
	if tx == nil || tx.Dialect() != store.Dialect() {
		return fail("SSO_TRANSACTION_STORAGE_MISMATCH")
	}
	return s.checkSource(ctx, tx, true, source, user)
}

func (s *Service) checkSource(ctx context.Context, q transaction.Executor, locked bool, source identity.SessionSource, user string) error {
	pgConnectorLock, pgBindingLock, myLock := "", "", ""
	damengConnectorLock, damengBindingLock := "", ""
	if locked {
		pgConnectorLock, pgBindingLock, myLock = " FOR SHARE OF c", " FOR SHARE OF b", " FOR SHARE"
		damengConnectorLock, damengBindingLock = " FOR UPDATE OF C.ID", " FOR UPDATE OF B.ID"
	}
	var origin string
	e := q.QueryRow(ctx, sourceSQL(q,
		`SELECT c.origin FROM sso_connectors c JOIN installation_state i ON i.tenant_id=c.tenant_id JOIN tenants t ON t.id=c.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND t.status='active' AND c.tenant_id=$1 AND c.id=$2 AND c.enabled AND c.auth_version=$3`+pgConnectorLock,
		`SELECT c.origin FROM sso_connectors c WHERE c.tenant_id=? AND c.id=? AND c.enabled=1 AND c.auth_version=? AND EXISTS(SELECT 1 FROM installation_state i JOIN tenants t ON t.id=i.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND i.tenant_id=c.tenant_id AND t.status='active')`+myLock, "SELECT C.ORIGIN FROM SSO_CONNECTORS C WHERE C.TENANT_ID=? AND C.ID=? AND C.ENABLED=1 AND C.AUTH_VERSION=? AND EXISTS(SELECT 1 FROM INSTALLATION_STATE I JOIN TENANTS T ON T.ID=I.TENANT_ID WHERE I.ID=1 AND I.BOOTSTRAP_COMPLETE=1 AND I.TENANT_ID=C.TENANT_ID AND T.STATUS='active')"+damengConnectorLock,
		source.TenantID, source.ConnectorID, source.AuthVersion)).Scan(&origin)
	if errors.Is(e, sql.ErrNoRows) {
		return fail("AUTH_SESSION_EXPIRED")
	}
	if e != nil {
		return e
	}
	if _, approved := s.approved[origin]; !approved {
		return fail("AUTH_SESSION_EXPIRED")
	}
	var id string
	e = q.QueryRow(ctx, sourceSQL(q,
		`SELECT b.id::text FROM sso_bindings b WHERE b.tenant_id=$1 AND b.connector_id=$2 AND b.id=$3 AND b.user_id=$4 AND b.active`+pgBindingLock,
		`SELECT b.id FROM sso_bindings b WHERE b.tenant_id=? AND b.connector_id=? AND b.id=? AND b.user_id=? AND b.active=1`+myLock, "SELECT B.ID FROM SSO_BINDINGS B WHERE B.TENANT_ID=? AND B.CONNECTOR_ID=? AND B.ID=? AND B.USER_ID=? AND B.ACTIVE=1"+damengBindingLock,
		source.TenantID, source.ConnectorID, source.BindingID, user)).Scan(&id)
	if errors.Is(e, sql.ErrNoRows) {
		return fail("AUTH_SESSION_EXPIRED")
	}
	if e != nil {
		return e
	}
	var valid bool
	e = q.QueryRow(ctx, sourceSQL(q,
		`SELECT EXISTS(SELECT 1 FROM sso_flows WHERE tenant_id=$1 AND connector_id=$2 AND id=$3 AND authenticated_user_id=$4 AND auth_version=$5 AND state='succeeded')`,
		`SELECT EXISTS(SELECT 1 FROM sso_flows WHERE tenant_id=? AND connector_id=? AND id=? AND authenticated_user_id=? AND auth_version=? AND state='succeeded')`, "SELECT CASE WHEN EXISTS(SELECT 1 FROM SSO_FLOWS WHERE TENANT_ID=? AND CONNECTOR_ID=? AND ID=? AND AUTHENTICATED_USER_ID=? AND AUTH_VERSION=? AND STATE='succeeded') THEN 1 ELSE 0 END FROM DUAL",
		source.TenantID, source.ConnectorID, source.FlowID, user, source.AuthVersion)).Scan(&valid)
	if e != nil {
		return e
	}
	if !valid {
		return fail("AUTH_SESSION_EXPIRED")
	}
	return nil
}
