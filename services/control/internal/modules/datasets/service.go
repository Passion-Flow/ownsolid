// Package datasets owns scoped editable text datasets, durable preparation jobs and immutable published versions.
package datasets

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"strings"
	"unicode"
	"unicode/utf8"

	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

type Service struct {
	DB              *pgxpool.Pool
	Identity        *identity.Service
	Workspaces      *workspaces.Service
	PublishedReader *PublishedReader
	Store           *Store
	Processor       *Processor
}

func (s *Service) policy() config.DatasetPolicy { return s.Identity.Config.Policy().Datasets }
func isAdmin(a identity.Auth) bool              { return a.Me.Audience == "admin" && a.Me.User.IsPlatformAdmin }
func isOwner(a identity.Auth, w workspaces.Workspace) bool {
	return isAdmin(a) || w.Role != nil && *w.Role == "owner"
}
func canWrite(a identity.Auth, w workspaces.Workspace) bool {
	return isAdmin(a) || a.Me.Audience == "portal" && w.Role != nil && (*w.Role == "owner" || *w.Role == "member")
}
func validText(v string, maximum int) bool {
	if !utf8.ValidString(v) || strings.TrimSpace(v) == "" || utf8.RuneCountInString(v) > maximum {
		return false
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func validReason(v string) bool { return validText(v, 500) }
func fingerprint(input any, actor string) []byte {
	raw, _ := json.Marshal(struct {
		Actor string
		Input any
	}{actor, input})
	digest := sha256.Sum256(raw)
	return digest[:]
}
func sameFingerprint(a, b []byte) bool { return subtle.ConstantTimeCompare(a, b) == 1 }
func (s *Service) transaction(ctx context.Context, a identity.Auth, workspace string, write bool, fn func(context.Context, transaction.Executor, workspaces.Workspace) error) error {
	if ctx == nil || !wire.ValidID(workspace) || a.Me.Audience != "portal" && !isAdmin(a) {
		return failure("ACCESS_DENIED")
	}
	store, e := s.repository()
	if e != nil {
		return e
	}
	if s.Workspaces == nil {
		return failure("DATASET_IDENTITY_REQUIRED")
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().CommitSeconds))
	defer cancel()
	e = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		if e := s.Identity.RecheckOrderedWithin(ctx, q, a); e != nil {
			return e
		}
		w, e := s.Workspaces.RecheckScopeWithin(ctx, q, a, workspace)
		if e != nil {
			return e
		}
		if w.Status != "active" || write && !canWrite(a, w) {
			return failure("ACCESS_DENIED")
		}
		return fn(ctx, q, w)
	})
	return datasetRuntimeError(e)
}
func auditEvent(ctx context.Context, tx transaction.Executor, a identity.Auth, w workspaces.Workspace, action, target, id, reason, requestID string) error {
	_, e := tx.Exec(ctx, datasetSQL(tx, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES($1,clock_timestamp(),$2,$3,$4,$5,$6,'success',$7,$8,$9,$10)`, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,CURRENT_TIMESTAMP(6),?,?,?,?,?,'success',?,?,?,?)`, []any{uuid.NewString(), a.Me.User.ID, w.TenantID, w.ID, a.Me.Audience, action, target, id, reason, requestID}, []any{uuid.NewString(), a.Me.User.ID, w.TenantID, w.ID, a.Me.Audience, action, target, id, reason, requestID}, transaction.SQL(transaction.Dameng, dmAuditInsert, uuid.NewString(), a.Me.User.ID, w.TenantID, w.ID, a.Me.Audience, action, target, id, reason, requestID)))
	return e
}
func delegatedAuthority(ctx context.Context, tx transaction.Executor, a identity.Auth, w workspaces.Workspace) (*string, error) {
	if isAdmin(a) {
		return nil, nil
	}
	var id string
	e := tx.QueryRow(ctx, datasetSQL(tx, "SELECT authority_id::text FROM workspace_memberships WHERE tenant_id=$1 AND workspace_id=$2 AND user_id=$3", `SELECT authority_id FROM workspace_memberships WHERE tenant_id=? AND workspace_id=? AND user_id=?`, []any{w.TenantID, w.ID, a.Me.User.ID}, []any{w.TenantID, w.ID, a.Me.User.ID}, transaction.SQL(transaction.Dameng, dmDelegatedAuthority, w.TenantID, w.ID, a.Me.User.ID))).Scan(&id)
	if e != nil {
		return nil, e
	}
	return &id, nil
}

const datasetFields = `id::text,workspace_id::text,name,origin,license,rights_asserted,created_by::text,revision,created_at,updated_at,retired_at,retire_reason`

