package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/sdk/go/identity"
)

const auditReadPath = "/api/v1/admin/audit-events"
const auditReadProblem = `{"type":"about:blank","title":"STORAGE_COMMIT_OUTCOME_UNKNOWN","status":409,"code":"STORAGE_COMMIT_OUTCOME_UNKNOWN","request_id":"33333333-3333-4333-8333-333333333333"}`

func auditReadParams(assistant bool) *identity.ListAuditEventsParams {
	category, cursor, limit := "audit", identity.Cursor("opaque+/=%"), identity.Limit(2)
	params := &identity.ListAuditEventsParams{Category: &category, Cursor: &cursor, Limit: &limit}
	if assistant {
		value := identity.ListAuditEventsParamsXOwnsolidAuditActor("true")
		params.XOwnsolidAuditActor = &value
	}
	return params
}

func auditReadClient(t *testing.T, origin string, client *http.Client) *identity.ClientWithResponses {
	t.Helper()
	result, err := identity.NewClientWithResponses(origin, identity.WithHTTPClient(client))
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func auditReadHTTPClient(t *testing.T) (*http.Client, *http.Transport) {
	t.Helper()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.MaxConnsPerHost = 1
	transport.Protocols = new(http.Protocols)
	transport.Protocols.SetHTTP1(true)
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	t.Cleanup(transport.CloseIdleConnections)
	return client, transport
}

func writeAuditReadProblem(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(http.StatusConflict)
	io.WriteString(w, auditReadProblem)
}

func requireAuditReadProblem(t *testing.T, result *identity.ListAuditEventsHTTPResponse, err error) {
	t.Helper()
	if err != nil || result == nil || result.StatusCode() != 409 || result.GetJSON200() != nil || result.GetApplicationproblemJSON409() == nil {
		t.Fatalf("audit 409 did not retain its dedicated Problem: %v", err)
	}
	if string(result.GetApplicationproblemJSON409().Code) != "STORAGE_COMMIT_OUTCOME_UNKNOWN" {
		t.Fatal("audit Problem code changed")
	}
}

func TestAuditRead409DoesNotRetry(t *testing.T) {
	for _, assistant := range []bool{false, true} {
		t.Run(map[bool]string{false: "standard", true: "assistant"}[assistant], func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				expected := ""
				if assistant {
					expected = "true"
				}
				q := r.URL.Query()
				if r.Method != http.MethodGet || r.URL.Path != auditReadPath || len(q) != 3 || q.Get("category") != "audit" || q.Get("cursor") != "opaque+/=%" || q.Get("limit") != "2" || r.ContentLength != 0 || r.Header.Get("X-Ownsolid-Audit-Actor") != expected || r.Header.Get("Idempotency-Key") != "" {
					t.Error("audit selection, representation or bodyless request changed")
				}
				writeAuditReadProblem(w)
			}))
			defer server.Close()
			client, _ := auditReadHTTPClient(t)
			result, err := auditReadClient(t, server.URL, client).ListAuditEventsWithResponse(t.Context(), auditReadParams(assistant))
			requireAuditReadProblem(t, result, err)
			if calls.Load() != 1 {
				t.Fatal("409 initiated an automatic second read")
			}
		})
	}
}

