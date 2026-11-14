package http

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/cloudinventory"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
)

type Handler struct{ Service *cloudinventory.Service }

func route(path string) ([]string, bool) {
	path = strings.TrimPrefix(path, "/api/v1/")
	path = strings.TrimPrefix(path, "admin/")
	p := strings.Split(path, "/")
	return p, len(p) >= 3 && p[0] == "workspaces" && p[2] == "aws"
}
func Owns(path string) bool {
	if !strings.HasPrefix(path, "/api/v1/") {
		return false
	}
	_, ok := route(path)
	return ok
}
func (h *Handler) Handle(r *http.Request, a identity.Auth, request string) (any, int, error) {
	bad := errors.New("VALIDATION_FAILED")
	p, ok := route(r.URL.Path)
	if !ok {
		return nil, 0, errors.New("RESOURCE_NOT_FOUND")
	}
	admin := strings.HasPrefix(r.URL.Path, "/api/v1/admin/")
	if admin != (a.Me.Audience == "admin") {
		return nil, 0, errors.New("ACCESS_DENIED")
	}
	if len(p) == 3 && r.Method == "GET" {
		query := r.URL.Query()
		for key, v := range query {
			if key != "after" && key != "limit" || len(v) != 1 {
				return nil, 0, bad
			}
		}
		limit := h.Service.Identity.Config.Policy().UI.PageSize
		if query.Has("limit") {
			var e error
			limit, e = strconv.Atoi(query.Get("limit"))
			if e != nil {
				return nil, 0, bad
			}
		}
		out, e := h.Service.List(r.Context(), a, p[1], query.Get("after"), limit)
		return out, 200, e
	}
	if r.URL.RawQuery != "" {
		return nil, 0, bad
	}
	if len(p) == 4 && p[3] == "imports" && r.Method == "POST" {
		var input cloudinventory.ImportInput
		if e := httpserver.DecodeRequiredObject(r, &input, httpserver.MaxJSONBytes); e != nil {
			return nil, 0, e
		}
		out, e := h.Service.Import(r.Context(), a, p[1], input, request)
		return out, 200, e
	}
	if len(p) == 5 && p[3] == "imports" && r.Method == "GET" {
		out, e := h.Service.Receipt(r.Context(), a, p[1], p[4])
		return out, 200, e
	}
	return nil, 0, errors.New("RESOURCE_NOT_FOUND")
}
