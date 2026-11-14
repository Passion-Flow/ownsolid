package sso

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
)

const mysqlConnectorColumns = `id,name,provider,origin,client_id,enabled,revision,auth_version,created_at,updated_at`
const maximumSSOCatalogCursorBytes = 2048
const minimumSSOCatalogYear = 1000
const maximumSSOCatalogYear = 9999
const connectorCursorDomain = "m11-connectors-cursor"

func (s *Service) catalogRepository() (transaction.Store, error) {
	if s == nil || s.Identity == nil {
		return nil, fail("SSO_CONFIGURATION_REQUIRED")
	}
	return s.Identity.Repository()
}

func decodeCatalogCursor(raw string) (pageCursor, string, []byte, error) {
	var cursor pageCursor
	invalid := fail("VALIDATION_FAILED")
	if raw == "" {
		return cursor, "", nil, nil
	}
	if len(raw) > maximumSSOCatalogCursorBytes {
		return cursor, "", nil, invalid
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 2 {
		return cursor, "", nil, invalid
	}
	payload, pe := base64.RawURLEncoding.Strict().DecodeString(parts[0])
	mac, me := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	if pe != nil || me != nil || base64.RawURLEncoding.EncodeToString(payload) != parts[0] || base64.RawURLEncoding.EncodeToString(mac) != parts[1] || len(mac) != sha256.Size || json.Unmarshal(payload, &cursor) != nil {
		return pageCursor{}, "", nil, invalid
	}
	canonical, e := json.Marshal(cursor)
	_, offset := cursor.CreatedAt.Zone()
	if e != nil || !bytes.Equal(payload, canonical) || !canonicalID(cursor.ID) || cursor.CreatedAt.IsZero() || cursor.CreatedAt.Year() < minimumSSOCatalogYear || cursor.CreatedAt.Year() > maximumSSOCatalogYear || offset != 0 || cursor.CreatedAt.Nanosecond()%int(time.Microsecond) != 0 {
		return pageCursor{}, "", nil, invalid
	}
	cursor.CreatedAt = cursor.CreatedAt.UTC()
	return cursor, parts[0], mac, nil
}

func catalogTenantWithin(ctx context.Context, q transaction.Executor) (string, error) {
	var tenant sql.NullString
	var complete bool
	e := q.QueryRow(ctx, sourceSQL(q,
		`SELECT tenant_id::text,bootstrap_complete FROM installation_state WHERE id=1 FOR SHARE`,
		`SELECT tenant_id,bootstrap_complete FROM installation_state WHERE id=1 FOR SHARE`, "SELECT TENANT_ID,BOOTSTRAP_COMPLETE FROM INSTALLATION_STATE WHERE ID=1 FOR UPDATE OF ID")).Scan(&tenant, &complete)
	if errors.Is(e, sql.ErrNoRows) || e == nil && (!complete || !tenant.Valid || !canonicalID(tenant.String)) {
		return "", fail("SSO_DISABLED")
	}
	if e != nil {
		return "", e
	}
	var status string
	e = q.QueryRow(ctx, sourceSQL(q, `SELECT status FROM tenants WHERE id=$1 FOR SHARE`, `SELECT status FROM tenants WHERE id=? FOR SHARE`, "SELECT STATUS FROM TENANTS WHERE ID=? FOR UPDATE OF ID", tenant.String)).Scan(&status)
	if errors.Is(e, sql.ErrNoRows) || e == nil && status != "active" {
		return "", fail("SSO_DISABLED")
	}
	return tenant.String, e
}

func (s *Service) PublicConnectors(ctx context.Context) ([]PublicConnector, error) {
	result := []PublicConnector{}
	if s == nil {
		return nil, fail("SSO_CONFIGURATION_REQUIRED")
	}
	if !s.enabled {
		return result, nil
	}
	if e := s.policy.Validate(); e != nil {
		return nil, e
	}
	store, e := s.catalogRepository()
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy.OperationSeconds))
	defer cancel()
	rows, e := store.Query(ctx, sourceBoundedSQL(store,
		`SELECT c.id::text,c.name,c.provider,c.origin FROM sso_connectors c JOIN installation_state i ON i.tenant_id=c.tenant_id JOIN tenants t ON t.id=c.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND t.status='active' AND c.enabled ORDER BY c.created_at,c.id LIMIT $1`,
		`SELECT c.id,c.name,c.provider,c.origin FROM sso_connectors c JOIN installation_state i ON i.tenant_id=c.tenant_id JOIN tenants t ON t.id=c.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND t.status='active' AND c.enabled=1 ORDER BY c.created_at,c.id LIMIT ?`,
		"SELECT C.ID,C.NAME,C.PROVIDER,C.ORIGIN FROM SSO_CONNECTORS C JOIN INSTALLATION_STATE I ON I.TENANT_ID=C.TENANT_ID JOIN TENANTS T ON T.ID=C.TENANT_ID WHERE I.ID=1 AND I.BOOTSTRAP_COMPLETE=1 AND T.STATUS='active' AND C.ENABLED=1 ORDER BY C.CREATED_AT,C.ID LIMIT ",
		"",
		s.policy.MaximumConnectors,
		config.SSOMaximumConnectors))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var value PublicConnector
		var origin string
		if e = rows.Scan(&value.ID, &value.Name, &value.Provider, &origin); e != nil {
			return nil, e
		}
		if !canonicalID(value.ID) || value.Provider != ProviderGitea {
			return nil, fail("STORAGE_UNAVAILABLE")
		}
		if _, approved := s.approved[origin]; approved {
			result = append(result, value)
		}
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	return result, nil
}

