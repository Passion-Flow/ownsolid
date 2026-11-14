package pagination

import (
	"encoding/base64"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestConfiguredPaginationDefaultAndLegacyCompatibility(t *testing.T) {
	values := url.Values{}
	limit, _, scope, e := ParseWithDefault(values, "synthetic scope", 7)
	if e != nil || limit != 7 || len(values) != 0 {
		t.Fatal("configured default mutated query", e)
	}
	stamp := time.Date(2026, 10, 19, 3, 4, 5, 123456000, time.UTC)
	id := uuid.NewString()
	cursor := Encode(stamp, id, scope)
	for _, v := range []int{1, 7, MaximumLimit} {
		query := url.Values{"cursor": {*cursor}, "limit": {strconv.Itoa(v)}}
		n, c, _, e := ParseWithDefault(query, "synthetic scope", 7)
		if e != nil || n != v || c.ID != id || !c.Time.Equal(stamp) {
			t.Fatal("configured seek/limit", e)
		}
	}
	n, old, _, e := Parse(url.Values{"cursor": {*cursor}}, "synthetic scope")
	if e != nil || n != LegacyDefaultLimit || old.ID != id {
		t.Fatal("legacy behavior changed", e)
	}
}
func TestConfiguredPaginationRejectsMalformedCursors(t *testing.T) {
	_, _, scope, _ := ParseWithDefault(nil, "scope", 7)
	id := uuid.NewString()
	valid := `{"time":"2026-10-19T03:04:05Z","id":"` + id + `","scope":"` + scope + `"}`
	for _, raw := range []string{
		`null`, valid + ` {}`, strings.Replace(valid, `"id":`, `"ID":`, 1), strings.Replace(valid, `"id":`, `"id":"`+id+`","id":`, 1), strings.Replace(valid, `}`, `,"extra":true}`, 1),
		strings.Replace(valid, id, uuid.Nil.String(), 1), strings.Replace(valid, id, strings.ToUpper(id), 1), strings.Replace(valid, id, "urn:uuid:"+id, 1),
		strings.Replace(valid, "2026-10-19T03:04:05Z", "0999-12-31T23:59:59Z", 1), strings.Replace(valid, "2026-10-19T03:04:05Z", "1000-01-01T00:00:00+14:00", 1), strings.Replace(valid, `"time":"2026-10-19T03:04:05Z"`, `"time":null`, 1),
	} {
		value := base64.RawURLEncoding.EncodeToString([]byte(raw))
		if _, _, _, e := ParseWithDefault(url.Values{"cursor": {value}}, "scope", 7); e == nil || e.Error() != "INVALID_CURSOR" {
			t.Fatalf("invalid cursor accepted: %v", e)
		}
	}
}
func TestConfiguredPaginationScopeAndCanonicalBoundary(t *testing.T) {
	_, _, scope, _ := ParseWithDefault(nil, "kind:tenant:workspace:portal:actor", 7)
	cursor := Encode(time.Date(1000, 1, 1, 0, 0, 0, 0, time.UTC), uuid.NewString(), scope)
	if _, _, _, e := ParseWithDefault(url.Values{"cursor": {*cursor}}, "kind:tenant:workspace:portal:actor", 7); e != nil {
		t.Fatal("supported minimum time", e)
	}
	for _, other := range []string{"other:tenant:workspace:portal:actor", "kind:foreign:workspace:portal:actor", "kind:tenant:other:portal:actor", "kind:tenant:workspace:admin:actor", "kind:tenant:workspace:portal:other"} {
		if _, _, _, e := ParseWithDefault(url.Values{"cursor": {*cursor}}, other, 7); e == nil || e.Error() != "INVALID_CURSOR" {
			t.Fatal("cross-context cursor accepted", e)
		}
	}
	canonical := *cursor
	raw, _ := base64.RawURLEncoding.DecodeString(canonical)
	if len(raw)%3 == 0 {
		canonical = *Encode(time.Date(1000, 1, 1, 0, 0, 0, 1000000, time.UTC), uuid.NewString(), scope)
	}
	alphabet := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	last := strings.IndexByte(alphabet, canonical[len(canonical)-1])
	noncanonical := canonical[:len(canonical)-1] + string(alphabet[last+1])
	if _, _, _, e := ParseWithDefault(url.Values{"cursor": {noncanonical}}, "kind:tenant:workspace:portal:actor", 7); e == nil || e.Error() != "INVALID_CURSOR" {
		t.Fatal("unused Base64 bits accepted", e)
	}
	for _, bad := range []string{*cursor + "=", strings.Repeat("a", MaximumCursorBytes+1), "not+url/base64"} {
		if _, _, _, e := ParseWithDefault(url.Values{"cursor": {bad}}, "kind:tenant:workspace:portal:actor", 7); e == nil || e.Error() != "INVALID_CURSOR" {
			t.Fatal(e)
		}
	}
}
func TestConfiguredPaginationInvalidPolicyAndQueries(t *testing.T) {
	for _, n := range []int{0, -1, MaximumLimit + 1} {
		if _, _, _, e := ParseWithDefault(nil, "scope", n); e == nil || e.Error() != "PAGINATION_CONFIGURATION_INVALID" {
			t.Fatal(e)
		}
	}
	for _, q := range []url.Values{{"limit": {""}}, {"limit": {"0"}}, {"limit": {"101"}}, {"limit": {"1", "2"}}, {"cursor": {"a", "b"}}, {"cursor": {}}, {"tenant_id": {"foreign"}}} {
		if _, _, _, e := ParseWithDefault(q, "scope", 7); e == nil || e.Error() != "VALIDATION_FAILED" {
			t.Fatal(e)
		}
	}
	if _, _, _, e := ParseWithDefault(url.Values{"cursor": {""}}, "scope", 7); e == nil || e.Error() != "INVALID_CURSOR" {
		t.Fatal(e)
	}
}
