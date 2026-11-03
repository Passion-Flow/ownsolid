package http

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/middleware"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
)

type Handler struct{ Service *middleware.Service }

// Owns matches exactly the audience-specific Workspace middleware segment.
func Owns(path string) bool {
	if !strings.HasPrefix(path, "/api/v1/") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/v1/"), "/")
	if len(parts) > 0 && parts[0] == "admin" {
		parts = parts[1:]
	}
	return len(parts) >= 3 && parts[0] == "workspaces" && parts[1] != "" && parts[2] == "middleware"
}

func route(path string, a identity.Auth) (string, []string, bool) {
	if !Owns(path) {
		return "", nil, false
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/v1/"), "/")
	admin := len(parts) > 0 && parts[0] == "admin"
	if admin {
		parts = parts[1:]
	}
	if admin != (a.Me.Audience == "admin") || len(parts) < 3 || parts[0] != "workspaces" || parts[2] != "middleware" {
		return "", nil, false
	}
	return parts[1], parts[3:], true
}

func (h *Handler) Handle(w http.ResponseWriter, r *http.Request, a identity.Auth, request string) (any, int, error) {
	w.Header().Set("Cache-Control", "no-store")
	workspace, parts, ok := route(r.URL.Path, a)
	if !ok {
		return nil, 0, errors.New("RESOURCE_NOT_FOUND")
	}
	if h == nil || h.Service == nil {
		return nil, 0, errors.New("SERVICE_NOT_READY")
	}
	for key, values := range r.URL.Query() {
		if key != "cursor" || len(values) != 1 || r.Method != http.MethodGet {
			return nil, 0, errors.New("VALIDATION_FAILED")
		}
	}
	s := h.Service
	ctx := r.Context()
	if len(parts) == 0 {
		switch r.Method {
		case http.MethodGet:
			v, e := s.List(ctx, a, workspace, r.URL.Query().Get("cursor"))
			return v, 200, e
		case http.MethodPost:
			var input struct {
				ProfileKey     string `json:"profile_key"`
				Name           string `json:"name"`
				IdempotencyKey string `json:"idempotency_key"`
			}
			if e := httpserver.DecodeJSON(r, &input); e != nil {
				return nil, 0, e
			}
			v, e := s.Register(ctx, a, workspace, input.ProfileKey, input.Name, input.IdempotencyKey, request)
			return v, 201, e
		}
	}
	if len(parts) == 1 && parts[0] == "profiles" && r.Method == http.MethodGet {
		v, e := s.Choices(ctx, a, workspace)
		return v, 200, e
	}
	if len(parts) == 2 && r.Method == http.MethodGet {
		switch parts[0] {
		case "registration-intents":
			v, e := s.RegistrationIntent(ctx, a, workspace, parts[1])
			return v, 200, e
		case "operation-intents":
			v, e := s.OperationIntent(ctx, a, workspace, parts[1])
			return v, 200, e
		case "operations":
			v, e := s.Operation(ctx, a, workspace, parts[1])
			return v, 200, e
		}
	}
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			v, e := s.Get(ctx, a, workspace, parts[0])
			return v, 200, e
		case http.MethodPatch:
			var input struct {
				Name          string `json:"name"`
				State         string `json:"state"`
				Version       int64  `json:"version"`
				RefreshInputs bool   `json:"refresh_inputs"`
				Reason        string `json:"reason"`
			}
			if e := httpserver.DecodeJSON(r, &input); e != nil {
				return nil, 0, e
			}
			v, e := s.Update(ctx, a, workspace, parts[0], input.Name, input.State, input.Version, input.RefreshInputs, input.Reason, request)
			return v, 200, e
		}
	}
	if len(parts) == 2 && parts[1] == "operations" {
		switch r.Method {
		case http.MethodGet:
			v, e := s.History(ctx, a, workspace, parts[0], r.URL.Query().Get("cursor"))
			return v, 200, e
		case http.MethodPost:
			var input middleware.OperationInput
			if e := httpserver.DecodeJSON(r, &input); e != nil {
				return nil, 0, e
			}
			v, e := s.Enqueue(ctx, a, workspace, parts[0], input, request)
			return v, 202, e
		}
	}
	return nil, 0, errors.New("RESOURCE_NOT_FOUND")
}
