package http

import (
	"errors"
	"net/http"
	"net/url"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/preferences"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
)

type Handler struct{ Service *preferences.Service }

func Owns(path string) bool { return path == "/api/v1/preferences" }
func (h *Handler) Handle(r *http.Request, a identity.Auth, request string) (any, int, error) {
	missing := errors.New("RESOURCE_NOT_FOUND")
	if !Owns(r.URL.Path) || r.URL.RawPath != "" {
		return nil, 0, missing
	}
	query, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil || len(query) != 0 {
		return nil, 0, errors.New("VALIDATION_FAILED")
	}
	switch r.Method {
	case http.MethodGet:
		v, e := h.Service.Get(r.Context(), a)
		return v, 200, e
	case http.MethodPatch:
		var input preferences.UpdateRequest
		if e := httpserver.DecodeRequiredObject(r, &input, httpserver.MaxJSONBytes); e != nil {
			return nil, 0, e
		}
		v, e := h.Service.Update(r.Context(), a, input, request)
		return v, 200, e
	default:
		return nil, 0, missing
	}
}
