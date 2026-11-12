package http_test

import (
	"net/http"
	"testing"
)

func recoveryCallback(t *testing.T, c *client, path, want string, issued bool) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, c.s.URL+path, nil)
	if err != nil {
		t.Fatal("actual callback request invalid")
	}
	req.Host = c.audience + ".example.invalid"
	for _, cookie := range c.cookies {
		req.AddCookie(cookie)
	}
	httpClient := *c.http
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := httpClient.Do(req)
	if err != nil {
		t.Fatal("actual verified callback failed")
	}
	defer response.Body.Close()
	if response.StatusCode != 303 || response.Header.Get("Location") != want || response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("Referrer-Policy") != "no-referrer" || response.Header.Get("X-Request-ID") == "" || response.TLS == nil || len(response.TLS.VerifiedChains) == 0 {
		t.Fatal("actual callback outcome or security headers invalid")
	}
	session := false
	for _, cookie := range response.Cookies() {
		if !cookie.Secure || !cookie.HttpOnly || cookie.Domain != "" || cookie.Path != "/" {
			t.Fatal("actual callback cookie boundary invalid")
		}
		if cookie.Name == "__Host-"+c.audience+"_session" && cookie.Value != "" && cookie.MaxAge > 0 {
			session = true
		}
		if cookie.MaxAge < 0 {
			delete(c.cookies, cookie.Name)
		} else {
			c.cookies[cookie.Name] = cookie
		}
	}
	if session != issued {
		t.Fatal("actual callback session issuance changed")
	}
}
