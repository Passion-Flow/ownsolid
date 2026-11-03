package workspaces

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strconv"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
)

const mysqlMemberFields = `m.user_id,u.email_display,u.display_name,u.status,m.role,m.version,m.created_at,m.updated_at`
const damengMemberFields = `M.USER_ID,U.EMAIL_DISPLAY,U.DISPLAY_NAME,U.STATUS,M.ROLE,M.VERSION,M.CREATED_AT,M.UPDATED_AT`

func listScope(kind, tenant, workspace string, a identity.Auth) string {
	return kind + ":" + tenant + ":" + workspace + ":" + a.Me.Audience + ":" + a.Me.User.ID
}
func (s *Service) listOptions(values url.Values, scope string) (int, pagination.Cursor, string, error) {
	return pagination.ParseWithDefault(values, scope, s.Identity.Config.Policy().UI.PageSize)
}
func listSeek(q transaction.Executor, pg, my, dm string, pgArgs, myArgs []any, stampColumn, idColumn string, cursor pagination.Cursor, limit int) (transaction.Statement, error) {
	if limit < 1 || limit > pagination.MaximumLimit {
		return transaction.Statement{}, fail("VALIDATION_FAILED")
	}
	dmArgs := append([]any(nil), myArgs...)
	if q.Dialect() == transaction.Dameng && cursor.ID != "" {
		stamp := cursor.Time.UTC()
		if stamp.Year() < 1970 || stamp.Year() > 9999 || stamp.Nanosecond()%1000 != 0 {
			return transaction.Statement{}, fail("INVALID_CURSOR")
		}
		dm += " AND (" + stampColumn + "<? OR (" + stampColumn + "=? AND " + idColumn + "<?))"
		dmArgs = append(dmArgs, stamp, stamp, cursor.ID)
	}
	if cursor.ID != "" {
		index := len(pgArgs) + 1
		pg += " AND (" + stampColumn + "," + idColumn + ")<($" + strconv.Itoa(index) + ",$" + strconv.Itoa(index+1) + "::uuid)"
		pgArgs = append(pgArgs, cursor.Time, cursor.ID)
		my += " AND (" + stampColumn + "<? OR (" + stampColumn + "=? AND " + idColumn + "<?))"
		myArgs = append(myArgs, cursor.Time, cursor.Time, cursor.ID)
	}
	order := " ORDER BY " + stampColumn + " DESC," + idColumn + " DESC LIMIT "
	if q.Dialect() == transaction.Dameng {
		return transaction.SQL(transaction.Dameng, dm+order+strconv.Itoa(limit+1), dmArgs...), nil
	}
	pg += order + "$" + strconv.Itoa(len(pgArgs)+1)
	pgArgs = append(pgArgs, limit+1)
	my += order + "?"
	myArgs = append(myArgs, limit+1)
	if q.Dialect() == transaction.MySQL {
		return transaction.SQL(transaction.MySQL, my, myArgs...), nil
	}
	if q.Dialect() != transaction.PostgreSQL {
		return transaction.Statement{}, fail("WORKSPACE_STORAGE_MISMATCH")
	}
	return transaction.SQL(transaction.PostgreSQL, pg, pgArgs...), nil
}
func listRows[T any](ctx context.Context, q transaction.Executor, statement transaction.Statement, limit int, fingerprint string, scan func(transaction.Row) (T, error), position func(T) (time.Time, string)) (Page[T], error) {
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
func scanListedWorkspace(row transaction.Row) (Workspace, error) {
	var w Workspace
	e := row.Scan(&w.ID, &w.TenantID, &w.Name, &w.Status, &w.Version, &w.IsDefault, &w.CreatedAt, &w.UpdatedAt, &w.Role)
	return w, e
}
func scanListedMember(row transaction.Row) (Membership, error) {
	var m Membership
	e := row.Scan(&m.UserID, &m.Email, &m.DisplayName, &m.UserStatus, &m.Role, &m.Version, &m.CreatedAt, &m.UpdatedAt)
	return m, e
}
func (s *Service) List(ctx context.Context, a identity.Auth, values url.Values) (Page[Workspace], error) {
	var out Page[Workspace]
	e := s.readActor(ctx, a, func(ctx context.Context, q transaction.Executor) error {
		var tenant string
		e := q.QueryRow(ctx, workspaceSQL(q, "SELECT i.tenant_id::text FROM installation_state i JOIN tenants t ON t.id=i.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND t.status='active'", "SELECT i.tenant_id FROM installation_state i JOIN tenants t ON t.id=i.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND t.status='active'", `SELECT I.TENANT_ID FROM INSTALLATION_STATE I JOIN TENANTS T ON T.ID=I.TENANT_ID WHERE I.ID=1 AND I.BOOTSTRAP_COMPLETE=1 AND T.STATUS='active'`)).Scan(&tenant)
		if errors.Is(e, sql.ErrNoRows) {
			return fail("RESOURCE_NOT_FOUND")
		}
		if e != nil {
			return e
		}
		limit, cursor, fingerprint, e := s.listOptions(values, listScope("workspaces", tenant, "", a))
		if e != nil {
			return e
		}
		pg := "SELECT " + workspaceFields + `,m.role FROM workspaces w JOIN installation_state i ON i.tenant_id=w.tenant_id JOIN tenants t ON t.id=w.tenant_id LEFT JOIN workspace_memberships m ON m.tenant_id=w.tenant_id AND m.workspace_id=w.id AND m.user_id=$1 WHERE i.id=1 AND i.bootstrap_complete AND t.status='active' AND i.tenant_id=$2 AND ($3 OR (m.user_id IS NOT NULL AND w.status='active'))`
		my := "SELECT " + mysqlWorkspaceFields + `,m.role FROM workspaces w JOIN installation_state i ON i.tenant_id=w.tenant_id JOIN tenants t ON t.id=w.tenant_id LEFT JOIN workspace_memberships m ON m.tenant_id=w.tenant_id AND m.workspace_id=w.id AND m.user_id=? WHERE i.id=1 AND i.bootstrap_complete=1 AND t.status='active' AND i.tenant_id=? AND (? OR (m.user_id IS NOT NULL AND w.status='active'))`
		dm := "SELECT " + damengWorkspaceFields + `,M.ROLE FROM WORKSPACES W JOIN INSTALLATION_STATE I ON I.TENANT_ID=W.TENANT_ID JOIN TENANTS T ON T.ID=W.TENANT_ID LEFT JOIN WORKSPACE_MEMBERSHIPS M ON M.TENANT_ID=W.TENANT_ID AND M.WORKSPACE_ID=W.ID AND M.USER_ID=? WHERE I.ID=1 AND I.BOOTSTRAP_COMPLETE=1 AND T.STATUS='active' AND I.TENANT_ID=? AND (?=1 OR (M.USER_ID IS NOT NULL AND W.STATUS='active'))`
		args := []any{a.Me.User.ID, tenant, admin(a)}
		query, e := listSeek(q, pg, my, dm, args, args, "w.created_at", "w.id", cursor, limit)
		if e != nil {
			return e
		}
		out, e = listRows(ctx, q, query, limit, fingerprint, scanListedWorkspace, func(w Workspace) (time.Time, string) { return w.CreatedAt, w.ID })
		return e
	})
	if e != nil {
		return Page[Workspace]{}, e
	}
	return out, nil
}
func (s *Service) Members(ctx context.Context, a identity.Auth, id string, values url.Values) (Page[Membership], error) {
	var out Page[Membership]
	e := s.readScope(ctx, a, id, true, func(ctx context.Context, q transaction.Executor, w Workspace) error {
		limit, cursor, fingerprint, e := s.listOptions(values, listScope("members", w.TenantID, w.ID, a))
		if e != nil {
			return e
		}
		pg := "SELECT " + memberFields + " FROM workspace_memberships m JOIN users u ON u.id=m.user_id WHERE m.tenant_id=$1 AND m.workspace_id=$2"
		my := "SELECT " + mysqlMemberFields + " FROM workspace_memberships m JOIN users u ON u.id=m.user_id WHERE m.tenant_id=? AND m.workspace_id=?"
		dm := "SELECT " + damengMemberFields + " FROM WORKSPACE_MEMBERSHIPS M JOIN USERS U ON U.ID=M.USER_ID WHERE M.TENANT_ID=? AND M.WORKSPACE_ID=?"
		args := []any{w.TenantID, w.ID}
		query, e := listSeek(q, pg, my, dm, args, args, "m.created_at", "m.user_id", cursor, limit)
		if e != nil {
			return e
		}
		out, e = listRows(ctx, q, query, limit, fingerprint, scanListedMember, func(m Membership) (time.Time, string) { return m.CreatedAt, m.UserID })
		return e
	})
	if e != nil {
		return Page[Membership]{}, e
	}
	return out, nil
}
func (s *Service) Notes(ctx context.Context, a identity.Auth, id string, values url.Values) (Page[Note], error) {
	var out Page[Note]
	e := s.readScope(ctx, a, id, false, func(ctx context.Context, q transaction.Executor, w Workspace) error {
		limit, cursor, fingerprint, e := s.listOptions(values, listScope("notes", w.TenantID, w.ID, a))
		if e != nil {
			return e
		}
		pg := "SELECT " + noteFields + " FROM workspace_notes WHERE tenant_id=$1 AND workspace_id=$2 AND deleted_at IS NULL"
		my := "SELECT " + mysqlNoteFields + " FROM workspace_notes WHERE tenant_id=? AND workspace_id=? AND deleted_at IS NULL"
		dm := "SELECT " + damengNoteFields + " FROM WORKSPACE_NOTES WHERE TENANT_ID=? AND WORKSPACE_ID=? AND DELETED_AT IS NULL"
		args := []any{w.TenantID, w.ID}
		query, e := listSeek(q, pg, my, dm, args, args, "created_at", "id", cursor, limit)
		if e != nil {
			return e
		}
		out, e = listRows(ctx, q, query, limit, fingerprint, scanNote, func(n Note) (time.Time, string) { return n.CreatedAt, n.ID })
		return e
	})
	if e != nil {
		return Page[Note]{}, e
	}
	return out, nil
}
