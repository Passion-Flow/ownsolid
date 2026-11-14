package plugins

import (
	"context"
	"net/url"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
	wire "github.com/Passion-Flow/ownsolid/api/plugins/v1"
	"sort"
	"strconv"
)

type Configuration struct {
	Enabled       bool                `json:"enabled"`
	CanManage     bool                `json:"can_manage"`
	TrustedKeyIDs []string            `json:"trusted_key_ids"`
	ABI           string              `json:"abi"`
	Executor      string              `json:"executor"`
	Limits        ConfigurationLimits `json:"limits"`
}
type ConfigurationLimits struct {
	CapsuleBytes      int `json:"capsule_bytes"`
	ManifestBytes     int `json:"manifest_bytes"`
	ResultBytes       int `json:"result_bytes"`
	MaximumModels     int `json:"maximum_models"`
	MaximumVersions   int `json:"maximum_versions"`
	MaximumGrants     int `json:"maximum_grants"`
	MaximumReferences int `json:"maximum_references"`
}

func (s *Service) Configuration(ctx context.Context, a identity.Auth) (Configuration, error) {
	var out Configuration
	e := s.adminTransaction(ctx, a, false, func(context.Context, transaction.Executor, string) error {
		p := s.policy()
		out = Configuration{Enabled: s.Identity.Config.Plugins.Enabled, CanManage: true, TrustedKeyIDs: []string{}, ABI: wire.ABI, Executor: wire.Executor, Limits: ConfigurationLimits{p.CapsuleBytes, p.ManifestBytes, p.ResultBytes, p.MaximumModels, p.MaximumVersions, p.MaximumGrants, p.MaximumReferences}}
		for _, key := range s.Identity.Config.Plugins.TrustedKeys {
			out.TrustedKeyIDs = append(out.TrustedKeyIDs, key.KeyID)
		}
		sort.Strings(out.TrustedKeyIDs)
		return nil
	})
	if e != nil {
		return Configuration{}, e
	}
	return out, nil
}
func (s *Service) page(values url.Values, scope string) (int, pagination.Cursor, string, error) {
	return pagination.ParseWithDefault(values, scope, s.Identity.Config.Policy().UI.PageSize)
}
func (s *Service) List(ctx context.Context, a identity.Auth, values url.Values) (Page[Version], error) {
	out := Page[Version]{Items: []Version{}}
	if _, e := s.repository(); e != nil {
		return Page[Version]{}, pluginError(e)
	}
	limit, cursor, scope, e := s.page(values, "plugin-versions:"+a.Me.Audience+":"+a.Me.User.ID)
	if e != nil {
		return Page[Version]{}, e
	}
	e = s.adminTransaction(ctx, a, false, func(ctx context.Context, q transaction.Executor, tenant string) error {
		pg := "SELECT " + versionFields + " FROM plugin_versions WHERE tenant_id=$1"
		my := "SELECT " + mysqlVersionFields + " FROM plugin_versions WHERE tenant_id=?"
		args := []any{tenant}
		if cursor.ID != "" {
			args = append(args, cursor.Time, cursor.ID)
			pg += " AND (created_at,id)<($2,$3::uuid)"
			my += " AND (created_at,id)<(?,?)"
		}
		args = append(args, limit+1)
		pg += " ORDER BY created_at DESC,id DESC LIMIT $" + strconv.Itoa(len(args))
		my += " ORDER BY created_at DESC,id DESC LIMIT ?"
		statement := pluginSQL(q, pg, my, pluginNativeQuery{}, args...)
		if q.Dialect() == transaction.Dameng {
			var e error
			statement, e = pluginNativePage("SELECT "+nativeVersionFields+" FROM plugin_versions WHERE tenant_id=?", "created_at", "id", []any{tenant}, cursor, limit)
			if e != nil {
				return e
			}
		}
		rows, e := q.Query(ctx, statement)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			v, e := scanVersion(rows)
			if e != nil {
				return e
			}
			out.Items = append(out.Items, s.present(v))
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
		return Page[Version]{}, e
	}
	return out, nil
}
func (s *Service) Grants(ctx context.Context, a identity.Auth, workspace string, values url.Values) (Page[Grant], error) {
	out := Page[Grant]{Items: []Grant{}}
	if !validID(workspace) || a.Me.Audience != "portal" && !admin(a) {
		return Page[Grant]{}, failure("ACCESS_DENIED")
	}
	if _, e := s.repository(); e != nil {
		return Page[Grant]{}, pluginError(e)
	}
	limit, cursor, scope, e := s.page(values, "plugin-grants:"+a.Me.Audience+":"+a.Me.User.ID+":"+workspace)
	if e != nil {
		return Page[Grant]{}, e
	}
	e = s.storageTransaction(ctx, func(ctx context.Context, q transaction.Executor) error {
		if e := s.Identity.RecheckOrderedWithin(ctx, q, a); e != nil {
			return e
		}
		w, e := s.Workspaces.RecheckScopeWithin(ctx, q, a, workspace)
		if e != nil {
			return e
		}
		if !admin(a) && (w.Role == nil || w.Status != "active") {
			return failure("ACCESS_DENIED")
		}
		pg := "SELECT " + grantFields + " FROM plugin_grants g WHERE g.tenant_id=$1 AND g.workspace_id=$2"
		my := "SELECT " + mysqlGrantFields + " FROM plugin_grants g WHERE g.tenant_id=? AND g.workspace_id=?"
		args := []any{w.TenantID, w.ID}
		if !admin(a) {
			filter := " AND g.enabled AND EXISTS(SELECT 1 FROM plugin_versions v WHERE v.tenant_id=g.tenant_id AND v.id=g.plugin_version_id AND v.state='installed')"
			pg += filter
			my += filter
			if !s.Identity.Config.Plugins.Enabled {
				pg += " AND false"
				my += " AND false"
			}
		}
		if cursor.ID != "" {
			args = append(args, cursor.Time, cursor.ID)
			pg += " AND (g.created_at,g.id)<($3,$4::uuid)"
			my += " AND (g.created_at,g.id)<(?,?)"
		}
		args = append(args, limit+1)
		pg += " ORDER BY g.created_at DESC,g.id DESC LIMIT $" + strconv.Itoa(len(args))
		my += " ORDER BY g.created_at DESC,g.id DESC LIMIT ?"
		statement := pluginSQL(q, pg, my, pluginNativeQuery{}, args...)
		if q.Dialect() == transaction.Dameng {
			base := "SELECT " + nativeGrantFields + " FROM plugin_grants g WHERE g.tenant_id=? AND g.workspace_id=?"
			if !admin(a) {
				base += " AND g.enabled=1 AND EXISTS(SELECT 1 FROM plugin_versions v WHERE v.tenant_id=g.tenant_id AND v.id=g.plugin_version_id AND v.state='installed')"
				if !s.Identity.Config.Plugins.Enabled {
					base += " AND 1=0"
				}
			}
			var e error
			statement, e = pluginNativePage(base, "g.created_at", "g.id", []any{w.TenantID, w.ID}, cursor, limit)
			if e != nil {
				return e
			}
		}
		rows, e := q.Query(ctx, statement)
		if e != nil {
			return e
		}
		records := []grantRecord{}
		for rows.Next() {
			g, e := scanGrant(rows)
			if e != nil {
				rows.Close()
				return e
			}
			records = append(records, g)
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			return e
		}
		more := len(records) > limit
		if more {
			records = records[:limit]
		}
		for _, g := range records {
			if e = s.grantVersion(ctx, q, w.TenantID, &g); e != nil {
				return e
			}
			out.Items = append(out.Items, g.Grant)
		}
		if more {
			last := records[len(records)-1]
			out.NextCursor = pagination.Encode(last.CreatedAt, last.ID, scope)
		}
		return nil
	})
	if e != nil {
		return Page[Grant]{}, e
	}
	return out, nil
}
