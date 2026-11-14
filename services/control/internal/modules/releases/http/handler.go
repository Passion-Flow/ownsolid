package http

import (
	"errors"
	"net/http"
	"net/url"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/releases"
)

const CurrentPath = "/api/v1/admin/releases/current"
const HistoryPath = "/api/v1/admin/releases/history"

type Handler struct{ Service *releases.Service }

func Owns(path string) bool { return path == CurrentPath || path == HistoryPath }
func (h *Handler) Handle(r *http.Request, a identity.Auth, requestID string) (any, int, error) {
	if a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin {
		return nil, 0, errors.New("ACCESS_DENIED")
	}
	if !Owns(r.URL.Path) || r.Method != http.MethodGet {
		return nil, 0, errors.New("RESOURCE_NOT_FOUND")
	}
	if r.URL.RawPath != "" || r.ContentLength != 0 || len(r.TransferEncoding) > 0 {
		return nil, 0, errors.New("VALIDATION_FAILED")
	}
	q, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil {
		return nil, 0, errors.New("VALIDATION_FAILED")
	}
	for k, v := range q {
		if r.URL.Path != HistoryPath || k != "cursor" || len(v) != 1 || v[0] == "" || len(v[0]) > 1024 {
			return nil, 0, errors.New("VALIDATION_FAILED")
		}
	}
	if h == nil || h.Service == nil {
		return nil, 0, errors.New("RELEASE_VIEW_UNAVAILABLE")
	}
	if r.URL.Path == CurrentPath {
		v, e := h.Service.Current(r.Context(), a, requestID)
		return v, http.StatusOK, e
	}
	v, e := h.Service.History(r.Context(), a, q.Get("cursor"), requestID)
	return v, http.StatusOK, e
}
