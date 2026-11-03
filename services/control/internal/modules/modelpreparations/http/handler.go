package http

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

type Handler struct{ Service *modelpreparations.Service }

func Owns(path string) bool {
	if PreparationOwns(path) {
		return true
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	return len(parts) >= 4 && parts[0] == "api" && parts[1] == "v1" && (parts[2] == "admin" && parts[3] == "model-holdouts" || len(parts) >= 5 && parts[2] == "workspaces" && parts[4] == "model-holdouts")
}

func decode(r *http.Request, out any) error {
	media, parameters, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || media != "application/json" || len(parameters) > 1 || len(parameters) == 1 && parameters["charset"] != "utf-8" || len(r.Header.Values("Content-Type")) != 1 {
		return errors.New("UNSUPPORTED_MEDIA_TYPE")
	}
	raw, e := io.ReadAll(io.LimitReader(r.Body, httpserver.MaxJSONBytes+1))
	if e != nil {
		return errors.New("VALIDATION_FAILED")
	}
	if len(raw) > httpserver.MaxJSONBytes {
		return errors.New("PAYLOAD_TOO_LARGE")
	}
	if wire.DecodeStrict(raw, out) != nil {
		return errors.New("VALIDATION_FAILED")
	}
	return nil
}

func (h *Handler) Handle(r *http.Request, a identity.Auth, request string) (any, int, error) {
	if PreparationOwns(r.URL.Path) {
		return h.preparations(r, a, request)
	}
	missing, bad := errors.New("RESOURCE_NOT_FOUND"), errors.New("VALIDATION_FAILED")
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if !Owns(r.URL.Path) || r.URL.Path != "/"+strings.Join(parts, "/") {
		return nil, 0, missing
	}
	if h.Service == nil {
		return nil, 0, errors.New("MODEL_HOLDOUT_UNAVAILABLE")
	}
	query := r.URL.Query()
	workspace := ""
	var tail []string
	admin := parts[2] == "admin"
	if admin {
		if a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin {
			return nil, 0, missing
		}
		tail = parts[4:]
	} else {
		if a.Me.Audience != "portal" {
			return nil, 0, missing
		}
		workspace, tail = parts[3], parts[5:]
		if !datawire.ValidID(workspace) {
			return nil, 0, bad
		}
	}
	create := len(tail) == 0 && r.Method == http.MethodPost
	if admin && !create {
		values := query["workspace_id"]
		if len(values) != 1 || !datawire.ValidID(values[0]) {
			return nil, 0, bad
		}
		workspace = values[0]
		query.Del("workspace_id")
	}
	if create {
		if len(query) != 0 {
			return nil, 0, bad
		}
		var input modelpreparations.HoldoutRequest
		if e := decode(r, &input); e != nil {
			return nil, 0, e
		}
		if !admin && workspace != input.WorkspaceID {
			return nil, 0, bad
		}
		keys := r.Header.Values("Idempotency-Key")
		if len(keys) != 1 || !datawire.ValidID(keys[0]) {
			return nil, 0, bad
		}
		out, e := h.Service.ReserveHoldout(r.Context(), a, input, keys[0], request)
		return out, 201, e
	}
	if r.Method != http.MethodGet {
		return nil, 0, missing
	}
	if len(tail) == 0 {
		for name, values := range query {
			if len(values) != 1 || name != "limit" && name != "cursor" {
				return nil, 0, bad
			}
		}
		out, e := h.Service.ListHoldouts(r.Context(), a, workspace, query)
		return out, 200, e
	}
	if len(query) != 0 {
		return nil, 0, bad
	}
	if len(tail) == 1 && tail[0] == "capabilities" {
		out, e := h.Service.HoldoutCapabilities(r.Context(), a, workspace)
		return out, 200, e
	}
	if len(tail) == 1 && datawire.ValidID(tail[0]) {
		out, e := h.Service.GetHoldout(r.Context(), a, workspace, tail[0])
		return out, 200, e
	}
	return nil, 0, missing
}
