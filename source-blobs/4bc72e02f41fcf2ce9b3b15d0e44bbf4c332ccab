package http

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

type Handler struct{ Service *training.Service }

func Owns(path string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	return len(parts) >= 4 && parts[0] == "api" && parts[1] == "v1" && (parts[2] == "admin" && parts[3] == "training" || len(parts) >= 5 && parts[2] == "workspaces" && parts[4] == "training")
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
func creationKey(r *http.Request) (string, error) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) != 1 || !datawire.ValidID(values[0]) {
		return "", errors.New("VALIDATION_FAILED")
	}
	return values[0], nil
}
func queryOnly(query url.Values, allowed ...string) bool {
	for key, values := range query {
		if len(values) != 1 {
			return false
		}
		found := false
		for _, name := range allowed {
			if key == name {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func (h *Handler) Handle(r *http.Request, a identity.Auth, request string) (any, int, error) {
	bad := errors.New("VALIDATION_FAILED")
	missing := errors.New("RESOURCE_NOT_FOUND")
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if !Owns(r.URL.Path) || r.URL.Path != "/"+strings.Join(parts, "/") {
		return nil, 0, missing
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
		workspace = parts[3]
		tail = parts[5:]
		if !datawire.ValidID(workspace) {
			return nil, 0, bad
		}
	}
	create := len(tail) == 1 && tail[0] == "jobs" && r.Method == http.MethodPost
	if admin && !create {
		values := query["workspace_id"]
		if len(values) != 1 || !datawire.ValidID(values[0]) {
			return nil, 0, bad
		}
		workspace = values[0]
		query.Del("workspace_id")
	}
	if len(tail) == 1 && tail[0] == "capabilities" && r.Method == http.MethodGet {
		if len(query) != 0 {
			return nil, 0, bad
		}
		out, e := h.Service.Capabilities(r.Context(), a, workspace)
		return out, 200, e
	}
	if len(tail) == 2 && tail[0] == "nodes" && datawire.ValidID(tail[1]) && r.Method == http.MethodGet {
		if len(query) != 0 {
			return nil, 0, bad
		}
		out, e := h.Service.Target(r.Context(), a, workspace, tail[1])
		return out, 200, e
	}
	if len(tail) == 1 && tail[0] == "jobs" {
		if r.Method == http.MethodGet {
			if !queryOnly(query, "limit", "cursor", "state") {
				return nil, 0, bad
			}
			out, e := h.Service.List(r.Context(), a, workspace, query)
			return out, 200, e
		}
		if create {
			if len(query) != 0 {
				return nil, 0, bad
			}
			var input training.CreateRequest
			if e := decode(r, &input); e != nil {
				return nil, 0, e
			}
			if !admin && input.WorkspaceID != workspace {
				return nil, 0, bad
			}
			if h.Service.Store == nil {
				return nil, 0, errors.New("TRAINING_UNAVAILABLE")
			}
			key, e := creationKey(r)
			if e != nil {
				return nil, 0, e
			}
			out, e := h.Service.Create(r.Context(), a, input, key, request)
			return out, 201, e
		}
		return nil, 0, missing
	}
	if len(tail) < 2 || tail[0] != "jobs" || !datawire.ValidID(tail[1]) {
		return nil, 0, missing
	}
	job := tail[1]
	if len(tail) == 2 && r.Method == http.MethodGet {
		if len(query) != 0 {
			return nil, 0, bad
		}
		out, e := h.Service.Get(r.Context(), a, workspace, job)
		return out, 200, e
	}
	if len(tail) == 3 {
		if r.Method == http.MethodPost {
			if len(query) != 0 {
				return nil, 0, bad
			}
			switch tail[2] {
			case "cancellations":
				var input training.ActionRequest
				if e := decode(r, &input); e != nil {
					return nil, 0, e
				}
				out, e := h.Service.Cancel(r.Context(), a, workspace, job, input, request)
				return out, 200, e
			case "recoveries":
				var input training.RecoveryRequest
				if e := decode(r, &input); e != nil {
					return nil, 0, e
				}
				key, e := creationKey(r)
				if e != nil {
					return nil, 0, e
				}
				out, e := h.Service.Recover(r.Context(), a, workspace, job, input, key, request)
				return out, 200, e
			default:
				return nil, 0, missing
			}
		}
		if r.Method == http.MethodGet {
			if tail[2] == "metrics" {
				if !queryOnly(query, "attempt_id", "limit", "cursor") || !datawire.ValidID(query.Get("attempt_id")) {
					return nil, 0, bad
				}
				attempt := query.Get("attempt_id")
				query.Del("attempt_id")
				out, e := h.Service.Metrics(r.Context(), a, workspace, job, attempt, query)
				return out, 200, e
			}
			if !queryOnly(query, "limit", "cursor") {
				return nil, 0, bad
			}
			switch tail[2] {
			case "attempts":
				out, e := h.Service.Attempts(r.Context(), a, workspace, job, query)
				return out, 200, e
			case "checkpoints":
				out, e := h.Service.Checkpoints(r.Context(), a, workspace, job, query)
				return out, 200, e
			case "recoveries":
				out, e := h.Service.Recoveries(r.Context(), a, workspace, job, query)
				return out, 200, e
			}
		}
	}
	if len(tail) == 4 && tail[2] == "files" && r.Method == http.MethodGet {
		if len(query) != 0 {
			return nil, 0, bad
		}
		out, e := h.Service.Export(r.Context(), a, workspace, job, tail[3], request)
		if e != nil {
			return nil, 0, e
		}
		if out.Manifest != nil {
			return *out.Manifest, 200, nil
		}
		if out.File != nil {
			return *out.File, 200, nil
		}
		return nil, 0, errors.New("TRAINING_DATA_INVALID")
	}
	return nil, 0, missing
}
