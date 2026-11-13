package http

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/sso"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"github.com/google/uuid"
)

func samlCompletionHTTPRequest(t *testing.T) (*http.Request, sso.SAMLCompleteInput, httpserver.CookiePolicy) {
	t.Helper()
	input := sso.SAMLCompleteInput{ConnectorID: uuid.NewString(), Audience: "portal", FlowID: uuid.NewString(), Continuation: security.Token(), PreviousSession: security.Token()}
	path := "/api/v1/sso/saml/connectors/" + input.ConnectorID + "/flows/" + input.FlowID + "/complete"
	r := httptest.NewRequest(http.MethodPost, "https://portal.example.invalid"+path, strings.NewReader("{}"))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", input.FlowID)
	cookies, e := httpserver.NewCookiePolicy(config.TransportDirectHTTPS)
	if e != nil {
		t.Fatal(e)
	}
	r.AddCookie(&http.Cookie{Name: cookies.Name("portal", httpserver.SessionCookie), Value: input.PreviousSession})
	r.AddCookie(&http.Cookie{Name: cookies.Name("portal", httpserver.SSOFlowCookie), Value: input.Continuation})
	return r, input, cookies
}

func TestSAMLCompletionHTTPRoute(t *testing.T) {
	r, input, _ := samlCompletionHTTPRequest(t)
	if !SAMLCompletionPath(r.URL.Path) {
		t.Fatal("canonical first-party completion path was not selected")
	}
	for _, path := range []string{
		r.URL.Path + "/", "/" + r.URL.Path, strings.Replace(r.URL.Path, "/flows/", "//flows/", 1),
		strings.Replace(r.URL.Path, input.ConnectorID, "invalid", 1), strings.Replace(r.URL.Path, input.FlowID, uuid.Nil.String(), 1),
		strings.Replace(r.URL.Path, "/complete", "/acs", 1), "/api/v1/sso/saml/connectors/" + input.ConnectorID + "/acs",
		"/api/v1/auth/sso/" + input.ConnectorID + "/callback", "/api/v1/sso/saml/connectors",
	} {
		if SAMLCompletionPath(path) {
			t.Fatal("another or noncanonical path selected SAML completion")
		}
	}
}

func TestSAMLCompletionHTTPBrowserInput(t *testing.T) {
	r, expected, cookies := samlCompletionHTTPRequest(t)
	input, e := samlCompletionBrowserInput(r, "portal", cookies)
	if e != nil || input != expected {
		t.Fatal("exact protected browser input was not retained", e)
	}
}

