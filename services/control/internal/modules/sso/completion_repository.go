package sso

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"errors"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func (s *Service) failFlow(ctx context.Context, f claimedFlow, code, requestID string) error {
	if !validClaimedFlow(f) || !canonicalID(requestID) {
		return fail("SSO_FLOW_INVALID")
	}
	store, e := s.catalogRepository()
	if e != nil {
		return e
	}
	if e := s.policy.Validate(); e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy.CleanupTimeoutSeconds))
	defer cancel()
	code = publicFlowError(fail(code)).Error()
	if code == "STORAGE_UNAVAILABLE" || code == string(transaction.CommitUnknown) || code == string(transaction.CommitRolledBack) {
		code = "SSO_FLOW_UNUSABLE"
	}
	return sourceOutcome(store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var id string
		if e := q.QueryRow(ctx, sourceSQL(q, `SELECT id::text FROM sso_connectors WHERE tenant_id=$1 AND id=$2 FOR SHARE`, `SELECT id FROM sso_connectors WHERE tenant_id=? AND id=? FOR SHARE`, "SELECT ID FROM SSO_CONNECTORS WHERE TENANT_ID=? AND ID=? FOR UPDATE OF ID", f.tenant, f.connector)).Scan(&id); e != nil {
			return e
		}
		tenant, e := connectorTenant(ctx, q, connectorReadFence)
		if e != nil {
			return e
		}
		if tenant != f.tenant {
			return fail("SSO_FLOW_UNUSABLE")
		}
		changed, e := q.Exec(ctx, sourceSQL(q, `UPDATE sso_flows SET state='failed',error_code=$1,finished_at=clock_timestamp() WHERE tenant_id=$2 AND connector_id=$3 AND id=$4 AND claim_id=$5 AND state='claimed'`, `UPDATE sso_flows SET state='failed',error_code=?,finished_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND connector_id=? AND id=? AND claim_id=? AND state='claimed'`, "UPDATE SSO_FLOWS SET STATE='failed',ERROR_CODE=?,FINISHED_AT=CURRENT_TIMESTAMP(6) WHERE TENANT_ID=? AND CONNECTOR_ID=? AND ID=? AND CLAIM_ID=? AND STATE='claimed'", code, f.tenant, f.connector, f.id, *f.claim))
		if e != nil || changed == 0 {
			return e
		}
		if changed != 1 {
			return fail("STORAGE_UNAVAILABLE")
		}
		return audit.WriteWithin(ctx, q, "", "", f.audience, "sso.flow_failed", "failure", "sso_flow", f.id, "", requestID)
	}))
}

