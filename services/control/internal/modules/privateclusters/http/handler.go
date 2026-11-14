// Package http exports routes for the original authenticated Origin/CSRF entrances.
// Aggregate server/router wiring is parent-owned; this package grants no bypass.
package http

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/privateclusters"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
)

const adminRoot = "/api/v1/admin/private-clusters"

type Handler struct{ Service *privateclusters.Service }

func Owns(path string) bool {
	if path == adminRoot || strings.HasPrefix(path, adminRoot+"/") {
		return true
	}
	p := strings.Split(strings.TrimPrefix(path, "/api/v1/workspaces/"), "/")
	return strings.HasPrefix(path, "/api/v1/workspaces/") && len(p) >= 2 && p[1] == "private-clusters"
}
func (h *Handler) Handle(r *http.Request, a identity.Auth, request string) (any, int, error) {
	out, status, e := h.handle(r, a, request)
	if e != nil {
		return nil, 0, e
	}
	if e = h.Service.CheckPublicResponse(out); e != nil {
		return nil, 0, e
	}
	return out, status, nil
}

func (h *Handler) handle(r *http.Request, a identity.Auth, request string) (any, int, error) {
	bad := errors.New("VALIDATION_FAILED")
	missing := errors.New("RESOURCE_NOT_FOUND")
	if h == nil || h.Service == nil {
		return nil, 0, errors.New("PRIVATE_CLUSTER_OWNER_REQUIRED")
	}
	if r.URL.RawPath != "" {
		return nil, 0, bad
	}
	query, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil {
		return nil, 0, bad
	}
	admin := r.URL.Path == adminRoot || strings.HasPrefix(r.URL.Path, adminRoot+"/")
	workspace := ""
	path := ""
	if admin {
		if a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin {
			return nil, 0, errors.New("ACCESS_DENIED")
		}
		if len(query["workspace_id"]) != 1 {
			return nil, 0, bad
		}
		workspace = query.Get("workspace_id")
		path = strings.TrimPrefix(r.URL.Path, adminRoot)
	} else {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/workspaces/"), "/")
		if len(parts) < 2 || parts[1] != "private-clusters" || a.Me.Audience != "portal" {
			return nil, 0, missing
		}
		workspace = parts[0]
		path = "/" + strings.Join(parts[2:], "/")
	}
	for key, values := range query {
		if len(values) != 1 || key != "cursor" && (!admin || key != "workspace_id") {
			return nil, 0, bad
		}
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if r.Method == http.MethodGet {
		if admin && path == "/profiles" && query.Get("cursor") == "" {
			out, e := h.Service.Profiles(r.Context(), a, workspace)
			return out, 200, e
		}
		if path == "/connections" {
			out, e := h.Service.List(r.Context(), a, workspace, query.Get("cursor"))
			return out, 200, e
		}
		if len(parts) >= 2 && parts[0] == "connections" {
			connection := parts[1]
			if len(parts) == 3 && parts[2] == "runs" {
				out, e := h.Service.Runs(r.Context(), a, workspace, connection, query.Get("cursor"))
				return out, 200, e
			}
			if query.Get("cursor") != "" {
				return nil, 0, bad
			}
			if len(parts) == 2 {
				out, e := h.Service.Get(r.Context(), a, workspace, connection)
				return out, 200, e
			}
			if len(parts) == 4 && parts[2] == "runs" {
				out, e := h.Service.GetRun(r.Context(), a, workspace, connection, parts[3])
				return out, 200, e
			}
			if len(parts) == 5 && parts[2] == "runs" && parts[4] == "proof" {
				out, e := h.Service.Proof(r.Context(), a, workspace, connection, parts[3])
				return out, 200, e
			}
		}
		return nil, 0, missing
	}
	if r.Method != http.MethodPost || query.Get("cursor") != "" {
		return nil, 0, missing
	}
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 {
		return nil, 0, bad
	}
	body, e := decodeMutation(r, path == "/connections" && admin)
	if e != nil {
		return nil, 0, e
	}
	m := privateclusters.Mutation{WorkspaceID: workspace, Key: keys[0], RequestID: request, Reason: body.Reason, ExpectedRevision: body.ExpectedRevision}
	if path == "/connections" && admin {
		out, e := h.Service.Bind(r.Context(), a, m, body.ProfileID)
		return out, 201, e
	}
	if body.ProfileID != "" {
		return nil, 0, bad
	}
	if len(parts) == 3 && parts[0] == "connections" {
		if parts[2] == "runs" {
			out, e := h.Service.Start(r.Context(), a, m, parts[1])
			return out, 201, e
		}
		if !admin {
			return nil, 0, missing
		}
		out, e := h.Service.Change(r.Context(), a, m, parts[1], parts[2])
		return out, 200, e
	}
	if len(parts) == 5 && parts[0] == "connections" && parts[2] == "runs" {
		out, e := h.Service.Action(r.Context(), a, m, parts[1], parts[3], parts[4])
		return out, 200, e
	}
	return nil, 0, missing
}

// The binding and action envelopes have distinct complete required fields.
// No optional zero value or case-insensitive JSON alias can create authority.
type mutationBody struct {
	ProfileID        string
	Reason           string
	ExpectedRevision int64
}

func decodeMutation(r *http.Request, binding bool) (mutationBody, error) {
	if binding {
		var v struct {
			ProfileID        string `json:"profile_id"`
			Reason           string `json:"reason"`
			ExpectedRevision int64  `json:"expected_revision"`
		}
		if e := httpserver.DecodeRequiredObject(r, &v, httpserver.MaxJSONBytes); e != nil {
			return mutationBody{}, e
		}
		return mutationBody{v.ProfileID, v.Reason, v.ExpectedRevision}, nil
	}
	var v struct {
		Reason           string `json:"reason"`
		ExpectedRevision int64  `json:"expected_revision"`
	}
	if e := httpserver.DecodeRequiredObject(r, &v, httpserver.MaxJSONBytes); e != nil {
		return mutationBody{}, e
	}
	return mutationBody{Reason: v.Reason, ExpectedRevision: v.ExpectedRevision}, nil
}