func scanDataset(row transaction.Row) (Dataset, error) {
	var d Dataset
	e := row.Scan(&d.ID, &d.WorkspaceID, &d.Name, &d.Origin, &d.License, &d.RightsAsserted, &d.CreatedBy, &d.Revision, &d.CreatedAt, &d.UpdatedAt, &d.RetiredAt, &d.RetireReason)
	if errors.Is(e, pgx.ErrNoRows) {
		return Dataset{}, failure("DATASET_NOT_FOUND")
	}
	if e != nil {
		return Dataset{}, e
	}
	d.CreatedAt = d.CreatedAt.UTC()
	d.UpdatedAt = d.UpdatedAt.UTC()
	if d.RetiredAt != nil {
		stamp := d.RetiredAt.UTC()
		d.RetiredAt = &stamp
	}

	return committed(d, e)
}
func lockedDataset(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, id string) (Dataset, error) {
	if !wire.ValidID(id) {
		return Dataset{}, failure("DATASET_NOT_FOUND")
	}
	return scanDataset(tx.QueryRow(ctx, datasetSQL(tx, "SELECT "+datasetFields+" FROM datasets WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 FOR UPDATE", `SELECT id,workspace_id,name,origin,license,rights_asserted,created_by,revision,created_at,updated_at,retired_at,retire_reason FROM datasets WHERE tenant_id=? AND workspace_id=? AND id=? FOR UPDATE`, []any{w.TenantID, w.ID, id}, []any{w.TenantID, w.ID, id}, transaction.SQL(transaction.Dameng, dmDatasetLock, w.TenantID, w.ID, id))))
}
func (s *Service) Create(ctx context.Context, a identity.Auth, input CreateRequest, key, requestID string) (Dataset, error) {
	var out Dataset
	name, e := security.Name(input.Name)
	if e != nil {
		return committed(out, e)
	}
	input.Name = name
	if !wire.ValidID(key) || !validText(input.Origin, 2000) || !validText(input.License, 500) || !validReason(input.Reason) || !input.RightsAsserted {
		return out, failure("VALIDATION_FAILED")
	}
	intent := fingerprint(input, a.Me.User.ID)
	e = s.transaction(ctx, a, input.WorkspaceID, true, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		var original []byte
		var id string
		e := tx.QueryRow(ctx, datasetSQL(tx, "SELECT id::text,creation_fingerprint FROM datasets WHERE tenant_id=$1 AND workspace_id=$2 AND created_by=$3 AND creation_key=$4", `SELECT id,creation_fingerprint FROM datasets WHERE tenant_id=? AND workspace_id=? AND created_by=? AND creation_key=?`, []any{w.TenantID, w.ID, a.Me.User.ID, key}, []any{w.TenantID, w.ID, a.Me.User.ID, key}, transaction.SQL(transaction.Dameng, dmDatasetFindIntent, w.TenantID, w.ID, a.Me.User.ID, key))).Scan(&id, &original)
		if e == nil {
			if !sameFingerprint(original, intent) {
				return failure("IDEMPOTENCY_CONFLICT")
			}
			out, e = lockedDataset(ctx, tx, w, id)
			return e
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		var count int
		if e = tx.QueryRow(ctx, datasetSQL(tx, "SELECT count(*) FROM datasets WHERE tenant_id=$1 AND workspace_id=$2 AND retired_at IS NULL", `SELECT count(*) FROM datasets WHERE tenant_id=? AND workspace_id=? AND retired_at IS NULL`, []any{w.TenantID, w.ID}, []any{w.TenantID, w.ID}, transaction.SQL(transaction.Dameng, dmDatasetActiveCount, w.TenantID, w.ID))).Scan(&count); e != nil {
			return e
		}
		if count >= s.policy().MaxDatasetsPerWorkspace {
			return failure("DATASET_LIMIT_REACHED")
		}
		id = uuid.NewString()
		nameKey := cases.Fold().String(norm.NFKC.String(name))
		if utf8.RuneCountInString(nameKey) > 200 {
			return failure("VALIDATION_FAILED")
		}
		_, e = tx.Exec(ctx, datasetSQL(tx, `INSERT INTO datasets(id,tenant_id,workspace_id,name,name_key,origin,license,rights_asserted,created_by,creation_key,creation_fingerprint) VALUES($1,$2,$3,$4,$5,$6,$7,true,$8,$9,$10)`, `INSERT INTO datasets(id,tenant_id,workspace_id,name,name_key,origin,license,rights_asserted,created_by,creation_key,creation_fingerprint) VALUES(?,?,?,?,?,?,?,true,?,?,?)`, []any{id, w.TenantID, w.ID, input.Name, nameKey, input.Origin, input.License, a.Me.User.ID, key, intent}, []any{id, w.TenantID, w.ID, input.Name, nameKey, input.Origin, input.License, a.Me.User.ID, key, intent}, transaction.SQL(transaction.Dameng, dmDatasetInsert, id, w.TenantID, w.ID, input.Name, nameKey, input.Origin, input.License, a.Me.User.ID, key, intent)))
		if e != nil {
			var conflict *transaction.Error
			if errors.As(e, &conflict) && conflict.Kind == transaction.UniqueConflict {
				return failure("DATASET_EXISTS")
			}
			return e
		}
		if e = auditEvent(ctx, tx, a, w, "dataset.created", "dataset", id, input.Reason, requestID); e != nil {
			return e
		}
		out, e = lockedDataset(ctx, tx, w, id)
		if e != nil {
			return e
		}
		return datasetReadable(ctx, tx, a, w, id)
	})
	return committed(out, e)
}
func (s *Service) Get(ctx context.Context, a identity.Auth, workspace, id string) (Dataset, error) {
	var out Dataset
	e := s.transaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		var e error
		out, e = lockedDataset(ctx, tx, w, id)
		if e != nil {
			return e
		}
		return datasetReadable(ctx, tx, a, w, id)
	})
	return committed(out, e)
}
