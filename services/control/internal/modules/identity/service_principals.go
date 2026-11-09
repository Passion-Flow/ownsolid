package identity

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// ServicePrincipalScope identifies the exact tenant and workspace of a principal.
type ServicePrincipalScope struct {
	TenantID    string
	WorkspaceID string
}

// ServicePrincipalRef identifies a workload principal without carrying credentials.
type ServicePrincipalRef struct {
	Scope ServicePrincipalScope
	ID    string
}

// ServicePrincipal is a workload identity and its management provenance.
type ServicePrincipal struct {
	Ref         ServicePrincipalRef
	DisplayName string
	CreatedAt   time.Time
	ExpiresAt   time.Time
	CreatedBy   string
	RevokedAt   *time.Time
	RevokedBy   *string
}

// ServicePrincipalCreate describes an immutable creation intent.
type ServicePrincipalCreate struct {
	DisplayName    string
	ExpiresAt      time.Time
	IdempotencyKey string
}

const servicePrincipalFields = "id::text,display_name,created_at,expires_at,created_by::text,revoked_at,revoked_by::text"
const servicePrincipalUnavailable = "SERVICE_PRINCIPAL_UNAVAILABLE"

func (p ServicePrincipalScope) valid() bool {
	return sessionID(p.TenantID) && sessionID(p.WorkspaceID)
}

func (s *Service) servicePrincipalTransaction(ctx context.Context, tx transaction.Executor) error {
	_, pool := tx.(transaction.Store)
	if ctx == nil || tx == nil || pool {
		return failure("IDENTITY_TRANSACTION_REQUIRED")
	}
	if err := s.sessionExecutor(tx); err != nil {
		return err
	}
	if tx.Dialect() != transaction.PostgreSQL {
		return failure("SERVICE_PRINCIPAL_DIALECT_UNSUPPORTED")
	}
	return nil
}

// Lock the exact workspace before touching principal rows. Reuse the installed
// tenant predicate so a foreign scope cannot acquire a workspace lock here.
func lockServicePrincipalWorkspace(ctx context.Context, tx transaction.Executor, scope ServicePrincipalScope, active bool, denied string) error {
	const query = "SELECT w.status FROM workspaces w WHERE w.tenant_id=$1 AND w.id=$2 AND w.tenant_id=" + invitationTenantSQL + " FOR SHARE OF w"
	var status string
	err := tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, query, scope.TenantID, scope.WorkspaceID)).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return failure(denied)
	}
	if err != nil {
		return err
	}
	if active && status != "active" {
		return failure(denied)
	}
	return nil
}

func (s *Service) servicePrincipalManager(ctx context.Context, tx transaction.Executor, a Auth, scope ServicePrincipalScope, active bool) error {
	if err := s.servicePrincipalTransaction(ctx, tx); err != nil {
		return err
	}
	if !sessionActor(a) {
		return failure("AUTH_SESSION_EXPIRED")
	}
	if a.Me.Audience != "admin" {
		return failure("ACCESS_DENIED")
	}
	if err := s.RecheckWithin(ctx, tx, a); err != nil {
		return err
	}
	return lockServicePrincipalWorkspace(ctx, tx, scope, active, "RESOURCE_NOT_FOUND")
}

func scanServicePrincipal(row transaction.Row, scope ServicePrincipalScope) (ServicePrincipal, error) {
	var p ServicePrincipal
	err := row.Scan(&p.Ref.ID, &p.DisplayName, &p.CreatedAt, &p.ExpiresAt, &p.CreatedBy, &p.RevokedAt, &p.RevokedBy)
	if err != nil {
		return ServicePrincipal{}, err
	}
	p.Ref.Scope = scope
	p.CreatedAt, p.ExpiresAt = p.CreatedAt.UTC(), p.ExpiresAt.UTC()
	if p.RevokedAt != nil {
		stamp := p.RevokedAt.UTC()
		p.RevokedAt = &stamp
	}
	return p, nil
}

// CreateServicePrincipalWithin creates a scoped workload principal and its human
// management audit in the caller's transaction. Return any error to roll back.
func (s *Service) CreateServicePrincipalWithin(ctx context.Context, tx transaction.Executor, a Auth, scope ServicePrincipalScope, input ServicePrincipalCreate, requestID string) (ServicePrincipal, error) {
	label, err := security.Name(input.DisplayName)
	expires := input.ExpiresAt.UTC().Truncate(time.Microsecond)
	if err != nil || !scope.valid() || !sessionID(input.IdempotencyKey) || !sessionID(requestID) || !listTime(expires) {
		return ServicePrincipal{}, failure("VALIDATION_FAILED")
	}
	if err := s.servicePrincipalManager(ctx, tx, a, scope, true); err != nil {
		return ServicePrincipal{}, err
	}
	// The manager's user lock serializes retries, including across sessions of
	// the same administrator. The scoped unique constraint backs that invariant.
	const lookup = "SELECT " + servicePrincipalFields + " FROM service_principals WHERE tenant_id=$1 AND workspace_id=$2 AND created_by=$3 AND creation_key=$4"
	p, err := scanServicePrincipal(tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, lookup,
		scope.TenantID, scope.WorkspaceID, a.Me.User.ID, input.IdempotencyKey)), scope)
	if err == nil {
		if p.DisplayName != label || !p.ExpiresAt.Equal(expires) {
			return ServicePrincipal{}, failure("IDEMPOTENCY_CONFLICT")
		}
		return p, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ServicePrincipal{}, err
	}
	const insert = `INSERT INTO service_principals(id,tenant_id,workspace_id,display_name,created_by,creation_key,created_at,expires_at)
		SELECT $1::uuid,$2::uuid,$3::uuid,$4,$5::uuid,$6::uuid,statement_timestamp(),$7::timestamptz WHERE $7::timestamptz>statement_timestamp()
		RETURNING ` + servicePrincipalFields
	p, err = scanServicePrincipal(tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, insert,
		uuid.NewString(), scope.TenantID, scope.WorkspaceID, label, a.Me.User.ID, input.IdempotencyKey, expires)), scope)
	if errors.Is(err, sql.ErrNoRows) {
		return ServicePrincipal{}, failure("VALIDATION_FAILED")
	}
	if err != nil {
		return ServicePrincipal{}, err
	}
	if err := auditServicePrincipalManagement(ctx, tx, a, p.Ref, "identity.service_principal_created", "", requestID); err != nil {
		return ServicePrincipal{}, err
	}
	return p, nil
}

