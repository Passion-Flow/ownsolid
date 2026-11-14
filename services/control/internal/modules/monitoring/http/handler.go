// Package http is mounted behind the original Admin authentication/Origin policy.
package http

import (
	"errors"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/monitoring"
	"net/http"
	"net/url"
)

const Path = "/api/v1/admin/monitoring"

type Handler struct{ Service *monitoring.Service }

func Owns(path string) bool { return path == Path || path == FleetHistoryPath }
func (h *Handler) Handle(r *http.Request, a identity.Auth, requestID string) (any, int, error) {
	if r != nil && r.URL != nil && r.URL.Path == FleetHistoryPath {
		body, status, err := h.HandleFleetHistory(r, a, requestID)
		if err != nil {
			return nil, 0, err
		}
		return body, status, nil
	}
	if err := h.checkRead(r, a, Path); err != nil {
		return nil, 0, err
	}
	bad := errors.New("VALIDATION_FAILED")
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

func (h *Handler) checkRead(r *http.Request, a identity.Auth, path string) error {
	if h == nil || h.Service == nil {
		return errors.New("MONITORING_OWNER_REQUIRED")
	}
	if r == nil || r.URL == nil || r.Method != http.MethodGet || r.URL.Path != path {
		return errors.New("RESOURCE_NOT_FOUND")
	}
	if a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin {
		return errors.New("ACCESS_DENIED")
	}
	if r.URL.RawPath != "" || r.ContentLength != 0 || len(r.TransferEncoding) > 0 {
		return errors.New("VALIDATION_FAILED")
	}
	return nil
}