func (s *Service) completionConnectorLocks(ctx context.Context, q transaction.Executor, f claimedFlow) error {
	locks := []connectorLock{{f.tenant, f.connector}}
	if f.previousActor != nil {
		a := f.previousActor
		var tenant, connector sql.NullString
		e := q.QueryRow(ctx, sourceSQL(q, `SELECT sso_tenant_id::text,sso_connector_id::text FROM sessions WHERE id=$1 AND user_id=$2 AND token_hash=$3 AND audience=$4 AND revoked_at IS NULL`, `SELECT sso_tenant_id,sso_connector_id FROM sessions WHERE id=? AND user_id=? AND token_hash=? AND audience=? AND revoked_at IS NULL`, "SELECT SSO_TENANT_ID,SSO_CONNECTOR_ID FROM SESSIONS WHERE ID=? AND USER_ID=? AND TOKEN_HASH=? AND AUDIENCE=? AND REVOKED_AT IS NULL", a.SessionID, a.Me.User.ID, a.TokenHash, a.Me.Audience)).Scan(&tenant, &connector)
		if errors.Is(e, sql.ErrNoRows) {
			return fail("AUTH_SESSION_EXPIRED")
		}
		if e != nil {
			return e
		}
		if tenant.Valid != connector.Valid || tenant.Valid && (!canonicalID(tenant.String) || !canonicalID(connector.String)) {
			return fail("AUTH_SESSION_EXPIRED")
		}
		if tenant.Valid {
			locks = append(locks, connectorLock{tenant.String, connector.String})
		}
	}
	for _, lock := range connectorLocks(locks) {
		var id string
		if e := q.QueryRow(ctx, sourceSQL(q, `SELECT id::text FROM sso_connectors WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, `SELECT id FROM sso_connectors WHERE tenant_id=? AND id=? FOR UPDATE`, "SELECT ID FROM SSO_CONNECTORS WHERE TENANT_ID=? AND ID=? FOR UPDATE OF ID", lock.tenant, lock.id)).Scan(&id); e != nil {
			if errors.Is(e, sql.ErrNoRows) {
				return fail("SSO_FLOW_UNUSABLE")
			}
			return e
		}
	}
	return nil
}

func (s *Service) commitFlow(ctx context.Context, f claimedFlow, subject, previous, requestID string) (identity.IssuedSession, error) {
	if !canonicalSubject(subject) {
		return identity.IssuedSession{}, fail("SSO_IDENTITY_REJECTED")
	}
	if !validClaimedFlow(f) || !canonicalID(requestID) || !hmac.Equal(f.previousHash, security.Digest(security.Binding(previous))) {
		return identity.IssuedSession{}, fail("SSO_FLOW_INVALID")
	}
	if s == nil || s.Identity == nil {
		return identity.IssuedSession{}, fail("SSO_CONFIGURATION_REQUIRED")
	}
	if !s.enabled {
		return identity.IssuedSession{}, fail("SSO_DISABLED")
	}
	store, e := s.catalogRepository()
	if e != nil {
		return identity.IssuedSession{}, sourceOutcome(e)
	}
	if e := s.policy.Validate(); e != nil {
		return identity.IssuedSession{}, sourceOutcome(e)
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy.OperationSeconds))
	defer cancel()
	var user string
	if f.mode == ModeLink {
		user = *f.user
	} else {
		e := store.QueryRow(ctx, sourceSQL(store, `SELECT user_id::text FROM sso_bindings WHERE tenant_id=$1 AND connector_id=$2 AND subject=$3 AND active`, `SELECT user_id FROM sso_bindings WHERE tenant_id=? AND connector_id=? AND subject=? AND active=1`, "SELECT USER_ID FROM SSO_BINDINGS WHERE TENANT_ID=? AND CONNECTOR_ID=? AND SUBJECT=? AND ACTIVE=1", f.tenant, f.connector, subject)).Scan(&user)
		if errors.Is(e, sql.ErrNoRows) {
			return identity.IssuedSession{}, fail("SSO_BINDING_REQUIRED")
		}
		if e != nil {
			return identity.IssuedSession{}, sourceOutcome(e)
		}
	}
	if !canonicalID(user) {
		return identity.IssuedSession{}, fail("STORAGE_UNAVAILABLE")
	}
	var issued identity.IssuedSession
	e = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		if e := s.Identity.LockSessionUsersWithin(ctx, q, user, previous, f.audience); e != nil {
			return e
		}
		if e := s.completionConnectorLocks(ctx, q, f); e != nil {
			return e
		}
		c, e := selectConnectorWithin(ctx, q, f.tenant, f.connector)
		if e != nil {
			return e
		}
		if !c.Enabled || c.Revision != f.connectorRevision || c.AuthVersion != f.authVersion {
			return fail("SSO_FLOW_UNUSABLE")
		}
		if f.previousActor != nil {
			if e := s.Identity.RecheckWithin(ctx, q, *f.previousActor); e != nil {
				return e
			}
		}
		if f.mode == ModeLink {
			if e := s.Identity.RecheckCredentialVersionWithin(ctx, q, *f.previousActor, *f.credential); e != nil {
				return e
			}
		}
		tenant, e := connectorTenant(ctx, q, connectorReadFence)
		if e != nil {
			return e
		}
		if tenant != f.tenant {
			return fail("SSO_FLOW_UNUSABLE")
		}
		var binding, boundUser string
		var active bool
		var revision int64
		e = q.QueryRow(ctx, sourceSQL(q, `SELECT id::text,user_id::text,active,revision FROM sso_bindings WHERE tenant_id=$1 AND connector_id=$2 AND subject=$3 FOR UPDATE`, `SELECT id,user_id,active,revision FROM sso_bindings WHERE tenant_id=? AND connector_id=? AND subject=? FOR UPDATE`, "SELECT ID,USER_ID,ACTIVE,REVISION FROM SSO_BINDINGS WHERE TENANT_ID=? AND CONNECTOR_ID=? AND SUBJECT=? FOR UPDATE OF ID", f.tenant, f.connector, subject)).Scan(&binding, &boundUser, &active, &revision)
		if errors.Is(e, sql.ErrNoRows) && f.mode == ModeLink {
			var count int
			if e := q.QueryRow(ctx, sourceSQL(q, `SELECT COUNT(*) FROM sso_bindings WHERE tenant_id=$1 AND user_id=$2`, `SELECT COUNT(*) FROM sso_bindings WHERE tenant_id=? AND user_id=?`, "SELECT COUNT(*) FROM SSO_BINDINGS WHERE TENANT_ID=? AND USER_ID=?", f.tenant, user)).Scan(&count); e != nil {
				return e
			}
			if count >= s.policy.MaximumBindingsPerUser {
				return fail("SSO_BINDING_LIMIT")
			}
			binding, boundUser, active, revision = uuid.NewString(), user, true, 1
			var changed int64
			changed, e = q.Exec(ctx, sourceSQL(q, `INSERT INTO sso_bindings(id,tenant_id,connector_id,subject,user_id) VALUES($1,$2,$3,$4,$5)`, `INSERT INTO sso_bindings(id,tenant_id,connector_id,subject,user_id) VALUES(?,?,?,?,?)`, "INSERT INTO SSO_BINDINGS(ID,TENANT_ID,CONNECTOR_ID,SUBJECT,USER_ID) VALUES(?,?,?,?,?)", binding, f.tenant, f.connector, subject, user))
			if e == nil && changed != 1 {
				return fail("STORAGE_UNAVAILABLE")
			}
		}
		if errors.Is(e, sql.ErrNoRows) {
			return fail("SSO_BINDING_REQUIRED")
		}
		if e != nil {
			return e
		}
		if !canonicalID(binding) || !canonicalID(boundUser) || revision < 1 || revision > identity.MaximumAuthVersion {
			return fail("STORAGE_UNAVAILABLE")
		}
		if boundUser != user {
			return fail("SSO_BINDING_CONFLICT")
		}
		if !active {
			if f.mode != ModeLink {
				return fail("SSO_BINDING_REQUIRED")
			}
			if revision >= identity.MaximumAuthVersion {
				return fail("SSO_FLOW_UNUSABLE")
			}
			changed, e := q.Exec(ctx, sourceSQL(q, `UPDATE sso_bindings SET active=true,revision=revision+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND id=$2 AND user_id=$3 AND revision=$4 AND NOT active`, `UPDATE sso_bindings SET active=1,revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND id=? AND user_id=? AND revision=? AND active=0`, "UPDATE SSO_BINDINGS SET ACTIVE=1,REVISION=REVISION+1,UPDATED_AT=CURRENT_TIMESTAMP(6) WHERE TENANT_ID=? AND ID=? AND USER_ID=? AND REVISION=? AND ACTIVE=0", f.tenant, binding, user, revision))
			if e != nil {
				return e
			}
			if changed != 1 {
				return fail("SSO_FLOW_UNUSABLE")
			}
		}
		locked, e := scanFlow(q.QueryRow(ctx, sourceSQL(q, `SELECT `+flowColumns+` FROM sso_flows WHERE tenant_id=$1 AND connector_id=$2 AND id=$3 FOR UPDATE`, `SELECT `+mysqlFlowColumns+` FROM sso_flows WHERE tenant_id=? AND connector_id=? AND id=? FOR UPDATE`, "SELECT "+damengFlowColumns+" FROM SSO_FLOWS WHERE TENANT_ID=? AND CONNECTOR_ID=? AND ID=? FOR UPDATE OF ID", f.tenant, f.connector, f.id)))
		if e != nil {
			return e
		}
		if !validPrivateFlow(locked) {
			return fail("STORAGE_UNAVAILABLE")
		}
		if locked.state != flowClaimed || locked.claim == nil || *locked.claim != *f.claim || locked.mode != f.mode || locked.audience != f.audience || locked.connectorRevision != f.connectorRevision || locked.authVersion != f.authVersion {
			return fail("SSO_FLOW_UNUSABLE")
		}
		var fresh bool
		if e := q.QueryRow(ctx, sourceSQL(q, `SELECT expires_at>clock_timestamp() FROM sso_flows WHERE tenant_id=$1 AND id=$2`, `SELECT expires_at>UTC_TIMESTAMP(6) FROM sso_flows WHERE tenant_id=? AND id=?`, "SELECT CASE WHEN EXPIRES_AT>CURRENT_TIMESTAMP(6) THEN 1 ELSE 0 END FROM SSO_FLOWS WHERE TENANT_ID=? AND ID=?", f.tenant, f.id)).Scan(&fresh); e != nil {
			return e
		}
		if !fresh {
			return fail("SSO_FLOW_EXPIRED")
		}
		var epoch int64
		if e := q.QueryRow(ctx, sourceSQL(q, `SELECT auth_epoch FROM users WHERE id=$1 AND status='active'`, `SELECT auth_epoch FROM users WHERE id=? AND status='active'`, "SELECT AUTH_EPOCH FROM USERS WHERE ID=? AND STATUS='active'", user)).Scan(&epoch); e != nil {
			if errors.Is(e, sql.ErrNoRows) {
				return fail("AUTH_SESSION_EXPIRED")
			}
			return e
		}
		changed, e := q.Exec(ctx, sourceSQL(q, `UPDATE sso_flows SET state='succeeded',authenticated_user_id=$1,finished_at=clock_timestamp() WHERE tenant_id=$2 AND id=$3 AND claim_id=$4 AND state='claimed'`, `UPDATE sso_flows SET state='succeeded',authenticated_user_id=?,finished_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND id=? AND claim_id=? AND state='claimed'`, "UPDATE SSO_FLOWS SET STATE='succeeded',AUTHENTICATED_USER_ID=?,FINISHED_AT=CURRENT_TIMESTAMP(6) WHERE TENANT_ID=? AND ID=? AND CLAIM_ID=? AND STATE='claimed'", user, f.tenant, f.id, *f.claim))
		if e != nil {
			return e
		}
		if changed != 1 {
			return fail("SSO_FLOW_UNUSABLE")
		}
		source := identity.SessionSource{TenantID: f.tenant, ConnectorID: f.connector, BindingID: binding, FlowID: f.id, AuthVersion: f.authVersion}
		issued, e = s.Identity.IssueSessionWithin(ctx, q, user, epoch, f.audience, previous, requestID, &source)
		if e != nil {
			return e
		}
		if f.mode == ModeLink {
			return audit.WriteWithin(ctx, q, user, user, f.audience, "sso.binding_linked", "success", "sso_binding", binding, *f.reason, requestID)
		}
		return nil
	})
	if e != nil {
		return identity.IssuedSession{}, sourceOutcome(e)
	}
	return issued, nil
}