func TestAuditReadExplicitRetryIsANewRequest(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			writeAuditReadProblem(w)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"data":{"items":[],"next_cursor":null}}`)
	}))
	defer server.Close()
	client, _ := auditReadHTTPClient(t)
	sdk := auditReadClient(t, server.URL, client)
	first, err := sdk.ListAuditEventsWithResponse(t.Context(), auditReadParams(false))
	requireAuditReadProblem(t, first, err)
	if calls.Load() != 1 {
		t.Fatal("the first read replayed itself")
	}
	second, err := sdk.ListAuditEventsWithResponse(t.Context(), auditReadParams(false))
	if err != nil || second.GetJSON200() == nil || calls.Load() != 2 {
		t.Fatalf("explicit second read failed: %v", err)
	}
}

func auditLostReplyFixture(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	calls := new(atomic.Int32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/prime" {
			io.WriteString(w, "ok")
			return
		}
		if r.URL.Path != auditReadPath {
			t.Error("unexpected fixture path")
			w.WriteHeader(404)
			return
		}
		if calls.Add(1) == 1 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			conn.Close()
			return
		}
		writeAuditReadProblem(w)
	}))
	t.Cleanup(server.Close)
	return server, calls
}

func primeAuditConnection(t *testing.T, client *http.Client, origin string) {
	t.Helper()
	response, err := client.Get(origin + "/prime")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		t.Fatal(err)
	}
}

func TestStandardSharedGETCanReplayALostReply(t *testing.T) {
	server, calls := auditLostReplyFixture(t)
	client, _ := auditReadHTTPClient(t)
	primeAuditConnection(t, client, server.URL)
	response, err := client.Get(server.URL + auditReadPath)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	io.Copy(io.Discard, response.Body)
	if response.StatusCode != 409 || calls.Load() != 2 {
		t.Fatal("the shared-connection control did not demonstrate the replay")
	}
}

func TestAuditReadDoesNotReplayALostReplyOnASharedClient(t *testing.T) {
	server, calls := auditLostReplyFixture(t)
	client, transport := auditReadHTTPClient(t)
	primeAuditConnection(t, client, server.URL)
	var reused atomic.Bool
	trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { reused.Store(info.Reused) }}
	ctx := httptrace.WithClientTrace(t.Context(), trace)
	sdk := auditReadClient(t, server.URL, client)
	result, err := sdk.ListAuditEventsWithResponse(ctx, auditReadParams(false))
	if err == nil || result != nil || calls.Load() != 1 || reused.Load() {
		t.Fatal("the generated audit read replayed the lost reply or reused a connection")
	}
	if transport.DisableKeepAlives {
		t.Fatal("the generated read changed the caller's shared transport")
	}
	result, err = sdk.ListAuditEventsWithResponse(t.Context(), auditReadParams(false))
	requireAuditReadProblem(t, result, err)
	if calls.Load() != 2 {
		t.Fatal("an explicit retry did not remain a separate request")
	}
}

func TestAuditReadPreservesTLSAndCookies(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		cookie, err := r.Cookie("synthetic_admin")
		if err != nil || cookie.Value != "fixture" || r.ProtoMajor != 1 {
			t.Error("verified HTTP/1 or the configured cookie jar was lost")
		}
		writeAuditReadProblem(w)
	}))
	defer server.Close()
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	origin, _ := url.Parse(server.URL)
	jar.SetCookies(origin, []*http.Cookie{{Name: "synthetic_admin", Value: "fixture", Path: "/", Secure: true}})
	client, transport := auditReadHTTPClient(t)
	transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12, NextProtos: []string{"h2", "http/1.1"}}
	client.Jar = jar
	result, err := auditReadClient(t, server.URL, client).ListAuditEventsWithResponse(t.Context(), auditReadParams(false))
	requireAuditReadProblem(t, result, err)
	if calls.Load() != 1 || transport.TLSClientConfig.RootCAs != pool || strings.Join(transport.TLSClientConfig.NextProtos, ",") != "h2,http/1.1" || client.Jar != jar {
		t.Fatal("the original trust or cookie configuration changed")
	}
	bad := transport.Clone()
	bad.TLSClientConfig = &tls.Config{RootCAs: x509.NewCertPool(), MinVersion: tls.VersionTLS12}
	defer bad.CloseIdleConnections()
	result, err = auditReadClient(t, server.URL, &http.Client{Transport: bad, Jar: jar, Timeout: 5 * time.Second}).ListAuditEventsWithResponse(t.Context(), auditReadParams(false))
	if err == nil || result != nil || calls.Load() != 1 {
		t.Fatal("an untrusted server received an audit request")
	}
}

func TestAuditReadPreservesClientTimeout(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		<-r.Context().Done()
	}))
	defer server.Close()
	client, _ := auditReadHTTPClient(t)
	client.Timeout = time.Second
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	result, err := auditReadClient(t, server.URL, client).ListAuditEventsWithResponse(ctx, auditReadParams(false))
	if err == nil || result != nil || !strings.Contains(err.Error(), "Client.Timeout") || calls.Load() != 1 {
		t.Fatalf("the caller's timeout was lost or retried: %v", err)
	}
}

func TestAuditReadRefusesRedirects(t *testing.T) {
	var audit, redirected atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/again" {
			redirected.Add(1)
			writeAuditReadProblem(w)
			return
		}
		audit.Add(1)
		http.Redirect(w, r, "/again", http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	client, _ := auditReadHTTPClient(t)
	result, err := auditReadClient(t, server.URL, client).ListAuditEventsWithResponse(t.Context(), auditReadParams(false))
	if err != nil || result.StatusCode() != 307 || audit.Load() != 1 || redirected.Load() != 0 || client.CheckRedirect != nil {
		t.Fatalf("audit redirect was followed or changed shared policy: %v", err)
	}
}

type auditCountingDoer struct{ calls atomic.Int32 }

func (d *auditCountingDoer) Do(r *http.Request) (*http.Response, error) {
	d.calls.Add(1)
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}")), Header: make(http.Header), Request: r}, nil
}

type auditCountingTransport struct{ calls atomic.Int32 }

func (d *auditCountingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	d.calls.Add(1)
	return nil, errors.New("custom transport must not be reached")
}

func TestAuditReadRejectsUninspectableExecutorsBeforeDispatch(t *testing.T) {
	doer, transport := new(auditCountingDoer), new(auditCountingTransport)
	for _, executor := range []identity.HttpRequestDoer{doer, (*http.Client)(nil), &http.Client{Transport: transport}, &http.Client{Transport: (*http.Transport)(nil)}} {
		client, err := identity.NewClient("https://unused.example.invalid", identity.WithHTTPClient(executor))
		if err != nil {
			t.Fatal(err)
		}
		result, err := client.ListAuditEvents(t.Context(), auditReadParams(false))
		if err == nil || result != nil || !strings.Contains(err.Error(), "audit reads require") {
			t.Fatal("an unsupported executor was not rejected clearly before dispatch")
		}
	}
	if doer.calls.Load() != 0 || transport.calls.Load() != 0 {
		t.Fatal("a custom executor received an audit request")
	}
}

func TestOtherIdentityCallsRetainTheConfiguredExecutor(t *testing.T) {
	doer := new(auditCountingDoer)
	client, err := identity.NewClient("https://unused.example.invalid", identity.WithHTTPClient(doer))
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.GetCsrf(t.Context())
	if err != nil || doer.calls.Load() != 1 {
		t.Fatal("the audit guard changed another operation's executor")
	}
	response.Body.Close()
}

func TestConcurrentAuditReadsRemainOneRequestEach(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeAuditReadProblem(w)
	}))
	defer server.Close()
	client, transport := auditReadHTTPClient(t)
	sdk := auditReadClient(t, server.URL, client)
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			result, err := sdk.ListAuditEventsWithResponse(t.Context(), auditReadParams(false))
			if err != nil || result.GetApplicationproblemJSON409() == nil {
				t.Errorf("concurrent audit read failed: %v", err)
			}
		})
	}
	group.Wait()
	if calls.Load() != 8 || transport.DisableKeepAlives {
		t.Fatal("concurrent audit reads replayed or mutated the shared transport")
	}
}
