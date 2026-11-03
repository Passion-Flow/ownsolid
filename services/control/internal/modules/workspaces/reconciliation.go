package workspaces

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

const OperationCommitted = "committed"
const OperationUnconfirmed = "unconfirmed"

type OperationEvent struct {
	Action      string `json:"action"`
	WorkspaceID string `json:"workspace_id"`
	TargetType  string `json:"target_type"`
	TargetID    string `json:"target_id"`
}

type OperationReceipt struct {
	RequestID string          `json:"request_id"`
	State     string          `json:"state"`
	Event     *OperationEvent `json:"event"`
}

func operationTarget(action string) string {
	switch action {
	case "workspace.created", "workspace.updated", "workspace.archived", "workspace.restored":
		return "workspace"
	case "workspace.member_added", "workspace.member_updated", "workspace.member_removed":
		return "workspace_membership"
	case "workspace.note_created", "workspace.note_updated", "workspace.note_deleted":
		return "workspace_note"
	}
	return ""
}

// Operation confirms only the caller's atomic successful write audit. Absence
// remains unconfirmed: retention, rollback and an in-flight commit look identical.
// Membership may have been removed by the original operation; current session,
// source, audience and installation tenant authority still apply.
func (s *Service) Operation(ctx context.Context, a identity.Auth, request string) (OperationReceipt, error) {
	if !workspaceID(request) {
		return OperationReceipt{}, fail("VALIDATION_FAILED")
	}
	if !mutationActor(a) {
		return OperationReceipt{}, fail("AUTH_SESSION_EXPIRED")
	}
	out := OperationReceipt{RequestID: request, State: OperationUnconfirmed}
	e := s.readActor(ctx, a, func(ctx context.Context, q transaction.Executor) error {
		var tenant string
		e := q.QueryRow(ctx, workspaceSQL(q,
			`SELECT i.tenant_id::text FROM installation_state i JOIN tenants t ON t.id=i.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND t.status='active' FOR SHARE OF i,t`,
			`SELECT i.tenant_id FROM installation_state i JOIN tenants t ON t.id=i.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND t.status='active' FOR SHARE`, `SELECT I.TENANT_ID FROM INSTALLATION_STATE I JOIN TENANTS T ON T.ID=I.TENANT_ID WHERE I.ID=1 AND I.BOOTSTRAP_COMPLETE=1 AND T.STATUS='active' FOR UPDATE OF I.ID,T.ID`)).Scan(&tenant)
		if errors.Is(e, sql.ErrNoRows) {
			return fail("SERVICE_NOT_READY")
		}
		if e != nil {
			return e
		}
		if !workspaceID(tenant) {
			return fail("SERVICE_NOT_READY")
		}
		const actions = ` AND action IN ('workspace.created','workspace.updated','workspace.archived','workspace.restored','workspace.member_added','workspace.member_updated','workspace.member_removed','workspace.note_created','workspace.note_updated','workspace.note_deleted') LIMIT 2`
		rows, e := q.Query(ctx, workspaceSQL(q,
			`SELECT action,workspace_id::text,target_type,target_id FROM audit_events WHERE request_id=$1 AND actor_user_id=$2 AND audience=$3 AND tenant_id=$4 AND outcome='success'`+actions,
			`SELECT action,workspace_id,target_type,target_id FROM audit_events WHERE request_id=? AND actor_user_id=? AND audience=? AND tenant_id=? AND outcome='success'`+actions, `SELECT ACTION,WORKSPACE_ID,TARGET_TYPE,TARGET_ID FROM AUDIT_EVENTS WHERE REQUEST_ID=? AND ACTOR_USER_ID=? AND AUDIENCE=? AND TENANT_ID=? AND OUTCOME='success' AND ACTION IN ('workspace.created','workspace.updated','workspace.archived','workspace.restored','workspace.member_added','workspace.member_updated','workspace.member_removed','workspace.note_created','workspace.note_updated','workspace.note_deleted') LIMIT 2`,
			request, a.Me.User.ID, a.Me.Audience, tenant))
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var event OperationEvent
			if e = rows.Scan(&event.Action, &event.WorkspaceID, &event.TargetType, &event.TargetID); e != nil {
				return e
			}
			if out.Event != nil || operationTarget(event.Action) != event.TargetType || !workspaceID(event.WorkspaceID) || !workspaceID(event.TargetID) || event.TargetType == "workspace" && event.TargetID != event.WorkspaceID {
				return fail("STORAGE_UNAVAILABLE")
			}
			out.Event = &event
			out.State = OperationCommitted
		}
		return rows.Err()
	})
	if e != nil {
		return OperationReceipt{}, e
	}
	return out, nil
}
