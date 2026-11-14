package datasets

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"strconv"
	"strings"
	"time"
)

type Page[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

func (s *Service) pageValues(values url.Values) url.Values {
	out := url.Values{}
	for k, v := range values {
		out[k] = append([]string{}, v...)
	}
	if _, exists := out["limit"]; !exists {
		out.Set("limit", strconv.Itoa(s.Identity.Config.Policy().UI.PageSize))
	}
	return out
}
func listPage[T any](ctx context.Context, s *Service, a identity.Auth, workspace, dataset, resource, table, fields, timeColumn string, values url.Values, scan func(transaction.Row) (T, error), position func(T) (time.Time, string)) (Page[T], error) {
	out := Page[T]{Items: []T{}}
	limit, cursor, scope, e := pagination.Parse(s.pageValues(values), "datasets:"+resource+":"+a.Me.Audience+":"+a.Me.User.ID+":"+workspace+":"+dataset)
	if e != nil {
		return committed(out, e)
	}
	e = s.transaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		if tx.Dialect() == transaction.Dameng && cursor.ID != "" && !datasetDamengTime(cursor.Time) {
			return failure("INVALID_CURSOR")
		}
		if dataset != "" {
			if _, e := lockedDataset(ctx, tx, w, dataset); e != nil {
				return e
			}
			if e := datasetReadable(ctx, tx, a, w, dataset); e != nil {
				return e
			}
		}
		if resource == "drafts" && !canWrite(a, w) {
			return failure("ACCESS_DENIED")
		}
		statement := "SELECT " + fields + " FROM " + table + " WHERE tenant_id=$1 AND workspace_id=$2"
		myStatement := "SELECT " + datasetMySQLFields(fields) + " FROM " + table + " WHERE tenant_id=? AND workspace_id=?"
		dmStatement := "SELECT " + datasetDamengFields(fields) + " FROM " + table + " WHERE tenant_id=? AND workspace_id=?"
		args := []any{w.TenantID, w.ID}
		dmArgs := []any{w.TenantID, w.ID}
		if dataset != "" {
			args = append(args, dataset)
			statement += " AND dataset_id=$3"
			myStatement += " AND dataset_id=?"
			dmStatement += " AND dataset_id=?"
			dmArgs = append(dmArgs, dataset)
		}
		if resource == "datasets" && !canWrite(a, w) {
			filter := " AND EXISTS(SELECT 1 FROM dataset_versions v WHERE v.dataset_id=datasets.id AND v.tenant_id=datasets.tenant_id AND v.workspace_id=datasets.workspace_id)"
			statement += filter
			myStatement += filter
			dmStatement += filter
		}
		if cursor.ID != "" {
			args = append(args, cursor.Time, cursor.ID)
			n := len(args)
			statement += " AND (" + timeColumn + ",id)<($" + strconv.Itoa(n-1) + ",$" + strconv.Itoa(n) + "::uuid)"
			myStatement += " AND (" + timeColumn + ",id)<(?,?)"
			dmStatement += " AND (" + timeColumn + "<? OR (" + timeColumn + "=? AND id<?))"
			dmArgs = append(dmArgs, cursor.Time, cursor.Time, cursor.ID)
		}
		args = append(args, limit+1)
		statement += " ORDER BY " + timeColumn + " DESC,id DESC LIMIT $" + strconv.Itoa(len(args))
		myStatement += " ORDER BY " + timeColumn + " DESC,id DESC LIMIT ?"
		dmStatement += " ORDER BY " + timeColumn + " DESC,id DESC LIMIT " + strconv.Itoa(limit+1)
		rows, e := tx.Query(ctx, datasetSQL(tx, statement, myStatement, args, args, transaction.SQL(transaction.Dameng, dmStatement, dmArgs...)))
		if e != nil {
			return e
		}
		for rows.Next() {
			v, e := scan(rows)
			if e != nil {
				rows.Close()
				return e
			}
			out.Items = append(out.Items, v)
		}
		rows.Close()
		if rows.Err() != nil {
			return rows.Err()
		}
		if len(out.Items) > limit {
			out.Items = out.Items[:limit]
			stamp, id := position(out.Items[limit-1])
			out.NextCursor = pagination.Encode(stamp, id, scope)
		}
		return nil
	})
	return committed(out, e)
}
func (s *Service) DatasetPage(ctx context.Context, a identity.Auth, workspace string, values url.Values) (Page[Dataset], error) {
	return listPage(ctx, s, a, workspace, "", "datasets", "datasets", datasetFields, "created_at", values, scanDataset, func(d Dataset) (time.Time, string) { return d.CreatedAt, d.ID })
}
func (s *Service) DraftPage(ctx context.Context, a identity.Auth, workspace, dataset string, values url.Values) (Page[Draft], error) {
	return listPage(ctx, s, a, workspace, dataset, "drafts", "dataset_drafts", draftFields, "created_at", values, scanDraft, func(d Draft) (time.Time, string) { return d.CreatedAt, d.ID })
}
func (s *Service) VersionPage(ctx context.Context, a identity.Auth, workspace, dataset string, values url.Values) (Page[Version], error) {
	return listPage(ctx, s, a, workspace, dataset, "versions", "dataset_versions", versionFields, "published_at", values, scanVersion, func(v Version) (time.Time, string) { return v.PublishedAt, v.ID })
}

