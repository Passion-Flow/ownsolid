// Package httpfixture drives real TLS HTTP handlers with synthetic sessions and canonical response schemas.
package httpfixture

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
)

type Client struct {
	t        *testing.T
	client   *http.Client
	origin   string
	audience string
	cookies  map[string]*http.Cookie
	CSRF     string
	Headers  map[string]string
	schemas  map[string]*jsonschema.Schema
}

func (c *Client) Call(method, path string, body any, status int, schema string) map[string]any {
	c.t.Helper()
	var b []byte
	if raw, ok := body.([]byte); ok {
		b = raw
	} else if body != nil {
		b, _ = json.Marshal(body)
	}
	r, e := http.NewRequestWithContext(c.t.Context(), method, c.origin+path, bytes.NewReader(b))
	if e != nil {
		c.t.Fatal("HTTP_FIXTURE_REQUEST_INVALID")
	}
	r.Header.Set("Origin", c.origin)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if c.CSRF != "" {
		r.Header.Set("X-CSRF-Token", c.CSRF)
	}
	for name, value := range c.Headers {
		r.Header.Set(name, value)
	}
	for _, cookie := range c.cookies {
		r.AddCookie(cookie)
	}
	response, e := c.client.Do(r)
	if e != nil {
		c.t.Fatal(e)
	}
	defer response.Body.Close()
	b, e = io.ReadAll(response.Body)
	if e != nil {
		c.t.Fatal(e)
	}
	if response.StatusCode != status {
		var p struct{ Code string }
		json.Unmarshal(b, &p)
		c.t.Fatalf("%s %s: expected %d, observed %d (%s)", method, path, status, response.StatusCode, p.Code)
	}
	if response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("X-Request-ID") == "" {
		c.t.Fatal("missing security headers")
	}
	for _, cookie := range response.Cookies() {
		if cookie.MaxAge < 0 {
			delete(c.cookies, cookie.Name)
		} else {
			c.cookies[cookie.Name] = cookie
		}
	}
	if status == 204 {
		if len(b) != 0 {
			c.t.Fatal("unexpected 204 body")
		}
		return nil
	}
	if schema == "dataset-file" {
		digest := sha256.Sum256(b)
		if response.Header.Get("Content-Type") != "application/x-ndjson" && response.Header.Get("Content-Type") != "application/json" || !strings.HasPrefix(response.Header.Get("Content-Disposition"), "attachment; filename=dataset-") || response.Header.Get("X-Artifact-SHA256") != hex.EncodeToString(digest[:]) || response.Header.Get("Content-Length") != strconv.Itoa(len(b)) {
			c.t.Fatal("unsafe or inconsistent dataset export headers")
		}
		return map[string]any{"text": string(b), "sha256": hex.EncodeToString(digest[:])}
	}
	if schema == "text" {
		if response.Header.Get("Content-Type") != "text/plain; charset=utf-8" || !strings.HasPrefix(response.Header.Get("Content-Disposition"), "attachment; filename=\"workspace-note-") {
			c.t.Fatal("unsafe export headers")
		}
		return map[string]any{"text": string(b)}
	}
	var result map[string]any
	if json.Unmarshal(b, &result) != nil {
		c.t.Fatal("invalid JSON")
	}
	if schema != "" {
		if e = c.schemas[schema].Validate(result); e != nil {
			c.t.Fatalf("response violates schema %s", schema)
		}
	}
	if status >= 400 {
		return result
	}
	return result["data"].(map[string]any)
}
func (c *Client) Write(method, path string, body any, status int, schema string) map[string]any {
	c.t.Helper()
	c.CSRF = c.Call("GET", "/api/v1/auth/csrf", nil, 200, "")["token"].(string)
	return c.Call(method, path, body, status, schema)
}
func Schemas(t *testing.T, path string) map[string]*jsonschema.Schema {
	t.Helper()
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var spec map[string]any
	if json.Unmarshal(b, &spec) != nil {
		t.Fatal("invalid contract")
	}
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	if e = c.AddResource("https://projectone.invalid/workspaces", map[string]any{"$schema": "https://json-schema.org/draft/2020-12/schema", "components": spec["components"]}); e != nil {
		t.Fatal(e)
	}
	out := map[string]*jsonschema.Schema{}
	for name := range spec["components"].(map[string]any)["schemas"].(map[string]any) {
		out[name], e = c.Compile("https://projectone.invalid/workspaces#/components/schemas/" + name)
		if e != nil {
			t.Fatal(e)
		}
	}
	return out
}

func New(t *testing.T, server *httptest.Server, audience string, schemas map[string]*jsonschema.Schema) *Client {
	t.Helper()
	origin, e := fixtureOrigin(audience)
	if e != nil {
		t.Fatal("HTTP_FIXTURE_AUDIENCE_INVALID")
	}
	client, e := fixtureTLSClient(server, []string{origin})
	if e != nil {
		t.Fatal("HTTP_FIXTURE_CLIENT_CONFIGURATION_INVALID")
	}
	t.Cleanup(client.CloseIdleConnections)
	return &Client{t: t, client: client, origin: origin, audience: audience, cookies: map[string]*http.Cookie{}, schemas: schemas}
}
