package client

import (
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

func TestEnrollmentUncertaintyTrustedTLSAndMalformedProblems(t *testing.T) {
	const requestID = "12345678-1234-1234-1234-123456789abc"
	const private = "SYNTHETIC_PRIVATE_RESPONSE_MUST_NOT_ESCAPE"
	for _, name := range []string{"valid", "duplicate", "extra_private", "trailing", "wrong_http_status", "wrong_body_status",
		"wrong_type", "private_title", "missing_id", "zero_id", "mismatched_header", "duplicate_header", "wrong_content_type", "missing_no_store", "retry_advice", "other_route"} {
		t.Run(name, func(t *testing.T) {
			var requests atomic.Int32
			problem := map[string]any{"type": "about:blank", "title": enrollmentCommitUnknown,
				"status": 409, "code": enrollmentCommitUnknown, "request_id": requestID}
			status, path := http.StatusConflict, "/agent/v1/enroll"
			switch name {
			case "extra_private":
				problem["credential"] = private
			case "wrong_http_status":
				status = http.StatusServiceUnavailable
			case "wrong_body_status":
				problem["status"] = 503
			case "wrong_type":
				problem["type"] = "foreign"
			case "private_title":
				problem["title"] = private
			case "missing_id":
				delete(problem, "request_id")
			case "zero_id":
				problem["request_id"] = "00000000-0000-0000-0000-000000000000"
			case "other_route":
				path = "/agent/v1/unrelated"
			}
			raw, _ := json.Marshal(problem)
			if name == "duplicate" {
				raw = append(raw[:len(raw)-1], []byte(`,"request_id":"`+requestID+`"}`)...)
			}
			if name == "trailing" {
				raw = append(raw, []byte(` {"credential":"`+private+`"}`)...)
			}
			// The response is an explicit protocol test double over real verified
			// loopback TLS. Actual backend COMMIT evidence is a separate SQL gate.
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != "POST" || r.URL.Path != path {
					t.Error("unexpected enrollment retry or route")
				}
				w.Header().Set("Content-Type", "application/problem+json")
				w.Header().Set("Cache-Control", "no-store")
				w.Header().Set("X-Request-ID", requestID)
				switch name {
				case "mismatched_header":
					w.Header().Set("X-Request-ID", "22345678-1234-1234-1234-123456789abc")
				case "duplicate_header":
					w.Header().Add("X-Request-ID", requestID)
				case "wrong_content_type":
					w.Header().Set("Content-Type", "text/plain")
				case "missing_no_store":
					w.Header().Del("Cache-Control")
				case "retry_advice":
					w.Header().Set("Retry-After", "1")
				}
				w.WriteHeader(status)
				w.Write(raw)
			}))
			defer server.Close()
			ca := filepath.Join(t.TempDir(), "ca.pem")
			if e := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); e != nil {
				t.Fatal(e)
			}
			connection, e := New(server.URL, ca)
			if e != nil {
				t.Fatal(e)
			}
			defer connection.Close()
			var out wire.Enrolled
			e = connection.Post(t.Context(), path, map[string]string{"join_token": "synthetic-request-only"}, &out)
			if e == nil || !reflect.DeepEqual(out, wire.Enrolled{}) || requests.Load() != 1 || strings.Contains(e.Error(), private) {
				t.Fatal("uncertain enrollment retried,decoded private success or leaked diagnostics")
			}
			var uncertain *EnrollmentUncertain
			if name == "valid" {
				if !errors.As(e, &uncertain) || uncertain.RequestID() != requestID || e.Error() != enrollmentCommitUnknown+" request_id="+requestID {
					t.Fatal("validated uncertainty reconciliation context lost")
				}
			} else if errors.As(e, &uncertain) || e.Error() != "CONTROL_RESPONSE_REJECTED" {
				t.Fatal("malformed/foreign uncertainty response trusted", e)
			}
		})
	}
}
