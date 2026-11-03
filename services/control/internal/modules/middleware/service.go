package middleware

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func (s *Service) Choices(ctx context.Context, a identity.Auth, workspace string) ([]Choice, error) {
	if e := s.admit(ctx, a, workspace, false); e != nil {
		return nil, e
	}
	store, e := s.repository()
	if e != nil {
		return nil, e
	}
	ctx, cancel := s.bounded(ctx)
	defer cancel()
	out := []Choice{}
	e = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		w, e := s.authority(ctx, q, a, workspace, false)
		if e != nil {
			return e
		}
		for _, p := range s.configuration.Profiles {
			if p.Kind == PlatformOwned && p.ExpectedTenantID != w.TenantID {
				continue
			}
			targets := []string{}
			for _, t := range p.RestoreTargets {
				targets = append(targets, t.Key)
			}
			out = append(out, Choice{Key: p.Key, Kind: p.Kind, Health: true, Backup: p.Kind == PlatformOwned, RestoreTargets: targets})
		}
		return nil
	})
	return committed(out, normalize(e))
}

func (s *Service) Register(ctx context.Context, a identity.Auth, workspace, key, name, intent, request string) (Instance, error) {
	if !validID(intent) || !validID(request) {
		return Instance{}, fail("VALIDATION_FAILED")
	}
	name, e := security.Name(name)
	if e != nil {
		return Instance{}, fail("VALIDATION_FAILED")
	}
	if e = s.admit(ctx, a, workspace, true); e != nil {
		return Instance{}, e
	}
	p, e := s.profile(key)
	if e != nil {
		return Instance{}, e
	}
	captured, e := Capture(p)
	if e != nil {
		return Instance{}, e
	}
	store, e := s.repository()
	if e != nil {
		return Instance{}, e
	}
	ctx, cancel := s.bounded(ctx)
	defer cancel()
	var out Instance
	e = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		w, e := s.authority(ctx, q, a, workspace, true)
		if e != nil {
			return e
		}
		if p.Kind == PlatformOwned && p.ExpectedTenantID != w.TenantID {
			return fail("MIDDLEWARE_PLATFORM_SCOPE_MISMATCH")
		}
		fingerprint := hash([]byte(key + "\x00" + name + "\x00" + captured.fingerprint))
		var id, old string
		e = q.QueryRow(ctx, statement(q, `SELECT id::text,creation_fingerprint FROM middleware_instances WHERE tenant_id=$1 AND workspace_id=$2 AND created_by=$3 AND creation_key=$4`, `SELECT id,creation_fingerprint FROM middleware_instances WHERE tenant_id=? AND workspace_id=? AND created_by=? AND creation_key=?`, w.TenantID, w.ID, a.Me.User.ID, intent)).Scan(&id, &old)
		if e == nil {
			if old != fingerprint {
				return fail("IDEMPOTENCY_CONFLICT")
			}
			out, e = instanceWithin(ctx, q, w, id, false)
			return e
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		// The current authority holds the existing exclusive Workspace fence.
		// Profile uniqueness and capacity admission remain serialized with replay.
		var count, registered int
		if e = q.QueryRow(ctx, statement(q, "SELECT count(*) FROM middleware_instances WHERE tenant_id=$1 AND workspace_id=$2 AND profile_key=$3", "SELECT count(*) FROM middleware_instances WHERE tenant_id=? AND workspace_id=? AND profile_key=?", w.TenantID, w.ID, key)).Scan(&registered); e != nil {
			return e
		}
		if registered != 0 {
			return fail("MIDDLEWARE_PROFILE_ALREADY_REGISTERED")
		}
		if e = q.QueryRow(ctx, statement(q, "SELECT count(*) FROM middleware_instances WHERE tenant_id=$1 AND workspace_id=$2", "SELECT count(*) FROM middleware_instances WHERE tenant_id=? AND workspace_id=?", w.TenantID, w.ID)).Scan(&count); e != nil {
			return e
		}
		if count >= s.configuration.Policy.MaximumInstances {
			return fail("MIDDLEWARE_INSTANCE_LIMIT")
		}
		if !captured.Matches() {
			return fail("MIDDLEWARE_INPUT_CHANGED")
		}
		id = uuid.NewString()
		_, e = q.Exec(ctx, statement(q, `INSERT INTO middleware_instances(id,tenant_id,workspace_id,profile_key,kind,name,profile_fingerprint,created_by,creation_key,creation_fingerprint) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, `INSERT INTO middleware_instances(id,tenant_id,workspace_id,profile_key,kind,name,profile_fingerprint,created_by,creation_key,creation_fingerprint) VALUES(?,?,?,?,?,?,?,?,?,?)`, id, w.TenantID, w.ID, key, p.Kind, name, captured.fingerprint, a.Me.User.ID, intent, fingerprint))
		if e != nil {
			return e
		}
		if e = audit(ctx, q, a, w, "middleware.registered", "middleware_instance", id, "", request); e != nil {
			return e
		}
		out, e = instanceWithin(ctx, q, w, id, false)
		return e
	})
	return committed(out, normalize(e))
}

func (s *Service) Get(ctx context.Context, a identity.Auth, workspace, id string) (Instance, error) {
	if !validID(id) {
		return Instance{}, fail("RESOURCE_NOT_FOUND")
	}
	if e := s.admit(ctx, a, workspace, false); e != nil {
		return Instance{}, e
	}
	store, e := s.repository()
	if e != nil {
		return Instance{}, e
	}
	ctx, cancel := s.bounded(ctx)
	defer cancel()
	var out Instance
	e = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		w, e := s.authority(ctx, q, a, workspace, false)
		if e != nil {
			return e
		}
		out, e = instanceWithin(ctx, q, w, id, false)
		return e
	})
	return committed(out, normalize(e))
}

func (s *Service) List(ctx context.Context, a identity.Auth, workspace, cursor string) (Page[Instance], error) {
	if cursor != "" && !validID(cursor) {
		return Page[Instance]{}, fail("VALIDATION_FAILED")
	}
	if e := s.admit(ctx, a, workspace, false); e != nil {
		return Page[Instance]{}, e
	}
	store, e := s.repository()
	if e != nil {
		return Page[Instance]{}, e
	}
	ctx, cancel := s.bounded(ctx)
	defer cancel()
	out := Page[Instance]{Items: []Instance{}}
	e = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		w, e := s.authority(ctx, q, a, workspace, false)
		if e != nil {
			return e
		}
		rows, e := q.Query(ctx, statement(q, "SELECT "+instancePG+" FROM middleware_instances WHERE tenant_id=$1 AND workspace_id=$2 AND id::text>$3 ORDER BY id LIMIT $4", "SELECT "+instanceMy+" FROM middleware_instances WHERE tenant_id=? AND workspace_id=? AND id>? ORDER BY id LIMIT ?", w.TenantID, w.ID, cursor, s.configuration.Policy.PageSize+1))
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			v, e := scanInstance(rows)
			if e != nil {
				return e
			}
			out.Items = append(out.Items, v)
		}
		return rows.Err()
	})
	if len(out.Items) > s.configuration.Policy.PageSize {
		out.Items = out.Items[:s.configuration.Policy.PageSize]
		next := out.Items[len(out.Items)-1].ID
		out.NextCursor = &next
	}
	return committed(out, normalize(e))
}

// Update explicitly acknowledges new measured profile bytes or archives a
// registration. Historical operations retain their original version/fingerprint.
func (s *Service) Update(ctx context.Context, a identity.Auth, workspace, id, name, state string, version int64, refresh bool, reason, request string) (Instance, error) {
	if !validID(id) || !validID(request) || !workspaces.ValidReason(reason) || !workspaces.ValidVersion(version) || state != "active" && state != "archived" {
		return Instance{}, fail("VALIDATION_FAILED")
	}
	name, e := security.Name(name)
	if e != nil {
		return Instance{}, fail("VALIDATION_FAILED")
	}
	if e = s.admit(ctx, a, workspace, true); e != nil {
		return Instance{}, e
	}
	store, e := s.repository()
	if e != nil {
		return Instance{}, e
	}
	ctx, cancel := s.bounded(ctx)
	defer cancel()
	var out Instance
	e = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		w, e := s.authority(ctx, q, a, workspace, true)
		if e != nil {
			return e
		}
		old, e := instanceWithin(ctx, q, w, id, true)
		if e != nil {
			return e
		}
		if old.Version != version {
			return fail("VERSION_CONFLICT")
		}
		if version == identity.MaximumAuthVersion {
			return fail("MIDDLEWARE_VERSION_LIMIT")
		}
		fingerprint := old.Fingerprint
		if refresh {
			p, e := s.profile(old.ProfileKey)
			if e != nil {
				return e
			}
			if p.Kind != old.Kind || p.Kind == PlatformOwned && p.ExpectedTenantID != w.TenantID {
				return fail("MIDDLEWARE_PLATFORM_SCOPE_MISMATCH")
			}
			input, e := Capture(p)
			if e != nil {
				return e
			}
			fingerprint = input.fingerprint
		}
		_, e = q.Exec(ctx, statement(q, `UPDATE middleware_instances SET name=$1,state=$2,profile_fingerprint=$3,version=version+1,updated_at=clock_timestamp() WHERE tenant_id=$4 AND workspace_id=$5 AND id=$6`, `UPDATE middleware_instances SET name=?,state=?,profile_fingerprint=?,version=version+1,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND id=?`, name, state, fingerprint, w.TenantID, w.ID, id))
		if e != nil {
			return e
		}
		if e = audit(ctx, q, a, w, "middleware.registration_updated", "middleware_instance", id, reason, request); e != nil {
			return e
		}
		out, e = instanceWithin(ctx, q, w, id, false)
		return e
	})
	return committed(out, normalize(e))
}

func (s *Service) Enqueue(ctx context.Context, a identity.Auth, workspace, id string, input OperationInput, request string) (Operation, error) {
	if !validID(id) || !validID(input.IdempotencyKey) || !validID(request) || !workspaces.ValidReason(input.Reason) || !workspaces.ValidVersion(input.Version) {
		return Operation{}, fail("VALIDATION_FAILED")
	}
	if _, e := boundReason(input.IdempotencyKey, input.Reason); e != nil {
		return Operation{}, e
	}
	if input.Kind != "health" && input.Kind != "backup" && input.Kind != "isolated_restore" && input.Kind != "reconcile" {
		return Operation{}, fail("VALIDATION_FAILED")
	}
	if input.Kind == "isolated_restore" {
		if !input.ConfirmIsolated || !validID(input.BackupID) || !profileKey.MatchString(input.TargetKey) || input.OriginalOperationID != "" {
			return Operation{}, fail("MIDDLEWARE_ISOLATED_CONFIRMATION_REQUIRED")
		}
	} else if input.ConfirmIsolated || input.BackupID != "" || input.TargetKey != "" {
		return Operation{}, fail("VALIDATION_FAILED")
	}
	if (input.Kind == "reconcile") != validID(input.OriginalOperationID) {
		return Operation{}, fail("VALIDATION_FAILED")
	}
	if input.Kind != "reconcile" && input.OriginalOperationID != "" {
		return Operation{}, fail("VALIDATION_FAILED")
	}
	if e := s.admit(ctx, a, workspace, true); e != nil {
		return Operation{}, e
	}
	store, e := s.repository()
	if e != nil {
		return Operation{}, e
	}
	ctx, cancel := s.bounded(ctx)
	defer cancel()
	var out Operation
	e = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		w, e := s.authority(ctx, q, a, workspace, true)
		if e != nil {
			return e
		}
		v, e := instanceWithin(ctx, q, w, id, true)
		if e != nil {
			return e
		}
		if v.State != "active" || v.Version != input.Version {
			return fail("VERSION_CONFLICT")
		}
		p, e := s.profile(v.ProfileKey)
		if e != nil {
			return e
		}
		captured, e := Capture(p)
		if e != nil {
			return e
		}
		if v.Fingerprint != captured.fingerprint || p.Kind != v.Kind || p.Kind == PlatformOwned && p.ExpectedTenantID != w.TenantID {
			return fail("MIDDLEWARE_INPUT_CHANGED")
		}
		if input.Kind != "health" && p.Kind != PlatformOwned {
			return fail("MIDDLEWARE_EXTERNAL_MUTATION_REFUSED")
		}
		if input.Kind == "isolated_restore" {
			targetFound := false
			for _, t := range p.RestoreTargets {
				if t.Key == input.TargetKey {
					targetFound = true
				}
			}
			if !targetFound {
				return fail("MIDDLEWARE_ISOLATED_TARGET_INVALID")
			}
			b, e := operationWithin(ctx, q, w, input.BackupID, false)
			if e != nil {
				return e
			}
			if b.InstanceID != id {
				return fail("MIDDLEWARE_COMPLETED_BACKUP_REQUIRED")
			}
			if _, _, e = backupReceipt(b); e != nil {
				return e
			}
		}
		if input.Kind == "reconcile" {
			prior, e := operationWithin(ctx, q, w, input.OriginalOperationID, true)
			if e != nil {
				return e
			}
			if prior.InstanceID != id || prior.Kind == "health" || prior.Kind == "reconcile" || prior.State != "unknown" && prior.State != "failed" {
				return fail("MIDDLEWARE_ORIGINAL_UNCERTAIN_OPERATION_REQUIRED")
			}
		}
		raw, _ := json.Marshal(input)
		fingerprint := hash([]byte(id + "\x00" + string(raw) + "\x00" + captured.fingerprint))
		var previous, old string
		e = q.QueryRow(ctx, statement(q, `SELECT id::text,intent_fingerprint FROM middleware_operations WHERE tenant_id=$1 AND workspace_id=$2 AND requested_by=$3 AND intent_key=$4`, `SELECT id,intent_fingerprint FROM middleware_operations WHERE tenant_id=? AND workspace_id=? AND requested_by=? AND intent_key=?`, w.TenantID, w.ID, a.Me.User.ID, input.IdempotencyKey)).Scan(&previous, &old)
		if e == nil {
			if old != fingerprint {
				return fail("IDEMPOTENCY_CONFLICT")
			}
			out, e = operationWithin(ctx, q, w, previous, false)
			return e
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		authority, _ := json.Marshal(snapshot(a))
		operation := uuid.NewString()
		_, e = q.Exec(ctx, statement(q, `INSERT INTO middleware_operations(id,tenant_id,workspace_id,instance_id,kind,requested_by,intent_key,intent_fingerprint,input_json,actor_json,instance_version,profile_fingerprint,request_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, `INSERT INTO middleware_operations(id,tenant_id,workspace_id,instance_id,kind,requested_by,intent_key,intent_fingerprint,input_json,actor_json,instance_version,profile_fingerprint,request_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, operation, w.TenantID, w.ID, id, input.Kind, a.Me.User.ID, input.IdempotencyKey, fingerprint, string(raw), string(authority), v.Version, captured.fingerprint, request))
		if e != nil {
			return e
		}
		if e = audit(ctx, q, a, w, "middleware.operation_queued", "middleware_operation", operation, input.Reason, request); e != nil {
			return e
		}
		out, e = operationWithin(ctx, q, w, operation, false)
		return e
	})
	return committed(out, normalize(e))
}

const operationPG = `id::text,tenant_id::text,workspace_id::text,instance_id::text,kind,state,COALESCE(error_code,''),CASE WHEN state='succeeded' THEN result_json ELSE NULL END,created_at,updated_at`
const operationMy = `id,tenant_id,workspace_id,instance_id,kind,state,COALESCE(error_code,''),CASE WHEN state='succeeded' THEN result_json ELSE NULL END,created_at,updated_at`

func scanOperation(row transaction.Row) (Operation, error) {
	var v Operation
	var result *string
	e := row.Scan(&v.ID, &v.TenantID, &v.WorkspaceID, &v.InstanceID, &v.Kind, &v.State, &v.ErrorCode, &result, &v.CreatedAt, &v.UpdatedAt)
	if result != nil {
		v.Result = json.RawMessage(*result)
	}
	if errors.Is(e, sql.ErrNoRows) {
		return Operation{}, fail("RESOURCE_NOT_FOUND")
	}
	return v, e
}

func operationWithin(ctx context.Context, q transaction.Executor, w workspaces.Workspace, id string, lock bool) (Operation, error) {
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	return scanOperation(q.QueryRow(ctx, statement(q, "SELECT "+operationPG+" FROM middleware_operations WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3"+suffix, "SELECT "+operationMy+" FROM middleware_operations WHERE tenant_id=? AND workspace_id=? AND id=?"+suffix, w.TenantID, w.ID, id)))
}

func (s *Service) Operation(ctx context.Context, a identity.Auth, workspace, id string) (Operation, error) {
	if !validID(id) {
		return Operation{}, fail("RESOURCE_NOT_FOUND")
	}
	if e := s.admit(ctx, a, workspace, false); e != nil {
		return Operation{}, e
	}
	store, e := s.repository()
	if e != nil {
		return Operation{}, e
	}
	ctx, cancel := s.bounded(ctx)
	defer cancel()
	var out Operation
	e = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		w, e := s.authority(ctx, q, a, workspace, false)
		if e != nil {
			return e
		}
		out, e = operationWithin(ctx, q, w, id, false)
		return e
	})
	return committed(out, normalize(e))
}

func (s *Service) History(ctx context.Context, a identity.Auth, workspace, id, cursor string) (Page[Operation], error) {
	if !validID(id) || cursor != "" && !validID(cursor) {
		return Page[Operation]{}, fail("VALIDATION_FAILED")
	}
	if e := s.admit(ctx, a, workspace, false); e != nil {
		return Page[Operation]{}, e
	}
	store, e := s.repository()
	if e != nil {
		return Page[Operation]{}, e
	}
	ctx, cancel := s.bounded(ctx)
	defer cancel()
	out := Page[Operation]{Items: []Operation{}}
	e = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		w, e := s.authority(ctx, q, a, workspace, false)
		if e != nil {
			return e
		}
		if _, e = instanceWithin(ctx, q, w, id, false); e != nil {
			return e
		}
		rows, e := q.Query(ctx, statement(q, "SELECT "+operationPG+" FROM middleware_operations WHERE tenant_id=$1 AND workspace_id=$2 AND instance_id=$3 AND id::text>$4 ORDER BY id LIMIT $5", "SELECT "+operationMy+" FROM middleware_operations WHERE tenant_id=? AND workspace_id=? AND instance_id=? AND id>? ORDER BY id LIMIT ?", w.TenantID, w.ID, id, cursor, s.configuration.Policy.PageSize+1))
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			v, e := scanOperation(rows)
			if e != nil {
				return e
			}
			out.Items = append(out.Items, v)
		}
		return rows.Err()
	})
	if len(out.Items) > s.configuration.Policy.PageSize {
		out.Items = out.Items[:s.configuration.Policy.PageSize]
		next := out.Items[len(out.Items)-1].ID
		out.NextCursor = &next
	}
	return committed(out, normalize(e))
}
