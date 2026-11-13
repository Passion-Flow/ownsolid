package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/sdk/go/identity"
)

const legacyAuditPath = "/api/v1/admin/audit-events"
const legacyAuditFirstID = "11111111-1111-4111-8111-111111111111"
const legacyAuditSecondID = "22222222-2222-4222-8222-222222222222"

// This file intentionally uses only the Default API available before explicit 409.
func TestLegacyAuditResponseSourceAssignment(t *testing.T) {
	problem := &identity.Problem{Code: "STORAGE_COMMIT_OUTCOME_UNKNOWN"}
	headers := &identity.ListAuditEventsHTTPResponseDefaultHeaders{}
	response := identity.ListAuditEventsHTTPResponse{
		ApplicationproblemJSONDefault: problem,
		HeadersDefault:                headers,
	}
	if response.GetApplicationproblemJSONDefault() != problem || response.HeadersDefault != headers {
		t.Fatal("the legacy public response fields or getter changed")
	}
}

func legacyAuditClient(t *testing.T, handler http.HandlerFunc, assistant bool) (*identity.ClientWithResponses, *identity.ListAuditEventsParams) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expected := ""
		if assistant {
			expected = "true"
		}
		query := r.URL.Query()
		if r.Method != http.MethodGet || r.URL.Path != legacyAuditPath || len(query) != 2 || query.Get("limit") != "2" || query.Get("cursor") != "opaque+/=%" || r.Header.Get("X-Ownsolid-Audit-Actor") != expected || r.Header.Get("Idempotency-Key") != "" || r.ContentLength > 0 {
			t.Error("the generated legacy caller changed its audited selection or representation")
			http.Error(w, "invalid audit selection", http.StatusBadRequest)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DisableKeepAlives = true
	transport.MaxConnsPerHost = 1
	transport.Protocols = new(http.Protocols)
	transport.Protocols.SetHTTP1(true)
	t.Cleanup(transport.CloseIdleConnections)
	client, err := identity.NewClientWithResponses(server.URL, identity.WithHTTPClient(&http.Client{
		Transport: transport,
		Timeout:   5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	limit, cursor := identity.Limit(2), identity.Cursor("opaque+/=%")
	params := &identity.ListAuditEventsParams{Limit: &limit, Cursor: &cursor}
	if assistant {
		value := identity.ListAuditEventsParamsXOwnsolidAuditActor("true")
		params.XOwnsolidAuditActor = &value
	}
	return client, params
}

func writeLegacyAuditProblem(w http.ResponseWriter, status int, requestID string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("X-Request-ID", requestID)
	w.WriteHeader(status)
	code := "STORAGE_COMMIT_OUTCOME_UNKNOWN"
	if status != http.StatusConflict {
		code = "SERVICE_NOT_READY"
	}
	json.NewEncoder(w).Encode(map[string]any{
		"type": "about:blank", "title": code, "status": status, "code": code, "request_id": requestID,
	})
}

func writeLegacyAuditPage(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Request-ID", legacyAuditSecondID)
	io.WriteString(w, `{"data":{"items":[],"next_cursor":null}}`)
}

func requireLegacyAuditProblem(t *testing.T, response *identity.ListAuditEventsHTTPResponse, err error, status int) {
	t.Helper()
	if err != nil || response == nil || response.StatusCode() != status || response.GetJSON200() != nil {
		t.Fatalf("the legacy caller lost the error status or exposed a page: %v", err)
	}
	problem := response.GetApplicationproblemJSONDefault()
	headers := response.HeadersDefault
	if problem == nil || problem != response.ApplicationproblemJSONDefault || headers == nil || headers.XRequestID == nil {
		t.Fatal("the legacy Default body or headers were not populated")
	}
	if problem.Status != status || problem.RequestId.String() != legacyAuditFirstID || headers.XRequestID.String() != legacyAuditFirstID || response.HTTPResponse.Header.Get("X-Request-ID") != legacyAuditFirstID || headers.RetryAfter != nil {
		t.Fatal("the legacy caller lost correlation or gained a retry hint")
	}
	if status == http.StatusConflict && string(problem.Code) != "STORAGE_COMMIT_OUTCOME_UNKNOWN" {
		t.Fatal("the legacy caller lost the stable uncertain-commit code")
	}
	var wire map[string]any
	if err := json.Unmarshal(response.GetBody(), &wire); err != nil {
		t.Fatal("the legacy caller lost the raw Problem body", err)
	}
	if wire["code"] != string(problem.Code) || wire["status"] != float64(status) || wire["request_id"] != legacyAuditFirstID || wire["type"] != "about:blank" || wire["data"] != nil {
		t.Fatal("the typed legacy Problem disagrees with the raw body or exposes a page")
	}
}

func requireLegacyAuditPage(t *testing.T, response *identity.ListAuditEventsHTTPResponse, err error) {
	t.Helper()
	if err != nil || response == nil || response.StatusCode() != http.StatusOK || response.GetJSON200() == nil || response.HTTPResponse.Header.Get("X-Request-ID") != legacyAuditSecondID {
		t.Fatalf("the deliberate read did not return a separately correlated page: %v", err)
	}
	if response.GetApplicationproblemJSONDefault() != nil || response.HeadersDefault != nil {
		t.Fatal("the successful page inherited an earlier Default error")
	}
}

func TestLegacyAuditRead409AndDeliberateRetry(t *testing.T) {
	for _, assistant := range []bool{false, true} {
		t.Run(map[bool]string{false: "standard", true: "assistant"}[assistant], func(t *testing.T) {
			var calls atomic.Int32
			client, params := legacyAuditClient(t, func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					writeLegacyAuditProblem(w, http.StatusConflict, legacyAuditFirstID)
					return
				}
				writeLegacyAuditPage(w)
			}, assistant)
			first, err := client.ListAuditEventsWithResponse(t.Context(), params)
			requireLegacyAuditProblem(t, first, err, http.StatusConflict)
			if calls.Load() != 1 {
				t.Fatal("409 initiated an automatic second read")
			}
			second, err := client.ListAuditEventsWithResponse(t.Context(), params)
			requireLegacyAuditPage(t, second, err)
			if calls.Load() != 2 || first.HTTPResponse.Header.Get("X-Request-ID") == second.HTTPResponse.Header.Get("X-Request-ID") {
				t.Fatal("the deliberate read was not a distinct request")
			}
		})
	}
}

func TestLegacyAuditReadUnknownStatus(t *testing.T) {
	var calls atomic.Int32
	client, params := legacyAuditClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeLegacyAuditProblem(w, http.StatusTeapot, legacyAuditFirstID)
	}, false)
	response, err := client.ListAuditEventsWithResponse(t.Context(), params)
	requireLegacyAuditProblem(t, response, err, http.StatusTeapot)
	if calls.Load() != 1 {
		t.Fatal("an undeclared error status initiated another read")
	}
}

func TestLegacyAuditReadLostReplyDoesNotReplay(t *testing.T) {
	for _, assistant := range []bool{false, true} {
		t.Run(map[bool]string{false: "standard", true: "assistant"}[assistant], func(t *testing.T) {
			var calls atomic.Int32
			client, params := legacyAuditClient(t, func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					connection, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					connection.Close()
					return
				}
				writeLegacyAuditPage(w)
			}, assistant)
			response, err := client.ListAuditEventsWithResponse(t.Context(), params)
			if err == nil || response != nil || calls.Load() != 1 {
				t.Fatal("a lost response was replayed or became a page")
			}
			response, err = client.ListAuditEventsWithResponse(t.Context(), params)
			requireLegacyAuditPage(t, response, err)
			if calls.Load() != 2 {
				t.Fatal("the explicit call after a lost reply was not a single new request")
			}
		})
	}
}
