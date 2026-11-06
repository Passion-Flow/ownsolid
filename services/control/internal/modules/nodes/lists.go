package nodes

import (
	"context"
	"net/url"
	"strconv"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
)

func nodeListScope(kind, tenant, workspace, node string, a identity.Auth) string {
	return kind + ":" + tenant + ":" + workspace + ":" + node + ":" + a.Me.Audience + ":" + a.Me.User.ID
}
func nodeSeek(q transaction.Executor, pg, my, dm string, pgArgs, myArgs []any, stampColumn, idColumn string, cursor pagination.Cursor, limit int) transaction.Statement {
	if cursor.ID != "" {
		index := len(pgArgs) + 1
		pg += " AND (" + stampColumn + "," + idColumn + ")<($" + strconv.Itoa(index) + ",$" + strconv.Itoa(index+1) + "::uuid)"
		pgArgs = append(pgArgs, cursor.Time, cursor.ID)
		my += " AND (" + stampColumn + "<? OR (" + stampColumn + "=? AND " + idColumn + "<?))"
		dm += " AND (" + stampColumn + "<? OR (" + stampColumn + "=? AND " + idColumn + "<?))"
		myArgs = append(myArgs, cursor.Time, cursor.Time, cursor.ID)
	}
	order := " ORDER BY " + stampColumn + " DESC," + idColumn + " DESC LIMIT "
	if q.Dialect() == transaction.Dameng {
		// The public paginator validates this bounded integer; no business value is interpolated.
		if limit < 1 || limit > pagination.MaximumLimit || cursor.ID != "" && !nodeNativeTime(cursor.Time) {
			return transaction.SQL(transaction.Dameng, "")
		}
		return transaction.SQL(transaction.Dameng, dm+order+strconv.Itoa(limit+1), myArgs...)
	}
	if q.Dialect() == transaction.MySQL {
		return transaction.SQL(transaction.MySQL, my+order+"?", append(myArgs, limit+1)...)
	}
	if q.Dialect() == transaction.PostgreSQL {
		return transaction.SQL(transaction.PostgreSQL, pg+order+"$"+strconv.Itoa(len(pgArgs)+1), append(pgArgs, limit+1)...)
	}
	return transaction.SQL(q.Dialect(), "")
}
func nodeListRows[T any](ctx context.Context, q transaction.Executor, statement transaction.Statement, limit int, fingerprint string, scan func(transaction.Row) (T, error), position func(T) (time.Time, string)) (Page[T], error) {
	rows, e := q.Query(ctx, statement)
	if e != nil {
		return Page[T]{}, e
	}
	defer rows.Close()
	out := Page[T]{Items: []T{}}
	for rows.Next() {
		value, e := scan(rows)
		if e != nil {
			return Page[T]{}, e
		}
		out.Items = append(out.Items, value)
	}
	if e = rows.Err(); e != nil {
		return Page[T]{}, e
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		stamp, id := position(out.Items[limit-1])
		out.NextCursor = pagination.Encode(stamp, id, fingerprint)
	}
	return out, nil
}
func (s *Service) List(ctx context.Context, a identity.Auth, workspace string, values url.Values) (Page[Node], error) {
	var out Page[Node]
	e := s.readActor(ctx, a, func(ctx context.Context, q transaction.Executor) error {
		if workspace == "" && !isAdmin(a) {
			return fail("ACCESS_DENIED")
		}
		if workspace != "" {
			if _, e := s.readScope(ctx, q, a, workspace); e != nil {
				return e
			}
		}
		tenant, e := nodeTenant(ctx, q)
		if e != nil {
			return e
		}
		limit, cursor, fingerprint, e := pagination.ParseWithDefault(values, nodeListScope("nodes", tenant, workspace, "", a), s.Identity.Config.Policy().UI.PageSize)
		if e == nil && q.Dialect() == transaction.Dameng && cursor.ID != "" && !nodeNativeTime(cursor.Time) {
			return fail("INVALID_CURSOR")
		}
		if e != nil {
			return e
		}
		p := s.Identity.Config.Policy().Nodes
		pg, my := "SELECT "+pairedNodeFields+" FROM managed_nodes n WHERE n.tenant_id=$3", "SELECT "+mysqlNodeFields+" FROM managed_nodes n WHERE n.tenant_id=?"
		dm := "SELECT " + damengNodeFields + " FROM managed_nodes n WHERE n.tenant_id=?"
		pgArgs, myArgs := []any{p.OnlineSeconds, p.OfflineSeconds, tenant}, []any{-p.OnlineSeconds, -p.OfflineSeconds, tenant}
		if workspace != "" {
			pg += " AND n.workspace_id=$4"
			my += " AND n.workspace_id=?"
			dm += " AND n.workspace_id=?"
			pgArgs, myArgs = append(pgArgs, workspace), append(myArgs, workspace)
		}
		out, e = nodeListRows(ctx, q, nodeSeek(q, pg, my, dm, pgArgs, myArgs, "n.created_at", "n.id", cursor, limit), limit, fingerprint, func(row transaction.Row) (Node, error) { return scanNode(row) }, func(n Node) (time.Time, string) { return n.CreatedAt, n.ID })
		return e
	})
	if e != nil {
		return Page[Node]{}, e
	}
	return out, nil
}
func (s *Service) Tokens(ctx context.Context, a identity.Auth, values url.Values) (Page[JoinToken], error) {
	var out Page[JoinToken]
	e := s.readActor(ctx, a, func(ctx context.Context, q transaction.Executor) error {
		if !isAdmin(a) {
			return fail("ACCESS_DENIED")
		}
		tenant, e := nodeTenant(ctx, q)
		if e != nil {
			return e
		}
		limit, cursor, fingerprint, e := pagination.ParseWithDefault(values, nodeListScope("node-tokens", tenant, "", "", a), s.Identity.Config.Policy().UI.PageSize)
		if e == nil && q.Dialect() == transaction.Dameng && cursor.ID != "" && !nodeNativeTime(cursor.Time) {
			return fail("INVALID_CURSOR")
		}
		if e != nil {
			return e
		}
		pg, my := "SELECT "+joinFields+" FROM node_join_tokens j WHERE j.tenant_id=$1", "SELECT "+mysqlJoinFields+" FROM node_join_tokens j WHERE j.tenant_id=?"
		dm := "SELECT " + damengJoinFields + " FROM node_join_tokens j WHERE j.tenant_id=?"
		out, e = nodeListRows(ctx, q, nodeSeek(q, pg, my, dm, []any{tenant}, []any{tenant}, "j.created_at", "j.id", cursor, limit), limit, fingerprint, func(row transaction.Row) (JoinToken, error) { return scanJoin(row) }, func(v JoinToken) (time.Time, string) { return v.CreatedAt, v.ID })
		return e
	})
	if e != nil {
		return Page[JoinToken]{}, e
	}
	return out, nil
}
func (s *Service) Tasks(ctx context.Context, a identity.Auth, workspace, id string, values url.Values) (Page[Task], error) {
	var out Page[Task]
	e := s.readActor(ctx, a, func(ctx context.Context, q transaction.Executor) error {
		n, e := s.getWithin(ctx, q, a, workspace, id)
		if e != nil {
			return e
		}
		limit, cursor, fingerprint, e := pagination.ParseWithDefault(values, nodeListScope("node-tasks", n.TenantID, n.WorkspaceID, id, a), s.Identity.Config.Policy().UI.PageSize)
		if e == nil && q.Dialect() == transaction.Dameng && cursor.ID != "" && !nodeNativeTime(cursor.Time) {
			return fail("INVALID_CURSOR")
		}
		if e != nil {
			return e
		}
		// Finished tasks are all in the history, which outlives their rows in node_tasks (ADR 0029).
		pg, my := "SELECT "+taskFields+" FROM ("+
			"SELECT "+taskHistoryColumns+" FROM node_tasks WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND completed_at IS NULL"+
			" UNION ALL SELECT "+taskHistoryColumns+" FROM node_task_history WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3"+
			") tasks WHERE true", "SELECT "+mysqlTaskFields+" FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=?"
		dm := "SELECT " + damengTaskFields + " FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=?"
		args := []any{n.TenantID, n.WorkspaceID, id}
		out, e = nodeListRows(ctx, q, nodeSeek(q, pg, my, dm, args, args, "created_at", "id", cursor, limit), limit, fingerprint, func(row transaction.Row) (Task, error) { return scanTask(row) }, func(v Task) (time.Time, string) { return v.CreatedAt, v.ID })
		return e
	})
	if e != nil {
		return Page[Task]{}, e
	}
	return out, nil
}
