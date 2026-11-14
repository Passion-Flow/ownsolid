package http

import (
	"errors"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/preferences"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type Handler struct{ Service *preferences.Service }

const (
	tenantSettingsPath = "/api/v1/admin/tenant-settings"
	savedViewsPath     = "/api/v1/saved-views"
)

// Owns reports the paths this handler serves. The dispatcher keeps /api/v1/admin/ to the
// admin origin.
func Owns(path string) bool {
	return path == "/api/v1/preferences" || path == tenantSettingsPath || path == savedViewsPath || strings.HasPrefix(path, savedViewsPath+"/")
}
func (h *Handler) Handle(r *http.Request, a identity.Auth, request string) (any, int, error) {
	missing := errors.New("RESOURCE_NOT_FOUND")
	if !Owns(r.URL.Path) || r.URL.RawPath != "" {
		return nil, 0, missing
	}
	query, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil {
		return nil, 0, errors.New("VALIDATION_FAILED")
	}
	if r.URL.Path == savedViewsPath || strings.HasPrefix(r.URL.Path, savedViewsPath+"/") {
		return h.savedViews(r, a, query)
	}
	if len(query) != 0 {
		return nil, 0, errors.New("VALIDATION_FAILED")
	}
	if r.URL.Path == tenantSettingsPath {
		return h.tenantSettings(r, a, request)
	}
	switch r.Method {
	case http.MethodGet:
		v, e := h.Service.Get(r.Context(), a)
		return v, 200, e
	case http.MethodPatch:
		var input preferences.UpdateRequest
		if e := httpserver.DecodePatchObject(r, &input, httpserver.MaxJSONBytes); e != nil {
			return nil, 0, e
		}
		v, e := h.Service.Update(r.Context(), a, input, request)
		return v, 200, e
	default:
		return nil, 0, missing
	}
}

func (h *Handler) tenantSettings(r *http.Request, a identity.Auth, request string) (any, int, error) {
	switch r.Method {
	case http.MethodGet:
		v, e := h.Service.GetTenantSettings(r.Context(), a)
		return v, 200, e
	case http.MethodPatch:
		var input preferences.TenantSettingsUpdate
		if e := httpserver.DecodeRequiredObject(r, &input, httpserver.MaxJSONBytes); e != nil {
			return nil, 0, e
		}
		v, e := h.Service.UpdateTenantSettings(r.Context(), a, input, request)
		return v, 200, e
	default:
		return nil, 0, errors.New("RESOURCE_NOT_FOUND")
	}
}

// savedViews serves GET /api/v1/saved-views?list=, PUT /api/v1/saved-views/{id} and
// DELETE /api/v1/saved-views/{id}?revision=.
func (h *Handler) savedViews(r *http.Request, a identity.Auth, query url.Values) (any, int, error) {
	bad, missing := errors.New("VALIDATION_FAILED"), errors.New("RESOURCE_NOT_FOUND")
	only := func(name string) (string, bool) {
		values, ok := query[name]
		return query.Get(name), len(query) == 1 && ok && len(values) == 1
	}
	if r.URL.Path == savedViewsPath {
		list, ok := only("list")
		if r.Method != http.MethodGet {
			return nil, 0, missing
		}
		if !ok {
			return nil, 0, bad
		}
		items, e := h.Service.ListViews(r.Context(), a, list)
		return map[string]any{"items": items}, 200, e
	}
	id := strings.TrimPrefix(r.URL.Path, savedViewsPath+"/")
	switch r.Method {
	case http.MethodPut:
		if len(query) != 0 {
			return nil, 0, bad
		}
		var input preferences.SavedViewInput
		if e := httpserver.DecodeRequiredObject(r, &input, httpserver.MaxJSONBytes); e != nil {
			return nil, 0, e
		}
		v, e := h.Service.PutView(r.Context(), a, id, input)
		return v, 200, e
	case http.MethodDelete:
		value, ok := only("revision")
		revision, e := strconv.ParseInt(value, 10, 64)
		if !ok || e != nil || strconv.FormatInt(revision, 10) != value {
			return nil, 0, bad
		}
		return nil, 204, h.Service.DeleteView(r.Context(), a, id, revision)
	default:
		return nil, 0, missing
	}
}
