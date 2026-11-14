package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/applications"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/productagent"
)

func TestCommandRequestBoundary(t *testing.T) {
	service, err := productagent.New(&applications.Service{Identity: &identity.Service{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := &Handler{Service: service}
	for _, tt := range []struct {
		name, method, path, contentType, body, code string
	}{
		{"subroute", http.MethodPost, commandPath + "/extra", "application/json", `{}`, "RESOURCE_NOT_FOUND"},
		{"encoded path", http.MethodPost, strings.Replace(commandPath, "commands", "%63ommands", 1), "application/json", `{}`, "RESOURCE_NOT_FOUND"},
		{"read method", http.MethodGet, commandPath, "application/json", `{}`, "RESOURCE_NOT_FOUND"},
		{"query", http.MethodPost, commandPath + "?workspace_id=untrusted", "application/json", `{}`, "VALIDATION_FAILED"},
		{"empty query", http.MethodPost, commandPath + "?", "application/json", `{}`, "VALIDATION_FAILED"},
		{"media type", http.MethodPost, commandPath, "text/plain", `{}`, "UNSUPPORTED_MEDIA_TYPE"},
		{"oversize", http.MethodPost, commandPath, "application/json", strings.Repeat(" ", maxCommandBody+1), "PAYLOAD_TOO_LARGE"},
		{"missing command", http.MethodPost, commandPath, "application/json", `{}`, "VALIDATION_FAILED"},
		{"wrong case", http.MethodPost, commandPath, "application/json", `{"Command":"open home"}`, "VALIDATION_FAILED"},
		{"duplicate", http.MethodPost, commandPath, "application/json", `{"command":"open home","command":"open nodes"}`, "VALIDATION_FAILED"},
		{"trailing value", http.MethodPost, commandPath, "application/json", `{"command":"open home"}{}`, "VALIDATION_FAILED"},
		{"null cursor", http.MethodPost, commandPath, "application/json", `{"command":"open home","cursor":null}`, "VALIDATION_FAILED"},
		{"request id in body", http.MethodPost, commandPath, "application/json", `{"command":"open home","request_id":"untrusted"}`, "VALIDATION_FAILED"},
		{"unknown command", http.MethodPost, commandPath, "application/json", `{"command":"confirm plan"}`, "VALIDATION_FAILED"},
		{"empty command", http.MethodPost, commandPath, "application/json", `{"command":" "}`, "VALIDATION_FAILED"},
		{"workspace required", http.MethodPost, commandPath, "application/json", `{"command":"open home"}`, "ACCESS_DENIED"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, strings.NewReader(tt.body))
			r.Header.Set("Content-Type", tt.contentType)
			assertProblem(t, handler, r, commandActor("portal"), uuid.NewString(), tt.code)
		})
	}
}

func TestCommandRequiresDispatcherContext(t *testing.T) {
	service, err := productagent.New(&applications.Service{Identity: &identity.Service{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := &Handler{Service: service}
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, commandPath, strings.NewReader(`{"command":"open home"}`))
	r.Header.Set("Content-Type", "application/json")
	assertProblem(t, handler, r, identity.Auth{}, uuid.NewString(), "AUTH_REQUIRED")
	assertProblem(t, handler, r, commandActor("other"), uuid.NewString(), "AUTH_REQUIRED")
	assertProblem(t, &Handler{}, r, commandActor("portal"), uuid.NewString(), "SERVICE_NOT_READY")
	r.Header.Set("X-Request-ID", uuid.NewString())
	assertProblem(t, handler, r, commandActor("portal"), "", "INTERNAL_ERROR")
}

func commandActor(audience string) identity.Auth {
	actor := identity.Auth{SessionID: uuid.NewString()}
	actor.Me.Audience = audience
	return actor
}

func assertProblem(t *testing.T, h *Handler, r *http.Request, actor identity.Auth, id, code string) {
	t.Helper()
	data, status, err := h.Handle(r, actor, id)
	if data != nil || status != 0 || err == nil || err.Error() != code {
		t.Fatalf("got data %t, status %d, error %v; want %s", data != nil, status, err, code)
	}
}