// RevokeServicePrincipalWithin permanently ends a principal, including in an
// archived workspace. An already revoked principal retains its first outcome.
func (s *Service) RevokeServicePrincipalWithin(ctx context.Context, tx transaction.Executor, a Auth, ref ServicePrincipalRef, reason, requestID string) (ServicePrincipal, error) {
	reason = strings.TrimSpace(reason)
	if !ref.Scope.valid() || !sessionID(ref.ID) || !sessionID(requestID) || !recoveryReason(reason) || strings.ContainsRune(reason, 0) {
		return ServicePrincipal{}, failure("VALIDATION_FAILED")
	}
	if err := s.servicePrincipalManager(ctx, tx, a, ref.Scope, false); err != nil {
		return ServicePrincipal{}, err
	}
	const where = " WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3"
	const query = "SELECT " + servicePrincipalFields + " FROM service_principals" + where + " FOR NO KEY UPDATE"
	p, err := scanServicePrincipal(tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, query,
		ref.Scope.TenantID, ref.Scope.WorkspaceID, ref.ID)), ref.Scope)
	if errors.Is(err, sql.ErrNoRows) {
		return ServicePrincipal{}, failure("RESOURCE_NOT_FOUND")
	}
	if err != nil || p.RevokedAt != nil {
		return p, err
	}
	const update = "UPDATE service_principals SET revoked_at=greatest(clock_timestamp(),created_at),revoked_by=$4" + where + " RETURNING " + servicePrincipalFields
	p, err = scanServicePrincipal(tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, update,
		ref.Scope.TenantID, ref.Scope.WorkspaceID, ref.ID, a.Me.User.ID)), ref.Scope)
	if err != nil {
		return ServicePrincipal{}, err
	}
	if err := auditServicePrincipalManagement(ctx, tx, a, ref, "identity.service_principal_revoked", reason, requestID); err != nil {
		return ServicePrincipal{}, err
	}
	return p, nil
}

// RecheckServicePrincipalWithin checks a principal after the credential owner has
// authenticated its existing key and resolved the gateway workspace. The caller
// keeps this transaction open through admission. This check grants no key authority.
func (s *Service) RecheckServicePrincipalWithin(ctx context.Context, tx transaction.Executor, ref ServicePrincipalRef) error {
	if err := s.servicePrincipalTransaction(ctx, tx); err != nil {
		return err
	}
	if !ref.Scope.valid() || !sessionID(ref.ID) {
		return failure(servicePrincipalUnavailable)
	}
	if err := lockServicePrincipalWorkspace(ctx, tx, ref.Scope, true, servicePrincipalUnavailable); err != nil {
		return err
	}
	// Evaluate the database clock after the locked row is materialized, including
	// when a concurrent writer holds the row until after its expiry.
	const query = `WITH held AS MATERIALIZED (
		SELECT revoked_at,expires_at FROM service_principals WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 FOR SHARE
	) SELECT revoked_at IS NULL AND expires_at>clock_timestamp() FROM held`
	var active bool
	err := tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, query,
		ref.Scope.TenantID, ref.Scope.WorkspaceID, ref.ID)).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !active {
		return failure(servicePrincipalUnavailable)
	}
	return err
}

func auditServicePrincipalManagement(ctx context.Context, tx transaction.Executor, a Auth, ref ServicePrincipalRef, action, reason, requestID string) error {
	// Account audit defaults to the installation's default workspace. A principal
	// audit must retain the explicit workspace and the distinct workload target.
	const insert = `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id)
		VALUES($1,clock_timestamp(),$2,$3,$4,$5,$6,'success','service_principal',$7,NULLIF($8,''),$9)`
	n, err := tx.Exec(ctx, transaction.SQL(transaction.PostgreSQL, insert,
		uuid.NewString(), a.Me.User.ID, ref.Scope.TenantID, ref.Scope.WorkspaceID, a.Me.Audience, action, ref.ID, reason, requestID))
	if err == nil && n != 1 {
		return failure("AUDIT_WRITE_UNCONFIRMED")
	}
	return err
}