func TestSAMLCompletionHTTPInputRefusals(t *testing.T) {
	cases := []struct {
		name   string
		change func(*http.Request, sso.SAMLCompleteInput, httpserver.CookiePolicy)
	}{
		{"tenant_query", func(r *http.Request, _ sso.SAMLCompleteInput, _ httpserver.CookiePolicy) {
			r.URL.RawQuery = "tenant_id=" + uuid.NewString()
		}},
		{"empty_query_alias", func(r *http.Request, _ sso.SAMLCompleteInput, _ httpserver.CookiePolicy) { r.URL.ForceQuery = true }},
		{"encoded_path_alias", func(r *http.Request, _ sso.SAMLCompleteInput, _ httpserver.CookiePolicy) { r.URL.RawPath = r.URL.Path }},
		{"get_never_consumes", func(r *http.Request, _ sso.SAMLCompleteInput, _ httpserver.CookiePolicy) { r.Method = http.MethodGet }},
		{"missing_key", func(r *http.Request, _ sso.SAMLCompleteInput, _ httpserver.CookiePolicy) {
			r.Header.Del("Idempotency-Key")
		}},
		{"duplicate_key", func(r *http.Request, input sso.SAMLCompleteInput, _ httpserver.CookiePolicy) {
			r.Header.Add("Idempotency-Key", input.FlowID)
		}},
		{"different_mutation_key", func(r *http.Request, _ sso.SAMLCompleteInput, _ httpserver.CookiePolicy) {
			r.Header.Set("Idempotency-Key", uuid.NewString())
		}},
		{"missing_session", func(r *http.Request, input sso.SAMLCompleteInput, c httpserver.CookiePolicy) {
			r.Header.Del("Cookie")
			r.AddCookie(&http.Cookie{Name: c.Name("portal", httpserver.SSOFlowCookie), Value: input.Continuation})
		}},
		{"missing_continuation", func(r *http.Request, input sso.SAMLCompleteInput, c httpserver.CookiePolicy) {
			r.Header.Del("Cookie")
			r.AddCookie(&http.Cookie{Name: c.Name("portal", httpserver.SessionCookie), Value: input.PreviousSession})
		}},
		{"duplicate_session", func(r *http.Request, input sso.SAMLCompleteInput, c httpserver.CookiePolicy) {
			r.AddCookie(&http.Cookie{Name: c.Name("portal", httpserver.SessionCookie), Value: input.PreviousSession})
		}},
		{"duplicate_continuation", func(r *http.Request, input sso.SAMLCompleteInput, c httpserver.CookiePolicy) {
			r.AddCookie(&http.Cookie{Name: c.Name("portal", httpserver.SSOFlowCookie), Value: input.Continuation})
		}},
		{"unsupported_content_type", func(r *http.Request, _ sso.SAMLCompleteInput, _ httpserver.CookiePolicy) {
			r.Header.Set("Content-Type", "application/json; charset=utf-16")
		}},
		{"duplicate_content_type", func(r *http.Request, _ sso.SAMLCompleteInput, _ httpserver.CookiePolicy) {
			r.Header.Add("Content-Type", "application/json")
		}},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			r, input, cookies := samlCompletionHTTPRequest(t)
			item.change(r, input, cookies)
			out, e := samlCompletionBrowserInput(r, "portal", cookies)
			if e == nil || out != (sso.SAMLCompleteInput{}) {
				t.Fatal("changed browser authority was admitted", e)
			}
		})
	}
	for _, body := range []struct{ name, value string }{
		{"tenant_body", `{"tenant_id":"` + uuid.NewString() + `"}`}, {"user_body", `{"user_id":"` + uuid.NewString() + `"}`},
		{"provider_claim_body", `{"assertion":"untrusted"}`}, {"continuation_body", `{"continuation":"untrusted"}`},
		{"null_body", "null"}, {"array_body", "[]"}, {"two_objects", "{}{}"}, {"oversized_body", "{" + strings.Repeat(" ", httpserver.MaxJSONBytes) + "}"},
	} {
		t.Run(body.name, func(t *testing.T) {
			r, _, cookies := samlCompletionHTTPRequest(t)
			r.Body = io.NopCloser(strings.NewReader(body.value))
			r.ContentLength = int64(len(body.value))
			out, e := samlCompletionBrowserInput(r, "portal", cookies)
			if e == nil || out != (sso.SAMLCompleteInput{}) {
				t.Fatal("client identity/claim body was admitted", e)
			}
		})
	}
	r, _, cookies := samlCompletionHTTPRequest(t)
	if out, e := samlCompletionBrowserInput(r, "admin", cookies); e == nil || out != (sso.SAMLCompleteInput{}) {
		t.Fatal("unqualified Admin completion was admitted", e)
	}
}

func TestSAMLCompletionHTTPRequiresSameOwner(t *testing.T) {
	for _, handler := range []*Handler{nil, {}, {Service: &sso.Service{Identity: &identity.Service{}}}, {Service: &sso.Service{Identity: &identity.Service{}}, SAMLCompletion: &sso.SAMLCompletion{}}} {
		r, _, _ := samlCompletionHTTPRequest(t)
		w := httptest.NewRecorder()
		out, status, e := handler.HandleSAMLCompletion(w, r, "portal", uuid.NewString())
		if e == nil || status != 0 || out != nil || len(w.Result().Cookies()) != 0 {
			t.Fatal("missing/mixed owner emitted authentication output", e)
		}
	}
}
