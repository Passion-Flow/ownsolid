package http

import (
	"errors"
	strict "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/applications"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"net/http"
	"net/url"
)

type Handler struct{ Service *applications.Service }

func Owns(path string) bool {
	return path == "/api/v1/applications" || path == "/api/v1/application-conversations"
}
func (h *Handler) Handle(r *http.Request, a identity.Auth, _ string) (any, int, error) {
	missing := errors.New("RESOURCE_NOT_FOUND")
	if !Owns(r.URL.Path) || r.URL.RawPath != "" || r.Method != http.MethodGet || a.Me.Audience != "portal" {
		return nil, 0, missing
	}
	query, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil || len(query["workspace_id"]) != 1 || !strict.ValidID(query.Get("workspace_id")) {
		return nil, 0, errors.New("VALIDATION_FAILED")
	}
	workspace := query.Get("workspace_id")
	query.Del("workspace_id")
	if r.URL.Path == "/api/v1/applications" {
		v, e := h.Service.List(r.Context(), a, workspace, query)
		return v, 200, e
	}
	v, e := h.Service.Conversations(r.Context(), a, workspace, query)
	return v, 200, e
}
