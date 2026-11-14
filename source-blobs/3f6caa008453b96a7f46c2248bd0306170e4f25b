package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRegistryPublicFailuresPreserveAuthorityAndRedaction(t *testing.T) {
	for _, test := range []struct {
		name   string
		input  error
		code   string
		status int
	}{
		{"backend_credentials_do_not_expire_browser", errors.New("REGISTRY_AUTH_REJECTED"), "REGISTRY_AUTH_REJECTED", 503},
		{"backend_tls_refusal", errors.New("REGISTRY_TLS_INVALID"), "REGISTRY_TLS_INVALID", 503},
		{"revoked_grant_forbidden", errors.New("REGISTRY_GRANT_REVOKED"), "REGISTRY_GRANT_REVOKED", 403},
		{"invalid_verified_bytes", errors.New("REGISTRY_DIGEST_MISMATCH"), "REGISTRY_DIGEST_MISMATCH", 422},
		{"invalid_range", errors.New("REGISTRY_RANGE_INVALID"), "REGISTRY_RANGE_INVALID", 416},
		{"held_reference_conflict", errors.New("REGISTRY_REFERENCE_HELD"), "REGISTRY_REFERENCE_HELD", 409},
		{"remote_unknown_conflict", errors.New("REGISTRY_REMOTE_OUTCOME_UNKNOWN"), "REGISTRY_REMOTE_OUTCOME_UNKNOWN", 409},
		{"private_wrapped_diagnostic", fmt.Errorf("private vendor credential: %w", errors.New("REGISTRY_AUTH_REJECTED")), "STORAGE_UNAVAILABLE", 503},
		{"unregistered_private_code", errors.New("REGISTRY_private_credential"), "STORAGE_UNAVAILABLE", 503},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			Problem(w, "11111111-1111-4111-8111-111111111111", test.input)
			var body struct {
				Code string `json:"code"`
			}
			if e := json.Unmarshal(w.Body.Bytes(), &body); e != nil {
				t.Fatal(e)
			}
			if w.Code != test.status || body.Code != test.code || strings.Contains(w.Body.String(), "private") {
				t.Fatal("unsafe public registry problem", w.Code, body.Code)
			}
		})
	}
}
