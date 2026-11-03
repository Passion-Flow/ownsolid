package httpfixture

import (
	"io"
	"net/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
)

// Redirect observes the real first response without following a callback URL.
// Sensitive code/state and response bodies must never enter test diagnostics.
func (c *Client) Redirect(path string) (int, string, []*http.Cookie) {
	c.t.Helper()
	r, e := http.NewRequestWithContext(c.t.Context(), http.MethodGet, c.origin+path, nil)
	if e != nil {
		c.t.Fatal("callback request construction failed")
	}
	for _, cookie := range c.cookies {
		r.AddCookie(cookie)
	}
	client := *c.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, e := client.Do(r)
	if e != nil {
		c.t.Fatal("actual TLS callback request failed")
	}
	defer response.Body.Close()
	if _, e := io.Copy(io.Discard, io.LimitReader(response.Body, httpserver.MaxJSONBytes+1)); e != nil {
		c.t.Fatal("bounded callback response read failed")
	}
	if response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("X-Request-ID") == "" || response.Header.Get("Referrer-Policy") != "no-referrer" {
		c.t.Fatal("callback security headers absent")
	}
	cookies := response.Cookies()
	for _, cookie := range cookies {
		if cookie.MaxAge < 0 {
			delete(c.cookies, cookie.Name)
		} else {
			c.cookies[cookie.Name] = cookie
		}
	}
	return response.StatusCode, response.Header.Get("Location"), cookies
}
