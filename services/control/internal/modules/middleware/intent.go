package middleware

import (
	"context"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// Intent lookup is read-only. Absence is never automatically interpreted as
// permission to resend a mutation whose original response was uncertain.
func (s *Service) RegistrationIntent(ctx context.Context, a identity.Auth, workspace, key string) (Instance, error) {
	if !validID(key) {
		return Instance{}, fail("VALIDATION_FAILED")
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
		out, e = scanInstance(q.QueryRow(ctx, statement(q, "SELECT "+instancePG+" FROM middleware_instances WHERE tenant_id=$1 AND workspace_id=$2 AND created_by=$3 AND creation_key=$4", "SELECT "+instanceMy+" FROM middleware_instances WHERE tenant_id=? AND workspace_id=? AND created_by=? AND creation_key=?", w.TenantID, w.ID, a.Me.User.ID, key)))
		return e
	})
	return committed(out, normalize(e))
}

func (s *Service) OperationIntent(ctx context.Context, a identity.Auth, workspace, key string) (Operation, error) {
	if !validID(key) {
		return Operation{}, fail("VALIDATION_FAILED")
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
		out, e = scanOperation(q.QueryRow(ctx, statement(q, "SELECT "+operationPG+" FROM middleware_operations WHERE tenant_id=$1 AND workspace_id=$2 AND requested_by=$3 AND intent_key=$4", "SELECT "+operationMy+" FROM middleware_operations WHERE tenant_id=? AND workspace_id=? AND requested_by=? AND intent_key=?", w.TenantID, w.ID, a.Me.User.ID, key)))
		return e
	})
	return committed(out, normalize(e))
}
