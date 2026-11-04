package distribution_test

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"io"
	"net/http"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	"strings"
	"testing"
)

func TestActualTLSMachineRoutesStrictFieldsReceiptsAndAudienceIsolation(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	access := c.access(t)
	server := httpfixture.NewTLSServer(t, identityhttp.New(c.s.Identity))
	defer server.Close()
	call := func(operation string, body any, change func(*http.Request)) (*http.Response, []byte) {
		t.Helper()
		var b []byte
		switch value := body.(type) {
		case string:
			b = []byte(value)
		default:
			var e error
			b, e = json.Marshal(value)
			if e != nil {
				t.Fatal(e)
			}
		}
		r, e := http.NewRequest(http.MethodPost, httpfixture.AdminOrigin+"/gateway/v1/sites/default/cells/default/instances/"+access.gateway.Gateway.ID+"/"+operation, bytes.NewReader(b))
		if e != nil {
			t.Fatal(e)
		}
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+*access.gateway.Secret)
		if change != nil {
			change(r)
		}
		response, e := server.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		raw, e := io.ReadAll(response.Body)
		if e != nil {
			t.Fatal(e)
		}
		if strings.Contains(string(raw), *access.key.Secret) || strings.Contains(string(raw), *access.gateway.Secret) {
			t.Fatal("HTTP response disclosed an authentication secret")
		}
		return response, raw
	}
	response, raw := call("models", map[string]any{"api_key": *access.key.Secret}, nil)
	if response.StatusCode != 200 || !bytes.Contains(raw, []byte(access.alias.Name)) {
		t.Fatal("actual TLS machine authorization", response.StatusCode, string(raw))
	}
	for _, body := range []string{`{"API_KEY":"synthetic"}`, `{"api_key":null}`, `{"api_key":"synthetic","api_key":"other"}`, `{"api_key":"synthetic","workspace_id":"00000000-0000-0000-0000-000000000001"}`} {
		response, raw = call("models", body, nil)
		if response.StatusCode != 400 || !bytes.Contains(raw, []byte("VALIDATION_FAILED")) {
			t.Fatal("strict machine body", response.StatusCode, string(raw))
		}
	}
	for _, header := range []string{"Cookie", "Origin", "X-Workspace-ID"} {
		response, _ = call("models", map[string]any{"api_key": *access.key.Secret}, func(r *http.Request) { r.Header.Set(header, "synthetic-context") })
		if response.StatusCode != 400 {
			t.Fatal("conflicting authority header accepted", header, response.StatusCode)
		}
	}
	response, _ = call("models", map[string]any{"api_key": *access.key.Secret}, func(r *http.Request) { r.Header.Add("Authorization", "Bearer another-credential") })
	if response.StatusCode != 401 {
		t.Fatal("duplicate credential accepted")
	}
	response, _ = call("models", map[string]any{"api_key": *access.key.Secret}, func(r *http.Request) { r.URL.Host = "portal.example.invalid"; r.Host = r.URL.Host })
	if response.StatusCode != 404 {
		t.Fatal("Portal entrance accepted machine authority", response.StatusCode)
	}
	input := access.input()
	response, raw = call("admissions", input, nil)
	if response.StatusCode != 200 || !bytes.Contains(raw, []byte(input.RequestID)) {
		t.Fatal("HTTP durable admission", response.StatusCode, string(raw))
	}
	outcome := success(input.RequestID)
	response, raw = call("outcomes", outcome, nil)
	digest, _ := outcome.Digest()
	if response.StatusCode != 200 || !bytes.Contains(raw, []byte(digest)) {
		t.Fatal("nullable canonical receipt", response.StatusCode, string(raw))
	}
	serialized, _ := json.Marshal(outcome)
	bad := strings.Replace(string(serialized), `"output_started":true`, `"output_started":null`, 1)
	response, _ = call("outcomes", bad, nil)
	if response.StatusCode != 400 {
		t.Fatal("undeclared outcome null accepted")
	}
	if _, e := c.s.RevokeKey(ctx, access.owner, access.alias.WorkspaceID, access.key.Key.ID, "Synthetic HTTP revocation", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	response, raw = call("models", map[string]any{"api_key": *access.key.Secret}, nil)
	if response.StatusCode != 401 || !bytes.Contains(raw, []byte("API_KEY_INVALID")) {
		t.Fatal("fresh HTTP revocation", response.StatusCode, string(raw))
	}
	response, _ = call("outcomes", outcome, nil)
	if response.StatusCode != 200 {
		t.Fatal("revoked API key prevented existing receipt replay")
	}
}
