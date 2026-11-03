package http

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
)

type Handler struct{ Service *deployments.Service }

func Owns(path string) bool {
	if path == "/api/v1/admin/deployments" || strings.HasPrefix(path, "/api/v1/admin/deployments/") {
		return true
	}
	if strings.HasPrefix(path, "/api/v1/admin/nodes/") {
		parts := strings.Split(strings.TrimPrefix(path, "/api/v1/admin/nodes/"), "/")
		return len(parts) >= 2 && parts[1] == "inference"
	}
	if strings.HasPrefix(path, "/api/v1/workspaces/") {
		parts := strings.Split(strings.TrimPrefix(path, "/api/v1/workspaces/"), "/")
		return len(parts) >= 2 && parts[1] == "deployments"
	}
	return false
}
func id(value string) bool {
	parsed, e := uuid.Parse(value)
	return e == nil && parsed.String() == value
}
func (h *Handler) Handle(r *http.Request, a identity.Auth, request string) (any, int, error) {
	bad := errors.New("VALIDATION_FAILED")
	missing := errors.New("RESOURCE_NOT_FOUND")
	admin := strings.HasPrefix(r.URL.Path, "/api/v1/admin/")
	if admin && (a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin) {
		return nil, 0, errors.New("ACCESS_DENIED")
	}
	if !admin && a.Me.Audience != "portal" {
		return nil, 0, missing
	}
	key := ""
	if r.Method == "POST" {
		values := r.Header.Values("Idempotency-Key")
		if len(values) != 1 || !id(values[0]) {
			return nil, 0, bad
		}
		key = values[0]
	}
	if strings.HasPrefix(r.URL.Path, "/api/v1/admin/nodes/") {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/admin/nodes/"), "/")
		if len(parts) < 2 || !id(parts[0]) || parts[1] != "inference" || r.URL.RawQuery != "" {
			return nil, 0, bad
		}
		if len(parts) == 2 && r.Method == "GET" {
			out, e := h.Service.Node(r.Context(), a, "", parts[0])
			return out, 200, e
		}
		if len(parts) == 3 && parts[2] == "observations" && r.Method == "POST" {
			var input struct {
				Reason string `json:"reason"`
			}
			if e := httpserver.DecodeJSON(r, &input); e != nil {
				return nil, 0, e
			}
			out, e := h.Service.Discover(r.Context(), a, parts[0], input.Reason, key, request)
			return out, 202, e
		}
		return nil, 0, missing
	}
	workspace := ""
	parts := []string{}
	if admin {
		parts = strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/admin/deployments"), "/")
		if parts[0] == "" {
			parts = parts[1:]
		}
	} else {
		all := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/workspaces/"), "/")
		if len(all) < 2 || !id(all[0]) || all[1] != "deployments" {
			return nil, 0, bad
		}
		workspace = all[0]
		parts = all[2:]
	}
	if len(parts) == 0 && r.Method == "GET" {
		q := r.URL.Query()
		for name, values := range q {
			if len(values) != 1 || name != "model_id" && name != "retained" && !(admin && name == "workspace_id") {
				return nil, 0, bad
			}
		}
		if admin {
			workspace = q.Get("workspace_id")
			if !id(workspace) {
				return nil, 0, bad
			}
		}
		model := q.Get("model_id")
		if values, ok := q["model_id"]; ok && (len(values) != 1 || !id(model)) {
			return nil, 0, bad
		}
		retained := false
		if value, ok := q["retained"]; ok {
			if len(value) != 1 || value[0] != "true" {
				return nil, 0, bad
			}
			retained = true
		}
		out, e := h.Service.ListFiltered(r.Context(), a, workspace, model, retained)
		return map[string]any{"items": out}, 200, e
	}
	if r.URL.RawQuery != "" {
		return nil, 0, bad
	}
	if admin && len(parts) == 0 && r.Method == "POST" {
		var input deployments.CreateRequest
		if e := httpserver.DecodeJSON(r, &input); e != nil {
			return nil, 0, e
		}
		out, e := h.Service.Create(r.Context(), a, input, key, request)
		return out, 202, e
	}
	if len(parts) < 1 || !id(parts[0]) {
		return nil, 0, bad
	}
	if len(parts) == 1 && r.Method == "GET" {
		out, e := h.Service.Get(r.Context(), a, workspace, parts[0])
		return out, 200, e
	}
	if admin && len(parts) == 2 && parts[1] == "actions" && r.Method == "POST" {
		var input deployments.ActionRequest
		if e := httpserver.DecodeJSON(r, &input); e != nil {
			return nil, 0, e
		}
		out, e := h.Service.Action(r.Context(), a, parts[0], input, key, request)
		return out, 202, e
	}
	return nil, 0, missing
}
