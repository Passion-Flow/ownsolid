// Package http adapts authenticated browser requests to deterministic assistant commands.
package http

import (
	"errors"
	"net/http"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/productagent"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
)

const (
	commandPath = "/api/v1/product-assistant/commands"
	// Allow JSON escaping and framing around the command and pagination cursor.
	maxCommandBody = 4 << 10
)

type commandRequest struct {
	Command string `json:"command"`
	Cursor  string `json:"cursor,omitempty"`
}

// Handler runs assistant commands using the authenticated identity dispatcher.
type Handler struct{ Service *productagent.Service }

// Owns reports whether the normalized API path belongs to the assistant adapter.
func Owns(path string) bool { return path == commandPath }

// Handle expects the dispatcher's authenticated actor, verified CSRF context,
// and server-generated request ID. Errors carry no response data.
func (h *Handler) Handle(r *http.Request, actor identity.Auth, requestID string) (any, int, error) {
	if r == nil || r.URL == nil || !Owns(r.URL.Path) || r.URL.RawPath != "" || r.Method != http.MethodPost {
		return nil, 0, errors.New("RESOURCE_NOT_FOUND")
	}
	if actor.SessionID == "" || actor.Me.Audience != "admin" && actor.Me.Audience != "portal" {
		return nil, 0, errors.New("AUTH_REQUIRED")
	}
	if h == nil || h.Service == nil {
		return nil, 0, errors.New("SERVICE_NOT_READY")
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		return nil, 0, errors.New("VALIDATION_FAILED")
	}
	var command commandRequest
	if err := httpserver.DecodePatchObject(r, &command, maxCommandBody); err != nil {
		return nil, 0, err
	}
	result, err := h.Service.RunForRequest(r.Context(), actor, command.Command, command.Cursor, requestID)
	if err != nil {
		switch {
		case errors.Is(err, productagent.ErrInvalidCommand), errors.Is(err, productagent.ErrUnknownCommand):
			err = errors.New("VALIDATION_FAILED")
		case errors.Is(err, productagent.ErrWorkspaceRequired):
			err = errors.New("ACCESS_DENIED")
		case errors.Is(err, productagent.ErrInvalidRequestID):
			err = errors.New("INTERNAL_ERROR")
		}
		return nil, 0, err
	}
	return result, http.StatusOK, nil
}
