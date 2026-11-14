// Package http is mounted behind the original Admin authentication/Origin policy.
package http

import (
	"errors"
	"net/http"
	"net/url"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/monitoring"
)

const Path = "/api/v1/admin/monitoring"

type Handler struct{ Service *monitoring.Service }

func Owns(path string) bool { return path == Path }
func (h *Handler) Handle(r *http.Request, a identity.Auth, requestID string) (any, int, error) {
	bad := errors.New("VALIDATION_FAILED")
	if h == nil || h.Service == nil {
		return nil, 0, errors.New("MONITORING_OWNER_REQUIRED")
	}
	if r.Method != http.MethodGet || r.URL.Path != Path {
		return nil, 0, errors.New("RESOURCE_NOT_FOUND")
	}
	if a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin {
		return nil, 0, errors.New("ACCESS_DENIED")
	}
	if r.URL.RawPath != "" || r.ContentLength != 0 || len(r.TransferEncoding) > 0 {
		return nil, 0, bad
	}
	q, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil || len(q["workspace_id"]) != 1 {
		return nil, 0, bad
	}
	for k, v := range q {
		if len(v) != 1 || k != "workspace_id" && k != "node_cursor" || k == "node_cursor" && v[0] == "" {
			return nil, 0, bad
		}
	}
	out, e := h.Service.Get(r.Context(), a, q.Get("workspace_id"), q.Get("node_cursor"), requestID)
	if e != nil {
		return nil, 0, e
	}
	return out, http.StatusOK, nil
}
