package http

import (
	"errors"
	"github.com/google/uuid"
	"net/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/components"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"strings"
	"unicode/utf8"
)

type Handler struct{ Service *components.Service }

func Owns(path string) bool {
	return (strings.HasPrefix(path, "/api/v1/admin/nodes/") || strings.HasPrefix(path, "/api/v1/workspaces/")) && strings.Contains(path, "/nodes/") && strings.Contains(path, "/components/")
}
func (h *Handler) Handle(r *http.Request, a identity.Auth, request string) (any, int, error) {
	bad := errors.New("VALIDATION_FAILED")
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	workspace := ""
	index := 4
	if len(parts) > 4 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "workspaces" && parts[4] == "nodes" {
		workspace = parts[3]
		index = 5
	} else if len(parts) < 4 || parts[0] != "api" || parts[1] != "v1" || parts[2] != "admin" || parts[3] != "nodes" {
		return nil, 0, errors.New("RESOURCE_NOT_FOUND")
	}
	if workspace == "" && (a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin) {
		return nil, 0, errors.New("ACCESS_DENIED")
	}
	if workspace != "" && a.Me.Audience != "portal" {
		return nil, 0, errors.New("RESOURCE_NOT_FOUND")
	}
	if len(parts) < index+3 || parts[index+1] != "components" || parts[index+2] != "node-exporter" {
		return nil, 0, errors.New("RESOURCE_NOT_FOUND")
	}
	node := parts[index]
	if _, e := uuid.Parse(node); e != nil || len(node) != 36 {
		return nil, 0, bad
	}
	if r.URL.RawQuery != "" {
		return nil, 0, bad
	}
	if len(parts) == index+3 && r.Method == "GET" {
		out, e := h.Service.Get(r.Context(), a, workspace, node)
		return out, 200, e
	}
	if workspace == "" && len(parts) == index+4 && parts[index+3] == "logs" && r.Method == "GET" {
		out, e := h.Service.Logs(r.Context(), a, node)
		return out, 200, e
	}
	if workspace == "" && len(parts) == index+4 && parts[index+3] == "actions" && r.Method == "POST" {
		var input components.Request
		if e := httpserver.DecodeJSON(r, &input); e != nil {
			return nil, 0, e
		}
		keys := r.Header.Values("Idempotency-Key")
		if len(keys) != 1 || strings.TrimSpace(input.Reason) == "" || utf8.RuneCountInString(input.Reason) > 500 {
			return nil, 0, bad
		}
		out, e := h.Service.RequestDetailed(r.Context(), a, node, input, keys[0], request)
		return out, 202, e
	}
	return nil, 0, errors.New("RESOURCE_NOT_FOUND")
}
