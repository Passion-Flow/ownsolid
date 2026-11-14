package http

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
)

const browserSessionsPath = "/api/v1/me/sessions"

func (h *Handler) browserSessions(w http.ResponseWriter, r *http.Request, cookies httpserver.CookiePolicy, actor identity.Auth, requestID string) (any, int, error) {
	if r.URL.Path == browserSessionsPath && r.Method == http.MethodGet {
		query, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			return nil, 0, errors.New("VALIDATION_FAILED")
		}
		page, err := h.Service.ListBrowserSessions(r.Context(), actor, query)
		return page, http.StatusOK, err
	}
	id, found := strings.CutPrefix(r.URL.Path, browserSessionsPath+"/")
	if !found || r.Method != http.MethodDelete || strings.Contains(id, "/") {
		return nil, 0, errors.New("RESOURCE_NOT_FOUND")
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1))
	if err != nil || len(body) != 0 || r.URL.RawQuery != "" {
		return nil, 0, errors.New("VALIDATION_FAILED")
	}
	if err := h.Service.RevokeBrowserSession(r.Context(), actor, id, requestID); err != nil {
		return nil, 0, err
	}
	if id == actor.SessionID {
		cookies.ClearIdentity(w, actor.Me.Audience)
	}
	return nil, http.StatusNoContent, nil
}
