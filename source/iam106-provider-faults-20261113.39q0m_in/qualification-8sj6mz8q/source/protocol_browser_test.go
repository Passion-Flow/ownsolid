package boundary

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
)

type pendingLogin struct{ id, csrf string }

func (f *protocolFixture) pendingLogin(t *testing.T, client *http.Client) pendingLogin {
	t.Helper()
	response := f.request(t, client, "GET", f.rpOrigin+"/start", nil, "")
	authorize := response.Header.Get("Location")
	responseBody(t, response, 302)
	response = f.request(t, client, "GET", authorize, nil, "")
	login := response.Header.Get("Location")
	responseBody(t, response, 302)
	target, err := url.Parse(login)
	if err != nil || target.Host != strings.TrimPrefix(f.opOrigin, "https://") || !validRequestID(target.Query().Get("id")) {
		t.Fatal("private pending login destination invalid")
	}
	response = f.request(t, client, "GET", login, nil, "")
	match := csrfField.FindSubmatch(responseBody(t, response, 200))
	if len(match) != 2 {
		t.Fatal("private pending login CSRF absent")
	}
	f.evidence.record(string(match[1]))
	for _, origin := range []string{f.opOrigin, f.rpOrigin} {
		u, _ := url.Parse(origin)
		for _, cookie := range client.Jar.Cookies(u) {
			f.evidence.record(cookie.Value)
		}
	}
	return pendingLogin{id: target.Query().Get("id"), csrf: string(match[1])}
}
func TestLiveOPMandatoryBrowserCSRFAndOrigin(t *testing.T) {
	f := newProtocolFixture(t, nil)
	for _, name := range []string{"csrf", "missing_csrf", "missing_origin", "foreign_origin", "wrong_password", "foreign_browser", "duplicate_csrf", "tenant_query"} {
		t.Run(name, func(t *testing.T) {
			pending := f.pendingLogin(t, f.browser)
			body := url.Values{"id": {pending.id}, "csrf": {pending.csrf}, "username": {f.config.LoginName}, "password": {f.password}}
			origin, target, client := f.opOrigin, f.opOrigin+"/login", f.browser
			expected := 403
			switch name {
			case "csrf":
				body.Set("csrf", random(t))
			case "missing_csrf":
				body.Del("csrf")
				expected = 400
			case "missing_origin":
				origin = ""
			case "foreign_origin":
				origin = f.rpOrigin
			case "wrong_password":
				body.Set("password", random(t))
				expected = 401
			case "foreign_browser":
				jar, err := cookiejar.New(nil)
				if err != nil {
					t.Fatal("private foreign browser unavailable")
				}
				other := *f.browser
				other.Jar = jar
				f.pendingLogin(t, &other)
				client = &other
			case "duplicate_csrf":
				body.Add("csrf", pending.csrf)
				expected = 400
			case "tenant_query":
				target += "?tenant_id=" + random(t)
				expected = 400
			}
			before := f.scalar(t, `SELECT count(*) FROM op_fixture_sessions`)
			response := f.request(t, client, "POST", target, body, origin)
			var refusal map[string]json.RawMessage
			if json.Unmarshal(responseBody(t, response, expected), &refusal) != nil {
				t.Fatal("private browser refusal invalid")
			}
			noTokenFields(t, refusal)
			if f.scalar(t, `SELECT count(*) FROM op_fixture_sessions`) != before || f.scalar(t, `SELECT count(*) FROM op_fixture_requests WHERE id=$1 AND approved_at IS NOT NULL`, pending.id) != 0 {
				t.Fatal("browser refusal changed source session or approval")
			}
			response = f.request(t, f.browser, "POST", f.opOrigin+"/login", url.Values{"id": {pending.id}, "csrf": {pending.csrf}, "username": {f.config.LoginName}, "password": {f.password}}, f.opOrigin)
			responseBody(t, response, 303)
			if f.scalar(t, `SELECT count(*) FROM op_fixture_requests WHERE id=$1 AND approved_at IS NOT NULL`, pending.id) != 1 {
				t.Fatal("explicit browser repair did not approve request")
			}
		})
	}
}

func TestLiveOPSourceCallbackCookieBinding(t *testing.T) {
	f := newProtocolFixture(t, nil)
	pending := f.pendingLogin(t, f.browser)
	response := f.request(t, f.browser, "POST", f.opOrigin+"/login", url.Values{"id": {pending.id}, "csrf": {pending.csrf}, "username": {f.config.LoginName}, "password": {f.password}}, f.opOrigin)
	destination := response.Header.Get("Location")
	responseBody(t, response, 303)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal("private source browser unavailable")
	}
	u, _ := url.Parse(f.opOrigin)
	for _, cookie := range f.browser.Jar.Cookies(u) {
		if cookie.Name == browserCookie {
			cookie.Path = "/"
			cookie.Secure = true
			jar.SetCookies(u, []*http.Cookie{cookie})
		}
	}
	other := *f.browser
	other.Jar = jar
	response = f.request(t, &other, "GET", destination, nil, "")
	if response.Header.Get("Location") != "" {
		t.Fatal("missing source session opened callback")
	}
	var refusal map[string]json.RawMessage
	if json.Unmarshal(responseBody(t, response, 400), &refusal) != nil {
		t.Fatal("source refusal invalid")
	}
	noTokenFields(t, refusal)
	if f.scalar(t, `SELECT count(*) FROM op_fixture_requests WHERE id=$1 AND code_digest IS NOT NULL`, pending.id) != 0 {
		t.Fatal("missing source cookie issued code")
	}
	response = f.request(t, f.browser, "GET", destination, nil, "")
	if response.Header.Get("Location") == "" {
		t.Fatal("source cookie repair failed")
	}
	responseBody(t, response, 302)
}
