package http_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/preferences"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/google/uuid"
)

const savedViews = "/api/v1/saved-views"

func viewBody(name string, revision any) map[string]any {
	return map[string]any{"list": "mail-deliveries", "name": name, "state": map[string]any{"filter": "failed", "columns": []string{"recipient"}}, "expected_revision": revision}
}

func TestSavedViewsFollowThePerson(t *testing.T) {
	_, _, portal, admin, password := setup(t)
	login := map[string]any{"email": "admin@example.invalid", "password": password}
	admin.mutate("POST", "/api/v1/auth/login", login, 200, "LoginResponse")
	portal.mutate("POST", "/api/v1/auth/login", login, 200, "LoginResponse")
	list := func(c *client) []any {
		c.t.Helper()
		return c.request("GET", savedViews+"?list=mail-deliveries", nil, 200, "", nil)["items"].([]any)
	}
	if len(list(admin)) != 0 {
		t.Fatal("nobody has saved a view")
	}

	id := uuid.NewString()
	created := admin.mutate("PUT", savedViews+"/"+id, viewBody("Failed", 0), 200, "")
	if created["id"] != id || created["revision"] != float64(1) || created["state"].(map[string]any)["filter"] != "failed" {
		t.Fatal("created view", created)
	}
	// A retry of the same create returns what it made; different content is a conflict.
	if again := admin.mutate("PUT", savedViews+"/"+id, viewBody("Failed", 0), 200, ""); again["revision"] != float64(1) {
		t.Fatal("retried create", again)
	}
	problemCode(t, admin.mutate("PUT", savedViews+"/"+id, viewBody("Other", 0), 409, ""), "PREFERENCE_REVISION_CONFLICT")

	// The same person sees it on the other entrance, and only on its own list.
	if items := list(portal); len(items) != 1 || items[0].(map[string]any)["name"] != "Failed" {
		t.Fatal("views follow the account", items)
	}
	if items := portal.request("GET", savedViews+"?list=nodes", nil, 200, "", nil)["items"].([]any); len(items) != 0 {
		t.Fatal("another list's views", items)
	}

	updated := admin.mutate("PUT", savedViews+"/"+id, viewBody("Failures", 1), 200, "")
	if updated["revision"] != float64(2) || updated["name"] != "Failures" {
		t.Fatal("renamed view", updated)
	}
	problemCode(t, admin.mutate("PUT", savedViews+"/"+id, viewBody("Stale", 1), 409, ""), "PREFERENCE_REVISION_CONFLICT")

	other := uuid.NewString()
	problemCode(t, admin.mutate("PUT", savedViews+"/"+other, viewBody("Failures", 0), 409, ""), "SAVED_VIEW_NAME_EXISTS")
	admin.mutate("PUT", savedViews+"/"+other, viewBody("Second", 0), 200, "")
	problemCode(t, admin.mutate("PUT", savedViews+"/"+other, viewBody("Failures", 1), 409, ""), "SAVED_VIEW_NAME_EXISTS")

	problemCode(t, admin.mutate("DELETE", savedViews+"/"+id+"?revision=1", nil, 409, ""), "PREFERENCE_REVISION_CONFLICT")
	admin.mutate("DELETE", savedViews+"/"+id+"?revision=2", nil, 204, "")
	problemCode(t, admin.mutate("DELETE", savedViews+"/"+id+"?revision=2", nil, 404, ""), "RESOURCE_NOT_FOUND")
	if items := list(admin); len(items) != 1 {
		t.Fatal("one view left", items)
	}
}