func (s *Service) ListConnectors(ctx context.Context, a identity.Auth, limit int, rawCursor string) (ConnectorPage, error) {
	empty := ConnectorPage{Items: []Connector{}}
	if limit < 1 || limit > pagination.MaximumLimit {
		return empty, fail("VALIDATION_FAILED")
	}
	cursor, payload, mac, e := decodeCatalogCursor(rawCursor)
	if e != nil {
		return empty, e
	}
	if a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin {
		return empty, fail("ACCESS_DENIED")
	}
	result := empty
	e = s.actorTransaction(ctx, a, connectorReadFence, connectorShared, nil, func(ctx context.Context, q transaction.Executor, tenantID string) error {
		if rawCursor != "" && !hmac.Equal(mac, security.MAC(s.Identity.HMACKey, connectorCursorDomain, tenantID, a.Me.User.ID, a.Me.Audience, payload)) {
			return fail("VALIDATION_FAILED")
		}
		pg := `SELECT ` + connectorColumns + ` FROM sso_connectors WHERE tenant_id=$1`
		my := `SELECT ` + mysqlConnectorColumns + ` FROM sso_connectors WHERE tenant_id=?`
		dm := "SELECT " + damengConnectorColumns + " FROM SSO_CONNECTORS WHERE TENANT_ID=?"
		pgArgs, myArgs := []any{tenantID}, []any{tenantID}
		if rawCursor != "" {
			if q.Dialect() == transaction.Dameng && !damengTimestamp(cursor.CreatedAt) {
				return fail("VALIDATION_FAILED")
			}
			dm += " AND (CREATED_AT<? OR (CREATED_AT=? AND ID<?))"
			pg += ` AND (created_at,id)<($2,$3::uuid)`
			my += ` AND (created_at<? OR (created_at=? AND id<?))`
			pgArgs = append(pgArgs, cursor.CreatedAt, cursor.ID)
			myArgs = append(myArgs, cursor.CreatedAt, cursor.CreatedAt, cursor.ID)
		}
		dm += " ORDER BY CREATED_AT DESC,ID DESC LIMIT " + strconv.Itoa(limit+1)
		dmArgs := append([]any(nil), myArgs...)
		pg += ` ORDER BY created_at DESC,id DESC LIMIT $` + strconv.Itoa(len(pgArgs)+1)
		my += ` ORDER BY created_at DESC,id DESC LIMIT ?`
		pgArgs, myArgs = append(pgArgs, limit+1), append(myArgs, limit+1)
		statement := sourceSQL(q, pg, my, dm, myArgs...)
		switch q.Dialect() {
		case transaction.PostgreSQL:
			statement = transaction.SQL(transaction.PostgreSQL, pg, pgArgs...)
		case transaction.Dameng:
			statement = transaction.SQL(transaction.Dameng, dm, dmArgs...)
		}
		rows, e := q.Query(ctx, statement)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			value, e := scanConnector(rows)
			if e != nil {
				return e
			}
			result.Items = append(result.Items, value)
		}
		if e = rows.Err(); e != nil {
			return e
		}
		if len(result.Items) > limit {
			result.Items = result.Items[:limit]
			last := result.Items[limit-1]
			raw, e := json.Marshal(pageCursor{last.CreatedAt.UTC(), last.ID})
			if e != nil {
				return fail("STORAGE_UNAVAILABLE")
			}
			encoded := base64.RawURLEncoding.EncodeToString(raw)
			next := encoded + "." + base64.RawURLEncoding.EncodeToString(security.MAC(s.Identity.HMACKey, connectorCursorDomain, tenantID, a.Me.User.ID, a.Me.Audience, encoded))
			if _, _, _, e := decodeCatalogCursor(next); e != nil {
				return fail("STORAGE_UNAVAILABLE")
			}
			result.NextCursor = &next
		}
		return nil
	})
	if e != nil {
		return empty, e
	}
	return result, nil
}
