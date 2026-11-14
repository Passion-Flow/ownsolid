package distribution

import (
	"context"
	"net/url"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
	wire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
)

func (s *Service) TargetHistory(ctx context.Context, a identity.Auth, workspace, id string, values url.Values) (Page[AliasTarget], error) {
	out := Page[AliasTarget]{Items: []AliasTarget{}}
	if !admin(a) {
		return out, fail("ACCESS_DENIED")
	}
	if !wire.ValidID(id) {
		return out, fail("VALIDATION_FAILED")
	}
	if _, e := s.repository(); e != nil {
		return Page[AliasTarget]{}, distributionError(e)
	}
	limit, cursor, scope, e := pagination.ParseWithDefault(values, "alias-targets:"+a.Me.User.ID+":"+workspace+":"+id, s.Identity.Config.Policy().UI.PageSize)
	if e != nil {
		return out, e
	}
	e = s.transaction(ctx, a, workspace, false, nil, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		var exists bool
		if e := tx.QueryRow(ctx, distributionStatement(tx, "SELECT EXISTS(SELECT 1 FROM model_aliases WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3)", `SELECT EXISTS(SELECT 1 FROM model_aliases WHERE tenant_id=? AND workspace_id=? AND id=?)`, []any{w.TenantID, w.ID, id}, []any{w.TenantID, w.ID, id})).Scan(&exists); e != nil {
			return e
		}
		if !exists {
			return fail("MODEL_ALIAS_NOT_FOUND")
		}
		query := "SELECT " + targetFields + " FROM model_alias_target_versions WHERE tenant_id=$1 AND workspace_id=$2 AND alias_id=$3"
		mysqlQuery := "SELECT " + mysqlTargetFields + " FROM model_alias_target_versions WHERE tenant_id=? AND workspace_id=? AND alias_id=?"
		args := []any{w.TenantID, w.ID, id}
		if cursor.ID != "" {
			query += " AND (created_at,id)<($4,$5::uuid)"
			mysqlQuery += " AND (created_at,id)<(?,?)"
			args = append(args, cursor.Time, cursor.ID)
		}
		if len(args) == 3 {
			query += " ORDER BY created_at DESC,id DESC LIMIT $4"
		} else {
			query += " ORDER BY created_at DESC,id DESC LIMIT $6"
		}
		args = append(args, limit+1)
		mysqlQuery += " ORDER BY created_at DESC,id DESC LIMIT ?"
		rows, e := tx.Query(ctx, distributionSQL(tx, query, mysqlQuery, args...))
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			item, e := scanAliasTarget(rows)
			if e != nil {
				return e
			}
			out.Items = append(out.Items, item)
		}
		if e = rows.Err(); e != nil {
			return e
		}
		if len(out.Items) > limit {
			out.Items = out.Items[:limit]
			last := out.Items[limit-1]
			out.NextCursor = pagination.Encode(last.CreatedAt, last.ID, scope)
		}
		return nil
	})
	if e != nil {
		return Page[AliasTarget]{Items: []AliasTarget{}}, e
	}
	return out, nil
}
