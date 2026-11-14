package models

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
)

type Retirement struct {
	ID                string `json:"id"`
	WorkspaceID       string `json:"workspace_id"`
	State             string `json:"state"`
	ArtifactRetention string `json:"artifact_retention"`
}

// Retire removes an unreferenced version from inventory and machine admission, preserving immutable files.
func (s *Service) Retire(ctx context.Context, a identity.Auth, id, reason, request string) (Retirement, error) {
	var out Retirement
	if !admin(a) {
		return out, fail("ACCESS_DENIED")
	}
	if !validID(id) || !validID(request) || !reasonValid(reason) {
		return out, fail("VALIDATION_FAILED")
	}
	e := s.readActor(ctx, a, func(ctx context.Context, q transaction.Executor) error {
		var workspace string
		e := q.QueryRow(ctx, modelSQL(q,
			`SELECT m.workspace_id::text FROM model_versions m JOIN installation_state i ON i.tenant_id=m.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND m.id=$1 AND (m.site_id,m.cell_id)=(local_site_id(),local_cell_id())`,
			`SELECT m.workspace_id FROM model_versions m JOIN installation_state i ON i.tenant_id=m.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND m.id=?`, id)).Scan(&workspace)
		if errors.Is(e, sql.ErrNoRows) {
			return fail("RESOURCE_NOT_FOUND")
		}
		if e != nil {
			return e
		}
		w, e := s.Workspaces.RecheckScopeWithin(ctx, q, a, workspace)
		if e != nil {
			return e
		}
		if w.Status != "active" {
			return fail("ACCESS_DENIED")
		}
		var state string
		if e := q.QueryRow(ctx, modelSQL(q,
			"SELECT state FROM model_versions WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 AND (site_id,cell_id)=(local_site_id(),local_cell_id()) FOR UPDATE",
			"SELECT state FROM model_versions WHERE tenant_id=? AND workspace_id=? AND id=? FOR UPDATE", w.TenantID, w.ID, id)).Scan(&state); e != nil {
			if errors.Is(e, sql.ErrNoRows) {
				return fail("RESOURCE_NOT_FOUND")
			}
			return e
		}
		out = Retirement{ID: id, WorkspaceID: w.ID, State: "deleted", ArtifactRetention: "immutable_files_retained"}
		if state == "deleted" {
			return nil
		}
		if s.Referenced == nil {
			return fail("MODEL_REFERENCE_CHECK_UNAVAILABLE")
		}
		referenced, e := s.Referenced(ctx, q, w.TenantID, w.ID, id)
		if e != nil {
			return e
		}
		if referenced {
			return fail("MODEL_VERSION_REFERENCED")
		}
		n, e := q.Exec(ctx, modelSQL(q,
			"UPDATE model_versions SET state='deleted',deleted_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 AND state='ready' AND (site_id,cell_id)=(local_site_id(),local_cell_id())",
			"UPDATE model_versions SET state='deleted',deleted_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND id=? AND state='ready'", w.TenantID, w.ID, id))
		if e != nil {
			return e
		}
		if n != 1 {
			return fail("RESOURCE_NOT_FOUND")
		}
		_, e = q.Exec(ctx, modelSQL(q,
			`INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES($1,clock_timestamp(),$2,$3,$4,'admin','model.version_retired','success','model_version',$5,$6,$7)`,
			`INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,UTC_TIMESTAMP(6),?,?,?,'admin','model.version_retired','success','model_version',?,?,?)`,
			uuid.NewString(), a.Me.User.ID, w.TenantID, w.ID, id, reason, request))
		return e
	})
	if e != nil {
		return Retirement{}, e
	}
	return out, nil
}
