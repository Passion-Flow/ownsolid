package models

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
)

func historyAvailable(ctx context.Context, q transaction.Executor) error {
	if q == nil || q.Dialect() != transaction.PostgreSQL {
		return fail("MODEL_HISTORY_UNAVAILABLE")
	}
	var present bool
	err := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT
 to_regclass(format('%I.model_families',current_schema())) IS NOT NULL AND
 to_regclass(format('%I.model_family_members',current_schema())) IS NOT NULL`)).Scan(&present)
	if err != nil {
		return modelReadError(err)
	}
	if !present {
		return fail("MODEL_HISTORY_UNAVAILABLE")
	}
	return nil
}

func versionFamilyWithin(ctx context.Context, q transaction.Executor, tenant, workspace, version string) (string, error) {
	var family string
	err := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `
SELECT m.family_id::text FROM model_family_members m
JOIN model_families f ON (f.tenant_id,f.workspace_id,f.id)=(m.tenant_id,m.workspace_id,m.family_id)
JOIN model_family_members r ON (r.tenant_id,r.workspace_id,r.family_id,r.version_id)=(f.tenant_id,f.workspace_id,f.id,f.initial_version_id)
WHERE m.tenant_id=$1 AND m.workspace_id=$2 AND m.version_id=$3`, tenant, workspace, version)).Scan(&family)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !nonzeroModelID(family) {
		return "", fail("MODEL_FAMILY_INVALID")
	}
	return family, modelReadError(err)
}

func nonzeroModelID(value string) bool { return validID(value) && value != uuid.Nil.String() }

// StageVersionFamilyWithin selects immutable membership before the caller inserts
// its exact version. Empty anchor creates a new family. The caller owns publication,
// commit and suppression of results on unknown commit outcomes. An orphan cannot commit.
func (s *Service) StageVersionFamilyWithin(ctx context.Context, q transaction.Executor, a identity.Auth, workspace, version, anchor, reason, request string) (string, error) {
	if !admin(a) {
		return "", fail("ACCESS_DENIED")
	}
	if !nonzeroModelID(workspace) || !nonzeroModelID(version) || !nonzeroModelID(request) ||
		!reasonValid(reason) || anchor != "" && (!nonzeroModelID(anchor) || anchor == version) {
		return "", fail("VALIDATION_FAILED")
	}
	store, err := s.repository()
	if err != nil {
		return "", err
	}
	if _, pool := q.(transaction.Store); q == nil || pool || q.Dialect() != store.Dialect() {
		return "", fail("MODEL_STORAGE_MISMATCH")
	}
	if err = s.Identity.RecheckOrderedWithin(ctx, q, a); err != nil {
		return "", err
	}
	w, err := s.modelScope(ctx, q, a, workspace)
	if err != nil {
		return "", err
	}
	if w.Status != "active" {
		return "", fail("ACCESS_DENIED")
	}
	if err = historyAvailable(ctx, q); err != nil {
		return "", err
	}
	var family string
	if anchor != "" {
		if _, err = s.retainedVersionWithin(ctx, q, w.TenantID, w.ID, anchor); err != nil {
			return "", err
		}
		family, err = versionFamilyWithin(ctx, q, w.TenantID, w.ID, anchor)
		if err != nil {
			return "", err
		}
	}
	if _, err = q.Exec(ctx, transaction.SQL(transaction.PostgreSQL,
		`SELECT pg_advisory_xact_lock(hashtextextended('ownsolid.models.family:'||$1::text,0))`, version)); err != nil {
		return "", modelReadError(err)
	}
	var previous string
	var previousAnchor *string
	err = q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL,
		`SELECT family_id::text,anchor_version_id::text FROM model_family_members WHERE tenant_id=$1 AND workspace_id=$2 AND version_id=$3`, w.TenantID, w.ID, version)).Scan(&previous, &previousAnchor)
	if err == nil {
		matches := anchor == "" && previousAnchor == nil || previousAnchor != nil && *previousAnchor == anchor && previous == family
		if !matches {
			return "", fail("MODEL_FAMILY_CONFLICT")
		}
		return previous, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", modelReadError(err)
	}
	if anchor == "" {
		family = uuid.NewString()
		if _, err = modelExecOne(ctx, q, transaction.SQL(transaction.PostgreSQL,
			`INSERT INTO model_families(id,tenant_id,workspace_id,initial_version_id) VALUES($1,$2,$3,$4)`, family, w.TenantID, w.ID, version)); err != nil {
			return "", modelReadError(err)
		}
	}
	if _, err = modelExecOne(ctx, q, transaction.SQL(transaction.PostgreSQL,
		`INSERT INTO model_family_members(version_id,tenant_id,workspace_id,family_id,anchor_version_id) VALUES($1,$2,$3,$4,NULLIF($5,'')::uuid)`, version, w.TenantID, w.ID, family, anchor)); err != nil {
		return "", modelReadError(err)
	}
	err = workerFamilyAudit(ctx, q, a, w.TenantID, w.ID, version, reason, request)
	if err != nil {
		return "", modelReadError(err)
	}
	return family, nil
}

func workerFamilyAudit(ctx context.Context, q transaction.Executor, a identity.Auth, tenant, workspace, version, reason, request string) error {
	_, err := modelExecOne(ctx, q, transaction.SQL(transaction.PostgreSQL, `
INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id)
VALUES($1,clock_timestamp(),$2,$3,$4,'admin','model.version_family_selected','success','model_version',$5,$6,$7)`,
		uuid.NewString(), a.Me.User.ID, tenant, workspace, version, reason, request))
	return err
}
