// Package http adapts authenticated scheduling reads to the versioned entrances.
// The identity owner retains authentication, response encoding and composition.
package http

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/scheduling"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
)

// Handler borrows the installed Workspace authority; it owns no authentication.
type Handler struct {
	Workspaces *workspaces.Service
}

// Owns identifies scheduling paths before the general Workspace dispatcher.
func Owns(path string) bool {
	parts := strings.Split(path, "/")
	return len(parts) >= 5 && parts[0] == "" && parts[1] == "api" && parts[2] == "v1" &&
		(parts[3] == "admin" && parts[4] == "scheduling" ||
			len(parts) >= 6 && parts[3] == "workspaces" && parts[5] == "scheduling")
}

// Handle reads a quota status for a server-authenticated actor. The composition
// owner encodes only a successful result and applies its standard error envelope.
func (h *Handler) Handle(w http.ResponseWriter, r *http.Request, actor identity.Auth, _ string) (any, int, error) {
	w.Header().Set("Cache-Control", "no-store")
	missing := errors.New("RESOURCE_NOT_FOUND")
	bad := errors.New("VALIDATION_FAILED")
	if !Owns(r.URL.Path) || r.URL.EscapedPath() != r.URL.Path || r.Method != http.MethodGet {
		return nil, 0, missing
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || r.ContentLength != 0 || len(r.TransferEncoding) != 0 ||
		r.Body != nil && r.Body != http.NoBody {
		return nil, 0, bad
	}
	parts := strings.Split(r.URL.Path, "/")
	var workspace string
	if parts[3] == "admin" {
		if actor.Me.Audience != "admin" || !actor.Me.User.IsPlatformAdmin {
			return nil, 0, missing
		}
		if len(parts) != 6 || parts[5] != "quota" {
			return nil, 0, missing
		}
		ids := query["workspace_id"]
		if len(query) != 1 || len(ids) != 1 || !workspaces.ValidID(ids[0]) {
			return nil, 0, bad
		}
		workspace = ids[0]
	} else {
		if actor.Me.Audience != "portal" {
			return nil, 0, missing
		}
		if len(parts) != 7 || parts[6] != "quota" {
			return nil, 0, missing
		}
		if len(query) != 0 || !workspaces.ValidID(parts[4]) {
			return nil, 0, bad
		}
		workspace = parts[4]
	}
	if h == nil {
		return nil, 0, errors.New("STORAGE_UNAVAILABLE")
	}
	out, err := scheduling.ReadWorkspaceQuotaStatus(r.Context(), h.Workspaces, actor, workspace)
	if err != nil {
		if errors.Is(err, scheduling.ErrQuotaPolicyUnavailable) || errors.Is(err, scheduling.ErrReservationTransaction) {
			return nil, 0, errors.New("STORAGE_UNAVAILABLE")
		}
		return nil, 0, err
	}
	return out, http.StatusOK, nil
}