func TestSavedViewsRefuseWhatTheyCannotKeep(t *testing.T) {
	policy := config.DefaultRuntimePolicy()
	policy.Rates.CSRFCount = 1000
	_, _, portal, _, password := setup(t, policy)
	portal.mutate("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "LoginResponse")
	large := map[string]any{"list": "mail-deliveries", "name": "Large", "state": map[string]any{"blob": strings.Repeat("x", preferences.MaximumViewStateBytes)}, "expected_revision": 0}
	for name, body := range map[string]map[string]any{
		"oversized":      large,
		"array state":    {"list": "mail-deliveries", "name": "A", "state": []int{1}, "expected_revision": 0},
		"null state":     {"list": "mail-deliveries", "name": "A", "state": nil, "expected_revision": 0},
		"text state":     {"list": "mail-deliveries", "name": "A", "state": "text", "expected_revision": 0},
		"bad list":       {"list": "Mail Deliveries", "name": "A", "state": map[string]any{}, "expected_revision": 0},
		"empty name":     {"list": "mail-deliveries", "name": "", "state": map[string]any{}, "expected_revision": 0},
		"spaced name":    {"list": "mail-deliveries", "name": " A ", "state": map[string]any{}, "expected_revision": 0},
		"control name":   {"list": "mail-deliveries", "name": "A\tB", "state": map[string]any{}, "expected_revision": 0},
		"long name":      {"list": "mail-deliveries", "name": strings.Repeat("n", 101), "state": map[string]any{}, "expected_revision": 0},
		"negative":       {"list": "mail-deliveries", "name": "A", "state": map[string]any{}, "expected_revision": -1},
		"foreign field":  {"list": "mail-deliveries", "name": "A", "state": map[string]any{}, "expected_revision": 0, "user_id": "other"},
		"missing fields": {"name": "A"},
	} {
		t.Run(name, func(t *testing.T) {
			portal.mutate("PUT", savedViews+"/"+uuid.NewString(), body, 400, "")
		})
	}
	portal.mutate("PUT", savedViews+"/not-a-uuid", viewBody("A", 0), 400, "")
	portal.request("GET", savedViews, nil, 400, "", nil)
	portal.request("GET", savedViews+"?list=Bad", nil, 400, "", nil)
	portal.mutate("DELETE", savedViews+"/"+uuid.NewString()+"?revision=abc", nil, 400, "")
	portal.mutate("DELETE", savedViews+"/"+uuid.NewString(), nil, 400, "")
	portal.mutate("PUT", savedViews+"/"+uuid.NewString()+"?extra=1", viewBody("A", 0), 400, "")

	for i := range preferences.MaximumViewsPerList {
		portal.mutate("PUT", savedViews+"/"+uuid.NewString(), viewBody(fmt.Sprintf("View %d", i), 0), 200, "")
	}
	problemCode(t, portal.mutate("PUT", savedViews+"/"+uuid.NewString(), viewBody("One too many", 0), 409, ""), "RESOURCE_LIMIT_REACHED")
	if items := portal.request("GET", savedViews+"?list=mail-deliveries", nil, 200, "", nil)["items"].([]any); len(items) != preferences.MaximumViewsPerList {
		t.Fatal("kept what fits", len(items))
	}
}

func TestSavedViewsBelongToOnePerson(t *testing.T) {
	s, server, _, admin, password := setup(t)
	admin.mutate("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "LoginResponse")
	id := uuid.NewString()
	admin.mutate("PUT", savedViews+"/"+id, viewBody("Mine", 0), 200, "")

	invited := admin.mutate("POST", "/api/v1/admin/invitations", map[string]any{"email": "member@example.invalid", "locale": "en"}, 201, "")
	memberPassword := fixture.Password()
	member := newClient(t, server, "portal", schemas(t))
	member.mutate("POST", "/api/v1/auth/register", map[string]any{"token": fixture.InvitationToken(t, s, invited["id"].(string)), "display_name": "Member", "password": memberPassword}, 201, "")
	member.mutate("POST", "/api/v1/auth/login", map[string]any{"email": "member@example.invalid", "password": memberPassword}, 200, "")

	if items := member.request("GET", savedViews+"?list=mail-deliveries", nil, 200, "", nil)["items"].([]any); len(items) != 0 {
		t.Fatal("saw another person's view", items)
	}
	problemCode(t, member.mutate("PUT", savedViews+"/"+id, viewBody("Taken", 1), 404, ""), "RESOURCE_NOT_FOUND")
	problemCode(t, member.mutate("DELETE", savedViews+"/"+id+"?revision=1", nil, 404, ""), "RESOURCE_NOT_FOUND")
	// Creating under someone else's ID neither overwrites it nor reveals it.
	problemCode(t, member.mutate("PUT", savedViews+"/"+id, viewBody("Taken", 0), 409, ""), "PREFERENCE_REVISION_CONFLICT")
	// Names are per person.
	member.mutate("PUT", savedViews+"/"+uuid.NewString(), viewBody("Mine", 0), 200, "")
	if items := admin.request("GET", savedViews+"?list=mail-deliveries", nil, 200, "", nil)["items"].([]any); len(items) != 1 || items[0].(map[string]any)["revision"] != float64(1) {
		t.Fatal("the owner's view stays as it was", items)
	}
}

func TestConcurrentSavedViewsKeepNamesUnique(t *testing.T) {
	_, _, _, admin, password := setup(t)
	admin.mutate("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "LoginResponse")
	admin.refresh()
	const writers = 6
	var wg sync.WaitGroup
	var mu sync.Mutex
	codes := map[int]int{}
	for range writers {
		wg.Go(func() {
			raw, _ := json.Marshal(viewBody("Same name", 0))
			r, e := http.NewRequest("PUT", admin.s.URL+savedViews+"/"+uuid.NewString(), bytes.NewReader(raw))
			if e != nil {
				return
			}
			r.Host = "admin.example.invalid"
			r.Header.Set("Origin", "https://admin.example.invalid")
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-CSRF-Token", admin.csrf)
			for _, cookie := range admin.cookies {
				r.AddCookie(cookie)
			}
			response, e := admin.http.Do(r)
			if e != nil {
				return
			}
			response.Body.Close()
			mu.Lock()
			defer mu.Unlock()
			codes[response.StatusCode]++
		})
	}
	wg.Wait()
	if codes[200] != 1 || codes[409] != writers-1 {
		t.Fatal("one writer wins the name "+strconv.Itoa(writers), codes)
	}
}

func TestMailDeliveriesFilterByRecipient(t *testing.T) {
	_, _, _, admin, password := setup(t)
	admin.mutate("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "LoginResponse")
	for _, email := range []string{"grace@example.invalid", "gregory@example.invalid", "ada_b@example.invalid", "adaXb@example.invalid"} {
		admin.mutate("POST", "/api/v1/admin/invitations", map[string]any{"email": email, "locale": "en"}, 201, "")
	}
	count := func(prefix string) int {
		t.Helper()
		return len(admin.request("GET", "/api/v1/admin/mail-deliveries?recipient_prefix="+prefix, nil, 200, "MailListResponse", nil)["items"].([]any))
	}
	if count("gr") != 2 || count("GRE") != 1 || count("nobody") != 0 {
		t.Fatal("prefix matching", count("gr"), count("GRE"))
	}
	if count("ada_") != 1 {
		t.Fatal("a wildcard in the text is matched literally", count("ada_"))
	}
	admin.request("GET", "/api/v1/admin/mail-deliveries?recipient_prefix=", nil, 400, "", nil)
	admin.request("GET", "/api/v1/admin/mail-deliveries?recipient_prefix=%01", nil, 400, "", nil)
}