type rowCursor struct {
	Scope string `json:"scope"`
	Line  int64  `json:"line"`
	ID    string `json:"id"`
}

func (s *Service) RowPage(ctx context.Context, a identity.Auth, workspace, dataset, draft string, values url.Values) (Page[Row], error) {
	out := Page[Row]{Items: []Row{}}
	limitValues := s.pageValues(values)
	cursorValue := limitValues.Get("cursor")
	limitValues.Del("cursor")
	limit, _, scope, e := pagination.Parse(limitValues, "dataset-rows:"+a.Me.Audience+":"+a.Me.User.ID+":"+workspace+":"+dataset+":"+draft)
	if e != nil {
		return committed(out, e)
	}
	var cursor rowCursor
	if entries, exists := values["cursor"]; exists && (len(entries) != 1 || entries[0] == "") {
		return out, failure("INVALID_CURSOR")
	}
	if cursorValue != "" {
		if len(cursorValue) > 512 || strings.ContainsAny(cursorValue, "\r\n") {
			return out, failure("INVALID_CURSOR")
		}
		raw, e := base64.RawURLEncoding.DecodeString(cursorValue)
		if e != nil || wire.DecodeStrict(raw, &cursor) != nil || cursor.Scope != scope || cursor.Line < 1 || cursor.Line > wire.MaximumRowsCeiling || !wire.ValidID(cursor.ID) {
			return out, failure("INVALID_CURSOR")
		}
	}
	e = s.transaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		if _, e := lockedDataset(ctx, tx, w, dataset); e != nil {
			return e
		}
		d, e := lockedDraft(ctx, tx, w, dataset, draft)
		if e != nil {
			return e
		}
		if e = draftAccess(a, w, d, false); e != nil {
			return e
		}
		statement := "SELECT " + rowFields + " FROM dataset_rows WHERE tenant_id=$1 AND workspace_id=$2 AND dataset_id=$3 AND draft_id=$4 AND NOT deleted"
		myStatement := "SELECT " + mysqlRowFields + " FROM dataset_rows WHERE tenant_id=? AND workspace_id=? AND dataset_id=? AND draft_id=? AND NOT deleted"
		dmStatement := "SELECT " + dmRowFields + " FROM dataset_rows WHERE tenant_id=? AND workspace_id=? AND dataset_id=? AND draft_id=? AND deleted=0"
		args := []any{w.TenantID, w.ID, dataset, draft}
		dmArgs := []any{w.TenantID, w.ID, dataset, draft}
		if cursor.ID != "" {
			args = append(args, cursor.Line, cursor.ID)
			statement += " AND (line_number,id)>($5,$6::uuid)"
			myStatement += " AND (line_number,id)>(?,?)"
			dmStatement += " AND (line_number>? OR (line_number=? AND id>?))"
			dmArgs = append(dmArgs, cursor.Line, cursor.Line, cursor.ID)
		}
		args = append(args, limit+1)
		statement += " ORDER BY line_number,id LIMIT $" + strconv.Itoa(len(args))
		myStatement += " ORDER BY line_number,id LIMIT ?"
		dmStatement += " ORDER BY line_number,id LIMIT " + strconv.Itoa(limit+1)
		rows, e := tx.Query(ctx, datasetSQL(tx, statement, myStatement, args, args, transaction.SQL(transaction.Dameng, dmStatement, dmArgs...)))
		if e != nil {
			return e
		}
		for rows.Next() {
			r, e := scanRow(rows)
			if e != nil {
				rows.Close()
				return e
			}
			out.Items = append(out.Items, r)
		}
		rows.Close()
		if rows.Err() != nil {
			return rows.Err()
		}
		if len(out.Items) > limit {
			out.Items = out.Items[:limit]
			last := out.Items[limit-1]
			raw, _ := json.Marshal(rowCursor{scope, last.LineNumber, last.ID})
			value := base64.RawURLEncoding.EncodeToString(raw)
			out.NextCursor = &value
		}
		return nil
	})
	return committed(out, e)
}
