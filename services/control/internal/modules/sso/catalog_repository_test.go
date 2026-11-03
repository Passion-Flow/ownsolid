package sso

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

// This helper supplies a format-only MAC double. Authentication/scope MACs are
// checked separately against actual current-tenant repository transactions.
func catalogFormatToken(payload []byte) string {
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(make([]byte, sha256.Size))
}

func TestSSOCatalogDisabledRecoveryAndMissingStorage(t *testing.T) {
	s := &Service{}
	items, e := s.PublicConnectors(context.Background())
	if e != nil || items == nil || len(items) != 0 {
		t.Fatal("disabled recovery must not open storage or assets", e)
	}
	s.enabled = true
	if items, e = s.PublicConnectors(context.Background()); e == nil || e.Error() != "CONFIG_SSO_POLICY_INVALID" || len(items) != 0 {
		t.Fatal("invalid policy must fail before missing storage", e)
	}
	s.policy = config.DefaultSSOPolicy()
	if items, e = s.PublicConnectors(context.Background()); e == nil || e.Error() != "SSO_CONFIGURATION_REQUIRED" || len(items) != 0 {
		t.Fatal("missing owner must fail safely", e)
	}
	var missing *Service
	if _, e = missing.PublicConnectors(context.Background()); e == nil || e.Error() != "SSO_CONFIGURATION_REQUIRED" {
		t.Fatal("nil component must not panic", e)
	}
}

func TestSSOCatalogInputAndAuthorityBeforeStorage(t *testing.T) {
	s := &Service{Identity: &identity.Service{}}
	a := identity.Auth{}
	a.Me.Audience, a.Me.User.IsPlatformAdmin = "admin", true
	for _, limit := range []int{0, -1, 101} {
		page, e := s.ListConnectors(context.Background(), a, limit, "")
		if e == nil || e.Error() != "VALIDATION_FAILED" || len(page.Items) != 0 || page.NextCursor != nil {
			t.Fatal("invalid page size must fail before storage", e)
		}
	}
	for _, raw := range []string{"sensitive-customer-value", strings.Repeat("x", maximumSSOCatalogCursorBytes+1), "a.b.c", "."} {
		page, e := s.ListConnectors(context.Background(), a, 1, raw)
		if e == nil || e.Error() != "VALIDATION_FAILED" || len(page.Items) != 0 || page.NextCursor != nil {
			t.Fatal("invalid cursor must fail safely before storage", e)
		}
	}
	for _, audience := range []string{"portal", "operator", "admin"} {
		bad := a
		bad.Me.Audience, bad.Me.User.IsPlatformAdmin = audience, false
		page, e := s.ListConnectors(context.Background(), bad, 1, "")
		if e == nil || e.Error() != "ACCESS_DENIED" || len(page.Items) != 0 || page.NextCursor != nil {
			t.Fatal("nonAdmin must fail before storage", e)
		}
	}
	page, e := s.ListConnectors(context.Background(), a, 1, "")
	if e == nil || e.Error() != "IDENTITY_STORAGE_REQUIRED" || len(page.Items) != 0 || page.NextCursor != nil {
		t.Fatal("missing selected storage must return no partial page", e)
	}
}

func TestSSOCatalogCanonicalCursorWireAndDatetimeBoundaries(t *testing.T) {
	id := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	for _, stamp := range []time.Time{time.Date(1000, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 19, 1, 2, 3, 123456000, time.UTC), time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC)} {
		raw, _ := json.Marshal(pageCursor{stamp, id})
		cursor, encoded, mac, e := decodeCatalogCursor(catalogFormatToken(raw))
		if e != nil || cursor.ID != id || !cursor.CreatedAt.Equal(stamp) || encoded != base64.RawURLEncoding.EncodeToString(raw) || !bytes.Equal(mac, make([]byte, sha256.Size)) {
			t.Fatal("canonical existing wire or DATETIME boundary changed", e)
		}
	}
	valid, _ := json.Marshal(pageCursor{time.Date(2026, 10, 19, 1, 2, 3, 0, time.UTC), id})
	malformed := []string{
		`{"created_at":"2026-10-19T01:02:03Z","id":"` + id + `","extra":true}`,
		`{"created_at":"2026-10-19T01:02:03Z","id":"` + id + `","id":"` + id + `"}`,
		`{"Created_at":"2026-10-19T01:02:03Z","id":"` + id + `"}`,
		`{"id":"` + id + `","created_at":"2026-10-19T01:02:03Z"}`,
		strings.Replace(string(valid), id, strings.ToUpper(id), 1),
		strings.Replace(string(valid), id, uuid.Nil.String(), 1),
		strings.Replace(string(valid), "2026-", "0999-", 1),
		strings.Replace(string(valid), "03Z", "03.000000001Z", 1),
		strings.Replace(string(valid), "03Z", "03+01:00", 1),
		`{"created_at":null,"id":"` + id + `"}`,
	}
	for _, raw := range malformed {
		if _, _, _, e := decodeCatalogCursor(catalogFormatToken([]byte(raw))); e == nil || e.Error() != "VALIDATION_FAILED" {
			t.Fatal("noncanonical cursor accepted")
		}
	}
	token := catalogFormatToken(valid)
	for _, raw := range []string{token + "=", strings.Replace(token, ".", "\n.", 1), strings.Split(token, ".")[0] + "."} {
		if _, _, _, e := decodeCatalogCursor(raw); e == nil {
			t.Fatal("base64 alias or missing MAC accepted")
		}
	}
}
