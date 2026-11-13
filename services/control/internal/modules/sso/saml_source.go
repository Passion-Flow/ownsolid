package sso

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/jackc/pgx/v5"
)

// SAMLCompletion owns Portal result completion and composes durable SAML source
// authority with the existing Gitea owner. It never falls back after a SAML error.
type SAMLCompletion struct {
	service *Service
	legacy  identity.SourceGuard
}

// CheckSessionSource retains the caller transaction or performs a bounded source read.
func (g *SAMLCompletion) CheckSessionSource(ctx context.Context, tx pgx.Tx, source identity.SessionSource, user string) error {
	if ctx == nil || g == nil || g.service == nil || !sourceValid(source, user) {
		return fail("AUTH_SESSION_EXPIRED")
	}
	store, e := g.service.Identity.Repository()
	if e := g.ready(); e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(g.service.policy.OperationSeconds))
	defer cancel()
	if e != nil {
		return e
	}
	if store.Dialect() != transaction.PostgreSQL {
		return fail("SSO_TRANSACTION_STORAGE_MISMATCH")
	}
	if tx != nil {
		return g.check(ctx, transaction.PostgreSQLTransaction(tx), true, source, user)
	}
	return g.check(ctx, store, false, source, user)
}

// CheckSessionSourceWithin retains source fences through the caller transaction.
func (g *SAMLCompletion) CheckSessionSourceWithin(ctx context.Context, q transaction.Executor, source identity.SessionSource, user string) error {
	if ctx == nil || g == nil || g.service == nil || q == nil || q.Dialect() != transaction.PostgreSQL || !sourceValid(source, user) {
		return fail("AUTH_SESSION_EXPIRED")
	}
	return g.check(ctx, q, true, source, user)
}
func (g *SAMLCompletion) check(ctx context.Context, q transaction.Executor, locked bool, source identity.SessionSource, user string) error {
	if e := g.ready(); e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(g.service.policy.OperationSeconds))
	defer cancel()
	var protocol string
	e := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT provider FROM sso_connectors WHERE tenant_id=$1 AND id=$2`, source.TenantID, source.ConnectorID)).Scan(&protocol)
	if errors.Is(e, sql.ErrNoRows) {
		return fail("AUTH_SESSION_EXPIRED")
	}
	if e != nil {
		return e
	}
	switch protocol {
	case ProviderGitea:
		if g.legacy == nil {
			return fail("AUTH_SESSION_EXPIRED")
		}
		if locked {
			return g.legacy.CheckSessionSourceWithin(ctx, q, source, user)
		}
		return g.legacy.CheckSessionSource(ctx, nil, source, user)
	case "saml":
	default:
		return fail("AUTH_SESSION_EXPIRED")
	}
	connectorLock, sourceLock := "", ""
	if locked {
		connectorLock = " FOR SHARE OF c"
		sourceLock = " FOR SHARE OF p,b,f,r"
	}
	var id string
	var sourceUntil, trustUntil time.Time
	e = q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT c.id::text FROM sso_connectors c JOIN installation_state i ON i.tenant_id=c.tenant_id JOIN tenants t ON t.id=c.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND t.status='active' AND c.tenant_id=$1 AND c.id=$2 AND c.provider='saml' AND c.enabled AND c.auth_version=$3`+connectorLock, source.TenantID, source.ConnectorID, source.AuthVersion)).Scan(&id)
	if errors.Is(e, sql.ErrNoRows) {
		return fail("AUTH_SESSION_EXPIRED")
	}
	if e != nil {
		return e
	}
	e = q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT b.id::text,r.source_until,p.trust_until FROM sso_saml_profiles p JOIN sso_saml_identities b ON b.tenant_id=p.tenant_id AND b.context_id=p.id JOIN sso_saml_results r ON r.tenant_id=b.tenant_id AND r.context_id=b.context_id AND r.identity_id=b.id JOIN sso_saml_requests f ON f.tenant_id=r.tenant_id AND f.id=r.flow_id AND f.connector_id=p.connector_id WHERE p.tenant_id=$1 AND p.connector_id=$2 AND b.id=$3 AND f.id=$4 AND f.context_id=p.id AND r.request_id=f.request_id AND f.authenticated_user_id=$5 AND b.bound_user_id=$5 AND b.active AND b.binding_version=r.completed_binding_version AND p.enabled AND p.revision=f.context_revision AND p.metadata_digest=f.metadata_digest AND f.auth_version=$6 AND f.state='succeeded' AND r.source_until>clock_timestamp() AND p.trust_until>clock_timestamp()`+sourceLock, source.TenantID, source.ConnectorID, source.BindingID, source.FlowID, user, source.AuthVersion)).Scan(&id, &sourceUntil, &trustUntil)
	if errors.Is(e, sql.ErrNoRows) {
		return fail("AUTH_SESSION_EXPIRED")
	}
	if e != nil {
		return e
	}
	if locked {
		var fresh bool
		if e = q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT clock_timestamp()<LEAST($1::timestamptz,$2::timestamptz)`, sourceUntil, trustUntil)).Scan(&fresh); e != nil {
			return e
		}
		if !fresh {
			return fail("AUTH_SESSION_EXPIRED")
		}
	}
	return nil
}

var _ identity.SourceGuard = (*SAMLCompletion)(nil)
