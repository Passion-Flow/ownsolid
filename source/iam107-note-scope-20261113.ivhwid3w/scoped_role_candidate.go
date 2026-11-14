package workspaces

import (
	"context"
	"database/sql"
	"errors"
	"sort"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// This entire file exists only in the external source overlay. These fixed
// experimental bounds are not a production policy/configuration admission.
const candidateMaximumGrants = 32
const candidateNoteUpdate = "workspace.note.update"
const candidateNoteDelete = "workspace.note.delete"

type candidateEvidence struct {
	GrantID, RoleID           string
	GrantVersion, RoleVersion int64
}
type candidateDecision struct {
	Allowed           bool
	PlatformAuthority bool
	Grants            []candidateEvidence
}
type candidateGrant struct {
	ID, Tenant, Workspace, User, Authority, Role, Note string
	Version                                            int64
	Active                                             bool
}

func candidateAction(action string) bool {
	return action == candidateNoteUpdate || action == candidateNoteDelete
}

func (s *Service) candidateDecisionWithin(ctx context.Context, q transaction.Executor, a identity.Auth, w Workspace, n *Note, action string) (candidateDecision, error) {
	if !candidateAction(action) {
		return candidateDecision{}, fail("ACCESS_DENIED")
	}
	if e := s.executor(q); e != nil {
		return candidateDecision{}, e
	}
	if !workspaceID(w.TenantID) || !workspaceID(w.ID) || n != nil && (n.TenantID != w.TenantID || n.WorkspaceID != w.ID || !workspaceID(n.ID)) {
		return candidateDecision{}, fail("RESOURCE_NOT_FOUND")
	}
	if admin(a) {
		return candidateDecision{Allowed: true, PlatformAuthority: true}, nil
	}
	var total int
	if e := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT count(*) FROM (SELECT g.id FROM iam_role_proto_grants g JOIN workspace_memberships m ON m.authority_id=g.authority_id AND m.tenant_id=g.tenant_id AND m.workspace_id=g.workspace_id AND m.user_id=g.user_id WHERE g.tenant_id=$1 AND g.workspace_id=$2 AND g.user_id=$3 AND g.active ORDER BY g.id LIMIT $4) bounded`, w.TenantID, w.ID, a.Me.User.ID, candidateMaximumGrants+1)).Scan(&total); e != nil {
		return candidateDecision{}, e
	}
	if total > candidateMaximumGrants {
		return candidateDecision{}, fail("STORAGE_UNAVAILABLE")
	}
	note := ""
	if n != nil {
		note = n.ID
	}
	rows, e := q.Query(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT g.id::text,g.version,r.id::text,r.version,ra.action FROM iam_role_proto_grants g JOIN workspace_memberships m ON m.authority_id=g.authority_id AND m.tenant_id=g.tenant_id AND m.workspace_id=g.workspace_id AND m.user_id=g.user_id JOIN iam_role_proto_roles r ON r.id=g.role_id AND r.tenant_id=g.tenant_id AND r.workspace_id=g.workspace_id JOIN iam_role_proto_actions ra ON ra.role_id=r.id AND ra.tenant_id=r.tenant_id AND ra.workspace_id=r.workspace_id WHERE g.tenant_id=$1 AND g.workspace_id=$2 AND g.user_id=$3 AND g.active AND r.active AND (g.note_id IS NULL OR g.note_id=NULLIF($4,'')::uuid) ORDER BY g.id,r.id,ra.action LIMIT $5`, w.TenantID, w.ID, a.Me.User.ID, note, candidateMaximumGrants*2+1))
	if e != nil {
		return candidateDecision{}, e
	}
	defer rows.Close()
	out := candidateDecision{}
	seen := map[string]candidateEvidence{}
	count := 0
	for rows.Next() {
		var proof candidateEvidence
		var held string
		if e = rows.Scan(&proof.GrantID, &proof.GrantVersion, &proof.RoleID, &proof.RoleVersion, &held); e != nil {
			return candidateDecision{}, e
		}
		count++
		if count > candidateMaximumGrants*2 || !workspaceID(proof.GrantID) || !workspaceID(proof.RoleID) || !ValidVersion(proof.GrantVersion) || !ValidVersion(proof.RoleVersion) || !candidateAction(held) {
			return candidateDecision{}, fail("STORAGE_UNAVAILABLE")
		}
		if held == action {
			seen[proof.GrantID] = proof
			out.Allowed = true
		}
	}
	if e = rows.Err(); e != nil {
		return candidateDecision{}, e
	}
	if len(seen) > candidateMaximumGrants {
		return candidateDecision{}, fail("STORAGE_UNAVAILABLE")
	}
	for _, proof := range seen {
		out.Grants = append(out.Grants, proof)
	}
	sort.Slice(out.Grants, func(i, j int) bool { return out.Grants[i].GrantID < out.Grants[j].GrantID })
	return out, nil
}
func (s *Service) candidateRequireNoteWithin(ctx context.Context, q transaction.Executor, a identity.Auth, w Workspace, n Note, remove bool) error {
	action := candidateNoteUpdate
	if remove {
		action = candidateNoteDelete
	}
	decision, e := s.candidateDecisionWithin(ctx, q, a, w, &n, action)
	if e != nil {
		return e
	}
	if !decision.Allowed {
		return fail("ACCESS_DENIED")
	}
	return candidatePauseAt(ctx, q, candidateNotePause)
}
func (s *Service) candidateExplain(ctx context.Context, a identity.Auth, workspace, note, action, request string) (candidateDecision, error) {
	if !workspaceID(workspace) || !workspaceID(note) || !candidateAction(action) {
		return candidateDecision{}, fail("VALIDATION_FAILED")
	}
	var out candidateDecision
	e := s.mutation(ctx, a, nil, false, request, func(ctx context.Context, q transaction.Executor, _ string) error {
		w, e := s.resolveWithin(ctx, q, a, workspace, scopeShared)
		if e != nil {
			return e
		}
		n, e := selectNoteWithin(ctx, q, w, note, false)
		if e != nil {
			return e
		}
		out, e = s.candidateDecisionWithin(ctx, q, a, w, &n, action)
		return e
	})
	if e != nil {
		return candidateDecision{}, e
	}
	return out, nil
}
func (s *Service) candidateSetGrantActive(ctx context.Context, a identity.Auth, workspace, grant string, version int64, active bool, request string) (candidateGrant, error) {
	if !workspaceID(workspace) || !workspaceID(grant) || !ValidVersion(version) {
		return candidateGrant{}, fail("VALIDATION_FAILED")
	}
	var out candidateGrant
	e := s.mutation(ctx, a, nil, false, request, func(ctx context.Context, q transaction.Executor, _ string) error {
		w, e := s.writableWithin(ctx, q, a, workspace, true)
		if e != nil {
			return e
		}
		var g candidateGrant
		e = q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT id::text,tenant_id::text,workspace_id::text,user_id::text,authority_id::text,role_id::text,COALESCE(note_id::text,''),version,active FROM iam_role_proto_grants WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 FOR UPDATE`, w.TenantID, w.ID, grant)).Scan(&g.ID, &g.Tenant, &g.Workspace, &g.User, &g.Authority, &g.Role, &g.Note, &g.Version, &g.Active)
		if errors.Is(e, sql.ErrNoRows) {
			return fail("RESOURCE_NOT_FOUND")
		}
		if e != nil {
			return e
		}
		if g.Version != version {
			return fail("VERSION_CONFLICT")
		}
		if e = incrementable(g.Version); e != nil {
			return e
		}
		if active && !admin(a) {
			var n *Note
			if g.Note != "" {
				selected, e := selectNoteWithin(ctx, q, w, g.Note, false)
				if e != nil {
					return e
				}
				n = &selected
			}
			rows, e := q.Query(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT action FROM iam_role_proto_actions WHERE tenant_id=$1 AND workspace_id=$2 AND role_id=$3 ORDER BY action`, w.TenantID, w.ID, g.Role))
			if e != nil {
				return e
			}
			var actions []string
			for rows.Next() {
				var action string
				if e = rows.Scan(&action); e != nil {
					rows.Close()
					return e
				}
				actions = append(actions, action)
				if len(actions) > 2 {
					rows.Close()
					return fail("STORAGE_UNAVAILABLE")
				}
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return e
			}
			for _, action := range actions {
				decision, e := s.candidateDecisionWithin(ctx, q, a, w, n, action)
				if e != nil {
					return e
				}
				if !decision.Allowed {
					return fail("ACCESS_DENIED")
				}
			}
		}
		changed, e := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `UPDATE iam_role_proto_grants SET active=$1,version=version+1 WHERE tenant_id=$2 AND workspace_id=$3 AND id=$4`, active, w.TenantID, w.ID, g.ID))
		if e != nil {
			return e
		}
		if changed != 1 {
			return fail("WORKSPACE_WRITE_UNCONFIRMED")
		}
		if e = bumpWorkspace(ctx, q, w); e != nil {
			return e
		}
		reason := "grant_revoked"
		if active {
			reason = "grant_activated"
		}
		if e = s.scopedAuditWithin(ctx, q, a, w, "workspace.role_grant_changed", "workspace_role_grant", g.ID, g.User, reason, request); e != nil {
			return e
		}
		if e = candidatePauseAt(ctx, q, candidateGrantPause); e != nil {
			return e
		}
		g.Active = active
		g.Version++
		out = g
		return nil
	})
	if e != nil {
		return candidateGrant{}, e
	}
	return out, nil
}

// These pause points belong only to this external experiment and never enter
// product source. They let an independent server observer identify real locks.
type candidatePauseKey uint8

const (
	candidateNotePause candidatePauseKey = iota + 1
	candidateGrantPause
)

type candidatePauseFunc func(context.Context, transaction.Executor) error

func candidatePauseAt(ctx context.Context, q transaction.Executor, key candidatePauseKey) error {
	if pause, ok := ctx.Value(key).(candidatePauseFunc); ok {
		return pause(ctx, q)
	}
	return nil
}
