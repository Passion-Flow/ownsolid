// Package http serves the administrator routes of cell clusters below
// /api/v1/admin/cell-clusters. The parent router authenticates, checks the origin and
// CSRF token, and admits the request before it reaches this package.
package http

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/cellclusters"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"github.com/google/uuid"
)

// Root is the path of the cell cluster collection.
const Root = "/api/v1/admin/cell-clusters"

// Handler serves the routes. A nil Service answers KUBERNETES_CELLS_UNCONFIGURED.
type Handler struct{ Service *cellclusters.Service }

// Owns reports whether path is one of this package's routes.
func Owns(path string) bool { return path == Root || strings.HasPrefix(path, Root+"/") }

func id(value string) bool {
	parsed, e := uuid.Parse(value)
	return e == nil && parsed.String() == value
}

// Handle serves one request:
//
//	GET  /cell-clusters                                list this cell's clusters
//	POST /cell-clusters                                create the cell's cluster (Idempotency-Key)
//	GET  /cell-clusters/{id}                           read a cluster
//	GET  /cell-clusters/{id}/members                   page through members (state, limit, cursor)
//	POST /cell-clusters/{id}/members                   add workers
//	POST /cell-clusters/{id}/members/{node}/retry      join a failed member again
//	POST /cell-clusters/{id}/observations              ask settled members for their readiness
func (h *Handler) Handle(r *http.Request, a identity.Auth, request string) (any, int, error) {
	bad := errors.New("VALIDATION_FAILED")
	missing := errors.New("RESOURCE_NOT_FOUND")
	if a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin {
		return nil, 0, errors.New("ACCESS_DENIED")
	}
	if r.URL.RawPath != "" {
		return nil, 0, bad
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, Root), "/")[1:]
	if r.URL.Path == Root {
		parts = nil
	}
	if len(parts) > 0 && !id(parts[0]) {
		return nil, 0, missing
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		return nil, 0, missing
	}
	if r.Method == http.MethodPost && r.URL.RawQuery != "" || r.Method == http.MethodGet && r.URL.RawQuery != "" && !(len(parts) == 2 && parts[1] == "members") {
		return nil, 0, bad
	}
	s := h.Service
	switch {
	case len(parts) == 0 && r.Method == http.MethodGet:
		out, e := s.List(r.Context(), a)
		return out, 200, e
	case len(parts) == 0:
		keys := r.Header.Values("Idempotency-Key")
		if len(keys) != 1 || !id(keys[0]) {
			return nil, 0, bad
		}
		var input cellclusters.CreateInput
		if e := httpserver.DecodeJSON(r, &input); e != nil {
			return nil, 0, e
		}
		out, e := s.Create(r.Context(), a, input, keys[0], request)
		return out, 201, e
	case len(parts) == 1 && r.Method == http.MethodGet:
		out, e := s.Get(r.Context(), a, parts[0])
		return out, 200, e
	case len(parts) == 2 && parts[1] == "members" && r.Method == http.MethodGet:
		out, e := s.Members(r.Context(), a, parts[0], r.URL.Query())
		return out, 200, e
	case len(parts) == 2 && parts[1] == "members":
		var input cellclusters.AddWorkersInput
		if e := httpserver.DecodeJSON(r, &input); e != nil {
			return nil, 0, e
		}
		out, e := s.AddWorkers(r.Context(), a, parts[0], input, request)
		return out, 200, e
	case len(parts) == 4 && parts[1] == "members" && id(parts[2]) && parts[3] == "retry" && r.Method == http.MethodPost:
		var input cellclusters.ReasonInput
		if e := httpserver.DecodeJSON(r, &input); e != nil {
			return nil, 0, e
		}
		out, e := s.Retry(r.Context(), a, parts[0], parts[2], input, request)
		return out, 200, e
	case len(parts) == 2 && parts[1] == "observations" && r.Method == http.MethodPost:
		var input cellclusters.ReasonInput
		if e := httpserver.DecodeJSON(r, &input); e != nil {
			return nil, 0, e
		}
		out, e := s.Observe(r.Context(), a, parts[0], input, request)
		return out, 202, e
	}
	return nil, 0, missing
}
