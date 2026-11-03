package http

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
)

type Handler struct{ Service *workspaces.Service }

func validID(id string) bool { return workspaces.ValidID(id) }
func version(v int64) bool   { return workspaces.ValidVersion(v) }
func reason(v string) bool   { return workspaces.ValidReason(v) }
func (h *Handler) Handle(w http.ResponseWriter, r *http.Request, a identity.Auth, requestID string) (any, int, error) {
	path := r.URL.Path
	isAdmin := strings.HasPrefix(path, "/api/v1/admin/workspaces")
	if isAdmin != (a.Me.Audience == "admin") {
		return nil, 0, errors.New("RESOURCE_NOT_FOUND")
	}
	if isAdmin && !a.Me.User.IsPlatformAdmin {
		return nil, 0, errors.New("ACCESS_DENIED")
	}
	prefix := "/api/v1/workspaces"
	if isAdmin {
		prefix = "/api/v1/admin/workspaces"
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	if len(parts) > 0 && parts[0] == "" {
		parts = parts[1:]
	}
	s := h.Service
	ctx := r.Context()
	bad := errors.New("VALIDATION_FAILED")
	if len(parts) == 2 && parts[0] == "operations" && r.Method == "GET" {
		w.Header().Set("Cache-Control", "no-store")
		v, e := s.Operation(ctx, a, parts[1])
		return v, http.StatusOK, e
	}
	if len(parts) == 0 {
		if r.Method == "GET" {
			v, e := s.List(ctx, a, r.URL.Query())
			return v, 200, e
		}
		if r.Method == "POST" && isAdmin {
			var v struct {
				Name    string `json:"name"`
				OwnerID string `json:"owner_user_id"`
			}
			if e := httpserver.DecodeJSON(r, &v); e != nil {
				return nil, 0, e
			}
			if !validID(v.OwnerID) {
				return nil, 0, bad
			}
			result, e := s.Create(ctx, a, v.Name, v.OwnerID, requestID)
			return result, 201, e
		}
		return nil, 0, errors.New("RESOURCE_NOT_FOUND")
	}
	id := parts[0]
	if !validID(id) {
		return nil, 0, bad
	}
	if len(parts) == 1 {
		if r.Method == "GET" {
			v, e := s.Get(ctx, a, id, false)
			return v, 200, e
		}
		if r.Method == "PATCH" {
			var v struct {
				Name    string `json:"name"`
				Status  string `json:"status"`
				Version int64  `json:"version"`
				Reason  string `json:"reason"`
			}
			if e := httpserver.DecodeJSON(r, &v); e != nil {
				return nil, 0, e
			}
			if !version(v.Version) || !reason(v.Reason) {
				return nil, 0, bad
			}
			result, e := s.Update(ctx, a, id, v.Name, v.Status, v.Version, v.Reason, requestID)
			return result, 200, e
		}
	}
	if len(parts) >= 2 && parts[1] == "members" {
		if len(parts) == 2 {
			if r.Method == "GET" {
				v, e := s.Members(ctx, a, id, r.URL.Query())
				return v, 200, e
			}
			if r.Method == "POST" {
				var v struct {
					Email string `json:"email"`
					Role  string `json:"role"`
				}
				if e := httpserver.DecodeJSON(r, &v); e != nil {
					return nil, 0, e
				}
				result, e := s.AddMember(ctx, a, id, v.Email, v.Role, requestID)
				return result, 201, e
			}
		}
		if len(parts) == 3 {
			if !validID(parts[2]) {
				return nil, 0, bad
			}
			if r.Method == "PATCH" {
				var v struct {
					Role    string `json:"role"`
					Version int64  `json:"version"`
					Reason  string `json:"reason"`
				}
				if e := httpserver.DecodeJSON(r, &v); e != nil {
					return nil, 0, e
				}
				if !version(v.Version) || !reason(v.Reason) {
					return nil, 0, bad
				}
				result, e := s.ChangeMember(ctx, a, id, parts[2], v.Role, v.Version, v.Reason, requestID, false)
				return result, 200, e
			}
			if r.Method == "DELETE" {
				var v struct {
					Version int64  `json:"version"`
					Reason  string `json:"reason"`
				}
				if e := httpserver.DecodeJSON(r, &v); e != nil {
					return nil, 0, e
				}
				if !version(v.Version) || !reason(v.Reason) {
					return nil, 0, bad
				}
				_, e := s.ChangeMember(ctx, a, id, parts[2], "", v.Version, v.Reason, requestID, true)
				return nil, 204, e
			}
		}
	}
	if len(parts) >= 2 && parts[1] == "notes" && !isAdmin {
		if len(parts) == 2 {
			if r.Method == "GET" {
				v, e := s.Notes(ctx, a, id, r.URL.Query())
				return v, 200, e
			}
			if r.Method == "POST" {
				var v struct {
					Title string  `json:"title"`
					Text  *string `json:"text"`
				}
				if e := httpserver.DecodeJSON(r, &v); e != nil {
					return nil, 0, e
				}
				if v.Text == nil {
					return nil, 0, bad
				}
				result, e := s.CreateNote(ctx, a, id, v.Title, *v.Text, requestID)
				return result, 201, e
			}
		}
		if len(parts) >= 3 && !validID(parts[2]) {
			return nil, 0, bad
		}
		if len(parts) == 3 {
			if r.Method == "GET" {
				v, e := s.GetNote(ctx, a, id, parts[2])
				return v, 200, e
			}
			if r.Method == "PATCH" {
				var v struct {
					Title   string  `json:"title"`
					Text    *string `json:"text"`
					Version int64   `json:"version"`
				}
				if e := httpserver.DecodeJSON(r, &v); e != nil {
					return nil, 0, e
				}
				if !version(v.Version) || v.Text == nil {
					return nil, 0, bad
				}
				result, e := s.ChangeNote(ctx, a, id, parts[2], v.Title, *v.Text, v.Version, requestID, false)
				return result, 200, e
			}
			if r.Method == "DELETE" {
				var v struct {
					Version int64 `json:"version"`
				}
				if e := httpserver.DecodeJSON(r, &v); e != nil {
					return nil, 0, e
				}
				if !version(v.Version) {
					return nil, 0, bad
				}
				_, e := s.ChangeNote(ctx, a, id, parts[2], "", "", v.Version, requestID, true)
				return nil, 204, e
			}
		}
		if len(parts) == 4 && parts[3] == "export" && r.Method == "GET" {
			n, e := s.GetNote(ctx, a, id, parts[2])
			if e != nil {
				return nil, 0, e
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("Content-Disposition", `attachment; filename="workspace-note-`+n.ID+`.txt"`)
			w.WriteHeader(200)
			w.Write([]byte(n.Text))
			return nil, 0, nil
		}
	}
	return nil, 0, errors.New("RESOURCE_NOT_FOUND")
}
