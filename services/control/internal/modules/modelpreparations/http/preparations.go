package http

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

func PreparationOwns(path string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	return len(parts) >= 4 && parts[0] == "api" && parts[1] == "v1" && (parts[2] == "admin" && parts[3] == "model-preparations" || len(parts) >= 5 && parts[2] == "workspaces" && parts[4] == "model-preparations")
}

// ReviewRoute selects the independent bounded model verification HTTP budget.
func ReviewRoute(path, method string) bool {
	return method == http.MethodPost && PreparationOwns(path) && strings.HasSuffix(path, "/reviews")
}
func oneKey(r *http.Request) (string, error) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) != 1 || !datawire.ValidID(values[0]) {
		return "", errors.New("VALIDATION_FAILED")
	}
	return values[0], nil
}
func validPageQuery(query map[string][]string, events, state bool) bool {
	for name, values := range query {
		if len(values) != 1 || values[0] == "" {
			return false
		}
		if name == "limit" || events && name == "after" {
			value, e := strconv.ParseInt(values[0], 10, 64)
			if e != nil || strconv.FormatInt(value, 10) != values[0] {
				return false
			}
			if name == "limit" && (value < 1 || value > 100) || name == "after" && (value < 0 || value > 1024) {
				return false
			}
			continue
		}
		if !events && (name == "cursor" || state && name == "state") {
			continue
		}
		return false
	}
	return true
}
func (h *Handler) preparations(r *http.Request, a identity.Auth, request string) (any, int, error) {
	bad, missing := errors.New("VALIDATION_FAILED"), errors.New("RESOURCE_NOT_FOUND")
	if h.Service == nil {
		return nil, 0, errors.New("MODEL_PREPARATION_UNAVAILABLE")
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if !PreparationOwns(r.URL.Path) || r.URL.Path != "/"+strings.Join(parts, "/") {
		return nil, 0, missing
	}
	admin := parts[2] == "admin"
	workspace := ""
	var tail []string
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
	query := r.URL.Query()
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
		var input modelpreparations.CreateRequest
		if e := decode(r, &input); e != nil {
			return nil, 0, e
		}
		if !admin && input.WorkspaceID != workspace {
			return nil, 0, bad
		}
		key, e := oneKey(r)
		if e != nil {
			return nil, 0, e
		}
		out, e := h.Service.Create(r.Context(), a, input, key, request)
		return out, 201, e
	}
	if r.Method == http.MethodGet {
		if len(tail) == 0 {
			if !validPageQuery(query, false, true) {
				return nil, 0, bad
			}
			out, e := h.Service.List(r.Context(), a, workspace, query)
			return out, 200, e
		}
		if len(tail) == 1 && tail[0] == "capabilities" {
			if len(query) != 0 {
				return nil, 0, bad
			}
			out, e := h.Service.PreparationCapabilities(r.Context(), a, workspace)
			return out, 200, e
		}
		if len(tail) == 2 && tail[0] == "nodes" && datawire.ValidID(tail[1]) {
			if len(query) != 0 {
				return nil, 0, bad
			}
			out, e := h.Service.Target(r.Context(), a, workspace, tail[1])
			return out, 200, e
		}
		if len(tail) < 1 || !datawire.ValidID(tail[0]) {
			return nil, 0, missing
		}
		id := tail[0]
		if len(tail) == 1 {
			if len(query) != 0 {
				return nil, 0, bad
			}
			out, e := h.Service.Get(r.Context(), a, workspace, id)
			return out, 200, e
		}
		if len(tail) == 2 && tail[1] == "report" {
			if len(query) != 0 {
				return nil, 0, bad
			}
			out, e := h.Service.Report(r.Context(), a, workspace, id)
			return out, 200, e
		}
		if len(tail) == 2 && tail[1] == "attempts" {
			if !validPageQuery(query, false, false) {
				return nil, 0, bad
			}
			out, e := h.Service.Attempts(r.Context(), a, workspace, id, query)
			return out, 200, e
		}
		if len(tail) == 4 && tail[1] == "attempts" && tail[3] == "events" && datawire.ValidID(tail[2]) {
			if !validPageQuery(query, true, false) {
				return nil, 0, bad
			}
			out, e := h.Service.Events(r.Context(), a, workspace, id, tail[2], query)
			return out, 200, e
		}
		return nil, 0, missing
	}
	if r.Method != http.MethodPost || len(tail) != 2 || !datawire.ValidID(tail[0]) || len(query) != 0 {
		return nil, 0, missing
	}
	switch tail[1] {
	case "cancellations":
		var input modelpreparations.ActionRequest
		if e := decode(r, &input); e != nil {
			return nil, 0, e
		}
		out, e := h.Service.Cancel(r.Context(), a, workspace, tail[0], input, request)
		return out, 200, e
	case "recoveries":
		var input modelpreparations.RecoveryRequest
		if e := decode(r, &input); e != nil {
			return nil, 0, e
		}
		key, e := oneKey(r)
		if e != nil {
			return nil, 0, e
		}
		out, e := h.Service.Recover(r.Context(), a, workspace, tail[0], input, key, request)
		return out, 201, e
	case "reviews":
		if !admin {
			return nil, 0, missing
		}
		var input modelpreparations.ReviewRequest
		if e := decode(r, &input); e != nil {
			return nil, 0, e
		}
		key, e := oneKey(r)
		if e != nil {
			return nil, 0, e
		}
		out, e := h.Service.Review(r.Context(), a, workspace, tail[0], input, key, request)
		return out, 201, e
	}
	return nil, 0, missing
}
