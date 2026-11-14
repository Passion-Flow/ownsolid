//go:build repository_integration

package http_test

import (
	"context"
	"net/url"
	"strconv"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func TestActualSSOCatalogTLSConfiguredDefaultAndCursor(t *testing.T) {
	admin, portal, password, accounts := configuredHTTP(t, func(policy *config.RuntimePolicy) {
		policy.UI.PageSize = 7
	})
	admin.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "")
	for i := range 9 {
		admin.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
		admin.Write("POST", "/api/v1/admin/sso/connectors", map[string]any{"id": uuid.NewString(), "name": "Configured catalog \u4e16\u754c " + strconv.Itoa(i), "provider": "gitea", "origin": "https://provider.example.invalid", "client_id": uuid.NewString(), "client_secret": security.Token(), "enabled": true, "reason": "Synthetic configured HTTP catalog"}, 201, "SSOConnectorResponse")
	}
	var before, after int
	if accounts.DB.QueryRow(context.Background(), "SELECT COUNT(*) FROM audit_events").Scan(&before) != nil {
		t.Fatal("actual isolated audit inventory")
	}
	first := admin.Call("GET", "/api/v1/admin/sso/connectors", nil, 200, "SSOConnectorPageResponse")
	items, ok := first["items"].([]any)
	cursor, valid := first["next_cursor"].(string)
	if !ok || len(items) != 7 || !valid || cursor == "" {
		t.Fatal("configured HTTP default must return7 of9 rows and a real cursor")
	}
	second := admin.Call("GET", "/api/v1/admin/sso/connectors?cursor="+url.QueryEscape(cursor), nil, 200, "SSOConnectorPageResponse")
	tail, ok := second["items"].([]any)
	if !ok || len(tail) != 2 || second["next_cursor"] != nil {
		t.Fatal("configured cursor page must return the remaining2 rows")
	}
	seen := map[string]bool{}
	for _, item := range append(items, tail...) {
		row := item.(map[string]any)
		id := row["id"].(string)
		if seen[id] {
			t.Fatal("configured HTTP page duplicated a connector")
		}
		seen[id] = true
	}
	portal.Call("GET", "/api/v1/auth/sso/connectors", nil, 200, "PublicSSOConnectorListResponse")
	if accounts.DB.QueryRow(context.Background(), "SELECT COUNT(*) FROM audit_events").Scan(&after) != nil || after != before {
		t.Fatal("catalog reads persisted an audit mutation")
	}
	admin.Call("GET", "/api/v1/admin/sso/connectors?limit=7&limit=8", nil, 400, "Problem")
	portal.Call("GET", "/api/v1/admin/sso/connectors", nil, 404, "Problem")
}
