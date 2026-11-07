package identity

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func TestListInputsAreBoundedBeforeStorage(t *testing.T) {
	policy := config.DefaultRuntimePolicy()
	policy.UI.PageSize = 7
	now := time.Now().UTC()
	p, e := makeListPlan("users", url.Values{}, policy, now)
	if e != nil || p.limit != 7 {
		t.Fatal("shared nondefault UI policy ignored", e)
	}
	for _, query := range []url.Values{{"limit": {"101"}}, {"limit": {"0"}}, {"limit": {"NaN"}}, {"limit": {"1", "2"}}, {"status": {"active|disabled"}}, {"email_prefix": {""}}, {"email_prefix": {"\x7f"}}, {"workspace_id": {uuid.NewString()}}, {"cursor": {"invalid"}}, {"cursor": {strings.Repeat("x", maximumListCursorBytes+1)}}} {
		if _, e := makeListPlan("users", query, policy, now); e == nil {
			t.Fatal("invalid filter/cursor accepted")
		}
	}
	for _, query := range []url.Values{{"from": {"0001-01-01T00:00:00Z"}, "to": {"0001-01-02T00:00:00Z"}}, {"from": {"2030-02-02T00:00:00Z"}, "to": {"2030-01-01T00:00:00Z"}}, {"request_id": {"invalid"}}} {
		if _, e := makeListPlan("audit-events", query, policy, now); e == nil {
			t.Fatal("invalid audit range accepted")
		}
	}
	for _, raw := range []string{`{"id":"one","id":"two"}`, `{"unknown":true}`, `[]`, `{} {}`} {
		if _, e := decodeListCursor([]byte(raw)); code(e) != "INVALID_CURSOR" {
			t.Fatal("invalid cursor metadata accepted", e)
		}
	}
	if _, e := makeListPlan("unknown", url.Values{}, policy, now); code(e) != "RESOURCE_NOT_FOUND" {
		t.Fatal(e)
	}
	if _, e := (&Service{}).ListForActor(context.Background(), Auth{}, "users", nil); code(e) != "AUTH_SESSION_EXPIRED" {
		t.Fatal(e)
	}
}

func TestListAuditFiltersByFamilyAndPerson(t *testing.T) {
	policy := config.DefaultRuntimePolicy()
	now := time.Now().UTC()
	person := uuid.NewString()
	p, e := makeListPlan("audit-events", url.Values{"category": {"license"}, "actor_user_id": {person}}, policy, now)
	if e != nil {
		t.Fatal(e)
	}
	joined := strings.Join(p.pgWhere, " AND ")
	if !strings.Contains(joined, "a.action LIKE $") || !strings.Contains(joined, "a.actor_user_id=$") {
		t.Fatal(joined)
	}
	if !slices.Contains(p.pgArgs, any("license.%")) || !slices.Contains(p.pgArgs, any(person)) {
		t.Fatal(p.pgArgs)
	}
	for name, values := range map[string][]string{
		"category":      {"License", "license.imported", "%", "a_b", strings.Repeat("a", 33)},
		"actor_user_id": {"someone", "1"},
	} {
		for _, value := range values {
			if _, e := makeListPlan("audit-events", url.Values{name: {value}}, policy, now); code(e) != "VALIDATION_FAILED" {
				t.Fatal(name, value, e)
			}
		}
	}
}

func TestListAuditCursorPinsWindowAndRejectsFilterDrift(t *testing.T) {
	policy := config.DefaultRuntimePolicy()
	now := time.Now().UTC()
	p, e := makeListPlan("audit-events", url.Values{}, policy, now)
	if e != nil {
		t.Fatal(e)
	}
	encode := func(value cursor) string {
		raw, e := json.Marshal(value)
		if e != nil {
			t.Fatal(e)
		}
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	c := cursor{Time: now.Add(-time.Hour), ID: uuid.NewString(), Filter: p.filter, WindowFrom: p.windowFrom, WindowTo: p.windowTo}
	q := url.Values{"cursor": {encode(c)}}
	next, e := makeListPlan("audit-events", q, policy, now.Add(time.Hour))
	if e != nil || !next.windowFrom.Equal(*p.windowFrom) || !next.windowTo.Equal(*p.windowTo) {
		t.Fatal("audit window moved across pages", e)
	}
	q.Set("action", "auth.logout")
	if _, e := makeListPlan("audit-events", q, policy, now); code(e) != "INVALID_CURSOR" {
		t.Fatal(e)
	}
	q.Del("action")
	q.Set("category", "license")
	if _, e := makeListPlan("audit-events", q, policy, now); code(e) != "INVALID_CURSOR" {
		t.Fatal("a new family kept the old cursor", e)
	}
	q.Del("category")
	q.Set("actor_user_id", uuid.NewString())
	if _, e := makeListPlan("audit-events", q, policy, now); code(e) != "INVALID_CURSOR" {
		t.Fatal("a new person kept the old cursor", e)
	}
	c.WindowTo = nil
	if _, e := makeListPlan("audit-events", url.Values{"cursor": {encode(c)}}, policy, now); code(e) != "INVALID_CURSOR" {
		t.Fatal(e)
	}
	c.WindowFrom = nil
	if _, e := makeListPlan("audit-events", url.Values{"cursor": {encode(c)}}, policy, now); code(e) != "INVALID_CURSOR" {
		t.Fatal("old implicit-window cursor did not require restart", e)
	}
}

func TestAuditTargetFilterAcceptsOnlyAnIdentifierAndBindsItsCursor(t *testing.T) {
	policy := config.DefaultRuntimePolicy()
	now := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)
	target := "6f1c7a1e-0000-4000-8000-000000000031"
	first, e := makeListPlan("audit-events", url.Values{"target_id": {target}}, policy, now)
	if e != nil {
		t.Fatal(e)
	}
	other, e := makeListPlan("audit-events", url.Values{"target_id": {"6f1c7a1e-0000-4000-8000-000000000032"}}, policy, now)
	if e != nil || first.filter == other.filter {
		t.Fatal("a cursor belongs to its target", e)
	}
	if _, e = makeListPlan("audit-events", url.Values{"target_id": {"not-an-id"}}, policy, now); e == nil || e.Error() != "VALIDATION_FAILED" {
		t.Fatal(e)
	}
}
