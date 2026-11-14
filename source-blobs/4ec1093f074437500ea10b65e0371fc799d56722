package http

import (
	"errors"
	"net/http"
	"net/url"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/notifications"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	strict "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"strings"
)

type Handler struct{ Service *notifications.Service }

const base = "/api/v1/notifications"

func Owns(path string) bool { return path == base || strings.HasPrefix(path, base+"/") }
func (h *Handler) Handle(r *http.Request, a identity.Auth, request string) (any, int, error) {
	missing := errors.New("RESOURCE_NOT_FOUND")
	bad := errors.New("VALIDATION_FAILED")
	if !Owns(r.URL.Path) || r.URL.RawPath != "" {
		return nil, 0, missing
	}
	query, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil {
		return nil, 0, bad
	}
	if r.URL.Path == base && r.Method == http.MethodGet {
		v, e := h.Service.List(r.Context(), a, query)
		return v, 200, e
	}
	if len(query) != 0 {
		return nil, 0, bad
	}
	if r.URL.Path == base+"/configuration" && r.Method == http.MethodGet {
		v, e := h.Service.Configuration(r.Context(), a)
		return v, 200, e
	}
	tail := strings.Split(strings.TrimPrefix(r.URL.Path, base+"/"), "/")
	if len(tail) == 2 && strict.ValidID(tail[0]) && tail[1] == "read" && r.Method == http.MethodPost {
		var input struct{}
		if e := httpserver.DecodeRequiredObject(r, &input, httpserver.MaxJSONBytes); e != nil {
			return nil, 0, e
		}
		v, e := h.Service.Read(r.Context(), a, tail[0], request)
		return v, 200, e
	}
	return nil, 0, missing
}
