package distribution

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"strconv"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
	wire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
)

type Page[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

type RequestRecord struct {
	ID                 string        `json:"id"`
	WorkspaceID        string        `json:"workspace_id"`
	GatewayID          string        `json:"gateway_id"`
	NodeID             string        `json:"node_id"`
	APIKeyID           string        `json:"api_key_id"`
	ActorID            string        `json:"actor_id"`
	AliasID            string        `json:"alias_id"`
	AliasName          string        `json:"alias_name"`
	DeploymentID       string        `json:"deployment_id"`
	DeploymentRevision int64         `json:"deployment_revision"`
	State              string        `json:"state"`
	CapacityHeld       bool          `json:"capacity_held"`
	Stream             bool          `json:"stream"`
	AdmittedAt         time.Time     `json:"admitted_at"`
	DeadlineAt         time.Time     `json:"deadline_at"`
	ReportedAt         *time.Time    `json:"reported_at"`
	ReconciledAt       *time.Time    `json:"reconciled_at"`
	ActiveRecoveryID   *string       `json:"active_recovery_id"`
	Outcome            *wire.Outcome `json:"outcome"`
}

const requestFields = `r.id::text,r.workspace_id::text,r.gateway_id::text,r.node_id::text,r.api_key_id::text,r.actor_user_id::text,r.alias_id::text,a.name,r.deployment_id::text,r.deployment_revision,r.state,r.capacity_held,r.stream,r.admitted_at,r.deadline_at,r.reported_at,r.reconciled_at,r.active_recovery_id::text,r.outcome`
const requestJoins = ` FROM gateway_requests r JOIN model_aliases a ON a.tenant_id=r.tenant_id AND a.workspace_id=r.workspace_id AND a.id=r.alias_id`

func scanRequest(row transaction.Row) (RequestRecord, error) {
	var out RequestRecord
	var raw []byte
	e := row.Scan(&out.ID, &out.WorkspaceID, &out.GatewayID, &out.NodeID, &out.APIKeyID, &out.ActorID, &out.AliasID, &out.AliasName, &out.DeploymentID, &out.DeploymentRevision, &out.State, &out.CapacityHeld, &out.Stream, &out.AdmittedAt, &out.DeadlineAt, &out.ReportedAt, &out.ReconciledAt, &out.ActiveRecoveryID, &raw)
	if errors.Is(e, sql.ErrNoRows) {
		return RequestRecord{}, fail("RESOURCE_NOT_FOUND")
	}
	if e != nil {
		if e != nil {
			return RequestRecord{}, e
		}
		return out, nil
	}
	if raw != nil {
		v, e := wire.DecodeOutcome(raw)
		if e != nil {
			return RequestRecord{}, fail("STORAGE_UNAVAILABLE")
		}
		out.Outcome = &v
	}
	out.AdmittedAt = out.AdmittedAt.UTC()
	out.DeadlineAt = out.DeadlineAt.UTC()
	out.ReportedAt = distributionUTCPointer(out.ReportedAt)
	out.ReconciledAt = distributionUTCPointer(out.ReconciledAt)
	return out, nil
}

// listPage uses trusted module-owned SQL selectors. Cursor values are data; scope is rechecked on every page.
func listPage[T any](ctx context.Context, s *Service, a identity.Auth, workspace string, values url.Values, resource, query, mysqlQuery, table, timeColumn, idColumn string, scan func(transaction.Row) (T, error), position func(T) (time.Time, string), prepare func(context.Context, transaction.Executor, workspaces.Workspace, *T) error) (Page[T], error) {
	out := Page[T]{Items: []T{}}
	if _, e := s.repository(); e != nil {
		return Page[T]{}, distributionError(e)
	}
	limit, cursor, scope, e := pagination.ParseWithDefault(values, "distribution:"+resource+":"+a.Me.Audience+":"+a.Me.User.ID+":"+workspace, s.Identity.Config.Policy().UI.PageSize)
	if e != nil {
		return out, e
	}
	e = s.transaction(ctx, a, workspace, false, nil, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		args := []any{w.TenantID, w.ID}
		statement := query + " WHERE " + table + ".tenant_id=$1 AND " + table + ".workspace_id=$2"
		mysqlStatement := mysqlQuery + " WHERE " + table + ".tenant_id=? AND " + table + ".workspace_id=?"
		if !admin(a) {
			switch resource {
			case "keys":
				args = append(args, a.Me.User.ID)
				statement += " AND k.created_by=$3"
				mysqlStatement += " AND k.created_by=?"
			case "requests":
				args = append(args, a.Me.User.ID)
				statement += " AND r.actor_user_id=$3"
				mysqlStatement += " AND r.actor_user_id=?"
			case "aliases":
				statement += " AND disabled_at IS NULL"
				mysqlStatement += " AND disabled_at IS NULL"
			}
		}
		if cursor.ID != "" {
			args = append(args, cursor.Time, cursor.ID)
			n := len(args)
			statement += " AND (" + timeColumn + "," + idColumn + ")<($" + strconv.Itoa(n-1) + ",$" + strconv.Itoa(n) + "::uuid)"
			mysqlStatement += " AND (" + timeColumn + "," + idColumn + ")<(?,?)"
		}
		args = append(args, limit+1)
		statement += " ORDER BY " + timeColumn + " DESC," + idColumn + " DESC LIMIT $" + strconv.Itoa(len(args))
		mysqlStatement += " ORDER BY " + timeColumn + " DESC," + idColumn + " DESC LIMIT ?"
		selected := distributionSQL(tx, statement, mysqlStatement, args...)
		if tx.Dialect() == transaction.Dameng {
			selected, e = nativeHistoryPage(resource, w.TenantID, w.ID, a.Me.User.ID, admin(a), cursor.Time, cursor.ID, limit)
			if e != nil {
				return e
			}
		}
		rows, e := tx.Query(ctx, selected)
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
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		if len(out.Items) > limit {
			out.Items = out.Items[:limit]
			stamp, id := position(out.Items[limit-1])
			out.NextCursor = pagination.Encode(stamp, id, scope)
		}
		if prepare != nil {
			for i := range out.Items {
				if e = prepare(ctx, tx, w, &out.Items[i]); e != nil {
					return e
				}
			}
		}
		return nil
	})
	if e != nil {
		return Page[T]{Items: []T{}}, e
	}
	return out, nil
}
func (s *Service) GatewayPage(ctx context.Context, a identity.Auth, workspace string, values url.Values) (Page[Gateway], error) {
	if !admin(a) {
		return Page[Gateway]{}, fail("ACCESS_DENIED")
	}
	return listPage(ctx, s, a, workspace, values, "gateways", "SELECT "+gatewayFields+" FROM gateway_instances g", "SELECT "+mysqlGatewayFields+" FROM gateway_instances g", "g", "g.created_at", "g.id", scanGateway, func(v Gateway) (time.Time, string) { return v.CreatedAt, v.ID }, nil)
}
func (s *Service) AliasPage(ctx context.Context, a identity.Auth, workspace string, values url.Values) (Page[Alias], error) {
	return listPage(ctx, s, a, workspace, values, "aliases", "SELECT "+aliasFields+" FROM model_aliases a", "SELECT "+mysqlAliasFields+" FROM model_aliases a", "a", "a.created_at", "a.id", scanAlias, func(v Alias) (time.Time, string) { return v.CreatedAt, v.ID }, s.aliasAvailability)
}
func (s *Service) KeyPage(ctx context.Context, a identity.Auth, workspace string, values url.Values) (Page[APIKey], error) {
	return listPage(ctx, s, a, workspace, values, "keys", "SELECT "+keyFields+keyJoins, "SELECT "+mysqlKeyFields+keyJoins, "k", "k.created_at", "k.id", scanKey, func(v APIKey) (time.Time, string) { return v.CreatedAt, v.ID }, nil)
}
func (s *Service) RequestPage(ctx context.Context, a identity.Auth, workspace string, values url.Values) (Page[RequestRecord], error) {
	return listPage(ctx, s, a, workspace, values, "requests", "SELECT "+requestFields+requestJoins, "SELECT "+mysqlRequestFields+requestJoins, "r", "r.admitted_at", "r.id", scanRequest, func(v RequestRecord) (time.Time, string) { return v.AdmittedAt, v.ID }, nil)
}
func (s *Service) Request(ctx context.Context, a identity.Auth, workspace, id string) (RequestRecord, error) {
	if !wire.ValidID(id) {
		return RequestRecord{}, fail("VALIDATION_FAILED")
	}
	var out RequestRecord
	e := s.transaction(ctx, a, workspace, false, nil, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		var e error
		out, e = scanRequest(tx.QueryRow(ctx, distributionNativeStatement(tx, "SELECT "+nativeRequestFields+nativeRequestJoins+" WHERE R.TENANT_ID=? AND R.WORKSPACE_ID=? AND R.ID=? AND (?=1 OR R.ACTOR_USER_ID=?)", []any{w.TenantID, w.ID, id, admin(a), a.Me.User.ID}, "SELECT "+requestFields+requestJoins+" WHERE r.tenant_id=$1 AND r.workspace_id=$2 AND r.id=$3 AND ($4 OR r.actor_user_id=$5)", `SELECT `+mysqlRequestFields+requestJoins+` WHERE r.tenant_id=? AND r.workspace_id=? AND r.id=? AND (? OR r.actor_user_id=?)`, []any{w.TenantID, w.ID, id, admin(a), a.Me.User.ID}, []any{w.TenantID, w.ID, id, admin(a), a.Me.User.ID})))
		return e
	})
	if e != nil {
		return RequestRecord{}, e
	}
	return out, nil
}
func (s *Service) Gateway(ctx context.Context, a identity.Auth, workspace, id string) (Gateway, error) {
	if !admin(a) {
		return Gateway{}, fail("ACCESS_DENIED")
	}
	if !wire.ValidID(id) {
		return Gateway{}, fail("VALIDATION_FAILED")
	}
	var out Gateway
	e := s.transaction(ctx, a, workspace, false, nil, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		var e error
		out, e = scanGateway(tx.QueryRow(ctx, distributionNativeStatement(tx, "SELECT "+nativeGatewayFields+" FROM GATEWAY_INSTANCES WHERE TENANT_ID=? AND WORKSPACE_ID=? AND ID=?", []any{w.TenantID, w.ID, id}, "SELECT "+gatewayFields+" FROM gateway_instances WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3", `SELECT `+mysqlGatewayFields+` FROM gateway_instances WHERE tenant_id=? AND workspace_id=? AND id=?`, []any{w.TenantID, w.ID, id}, []any{w.TenantID, w.ID, id})))
		return e
	})
	if e != nil {
		return Gateway{}, e
	}
	return out, nil
}
func (s *Service) Alias(ctx context.Context, a identity.Auth, workspace, id string) (Alias, error) {
	if !wire.ValidID(id) {
		return Alias{}, fail("VALIDATION_FAILED")
	}
	var out Alias
	e := s.transaction(ctx, a, workspace, false, nil, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		var e error
		out, e = scanAlias(tx.QueryRow(ctx, distributionNativeStatement(tx, "SELECT "+nativeAliasFields+" FROM MODEL_ALIASES WHERE TENANT_ID=? AND WORKSPACE_ID=? AND ID=? AND (?=1 OR DISABLED_AT IS NULL)", []any{w.TenantID, w.ID, id, admin(a)}, "SELECT "+aliasFields+" FROM model_aliases WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 AND ($4 OR disabled_at IS NULL)", `SELECT `+mysqlAliasFields+` FROM model_aliases WHERE tenant_id=? AND workspace_id=? AND id=? AND (? OR disabled_at IS NULL)`, []any{w.TenantID, w.ID, id, admin(a)}, []any{w.TenantID, w.ID, id, admin(a)})))
		if e != nil {
			return e
		}
		return s.aliasAvailability(ctx, tx, w, &out)
	})
	if e != nil {
		return Alias{}, e
	}
	return out, nil
}
func (s *Service) Key(ctx context.Context, a identity.Auth, workspace, id string) (APIKey, error) {
	if !wire.ValidID(id) {
		return APIKey{}, fail("VALIDATION_FAILED")
	}
	var out APIKey
	e := s.transaction(ctx, a, workspace, false, nil, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		var e error
		out, e = scanKey(tx.QueryRow(ctx, distributionNativeStatement(tx, "SELECT "+nativeKeyFields+nativeKeyJoins+" WHERE K.TENANT_ID=? AND K.WORKSPACE_ID=? AND K.ID=? AND (?=1 OR K.CREATED_BY=?)", []any{w.TenantID, w.ID, id, admin(a), a.Me.User.ID}, "SELECT "+keyFields+keyJoins+" WHERE k.tenant_id=$1 AND k.workspace_id=$2 AND k.id=$3 AND ($4 OR k.created_by=$5)", `SELECT `+mysqlKeyFields+keyJoins+` WHERE k.tenant_id=? AND k.workspace_id=? AND k.id=? AND (? OR k.created_by=?)`, []any{w.TenantID, w.ID, id, admin(a), a.Me.User.ID}, []any{w.TenantID, w.ID, id, admin(a), a.Me.User.ID})))
		return e
	})
	if e != nil {
		return APIKey{}, e
	}
	return out, nil
}
