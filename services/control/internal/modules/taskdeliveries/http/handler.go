package http

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/taskdeliveries"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/taskbus"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

// Root invokes Handle only after actual session, origin, CSRF for mutation and
// whole-platform License middleware. The owning Service repeats current authority.
type Handler struct{ Service *taskdeliveries.Service }

func Owns(path string) bool {
	p := strings.Split(strings.Trim(path, "/"), "/")
	return len(p) == 4 && p[0] == "api" && p[1] == "v1" && p[2] == "admin" && p[3] == "task-delivery-quarantine" || len(p) >= 6 && p[0] == "api" && p[1] == "v1" && p[2] == "admin" && p[3] == "nodes" && p[5] == "task-deliveries" || len(p) >= 7 && p[0] == "api" && p[1] == "v1" && p[2] == "workspaces" && p[4] == "nodes" && p[6] == "task-deliveries"
}
func project(value any, status int, err error) (any, int, error) {
	if err != nil {
		return nil, 0, err
	}
	return value, status, nil
}
func pageQuery(values url.Values) error {
	for key, v := range values {
		if key != "limit" && key != "cursor" || len(v) != 1 || v[0] == "" {
			return errors.New("VALIDATION_FAILED")
		}
		if key == "limit" {
			n, e := strconv.Atoi(v[0])
			if e != nil || n < 1 || n > pagination.MaximumLimit || strconv.Itoa(n) != v[0] {
				return errors.New("VALIDATION_FAILED")
			}
		}
		if key == "cursor" && len(v[0]) > pagination.MaximumCursorBytes {
			return errors.New("INVALID_CURSOR")
		}
	}
	return nil
}
func emptyBody(r *http.Request) error {
	if r.Body == nil {
		return nil
	}
	var one [1]byte
	n, e := r.Body.Read(one[:])
	if n != 0 || e != io.EOF {
		return errors.New("VALIDATION_FAILED")
	}
	return nil
}
func retryBody(r *http.Request) (taskdeliveries.RetryRequest, error) {
	var input taskdeliveries.RetryRequest
	media, params, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || media != "application/json" || len(params) > 1 || len(params) == 1 && params["charset"] != "utf-8" || r.Header.Get("Content-Encoding") != "" {
		return input, errors.New("UNSUPPORTED_MEDIA_TYPE")
	}
	if r.Body == nil {
		return input, errors.New("VALIDATION_FAILED")
	}
	raw, e := io.ReadAll(io.LimitReader(r.Body, httpserver.MaxJSONBytes+1))
	if e != nil {
		return input, errors.New("VALIDATION_FAILED")
	}
	if len(raw) > httpserver.MaxJSONBytes {
		return input, errors.New("PAYLOAD_TOO_LARGE")
	}
	if wire.DecodeStrict(raw, &input) != nil {
		return taskdeliveries.RetryRequest{}, errors.New("VALIDATION_FAILED")
	}
	return input, nil
}
func (h *Handler) Handle(r *http.Request, a identity.Auth, request string) (any, int, error) {
	missing, bad := errors.New("RESOURCE_NOT_FOUND"), errors.New("VALIDATION_FAILED")
	if r == nil || r.URL == nil {
		return nil, 0, bad
	}
	p := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if !Owns(r.URL.Path) || r.URL.RawPath != "" || r.URL.Path != "/"+strings.Join(p, "/") {
		return nil, 0, missing
	}
	q, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil {
		return nil, 0, bad
	}
	if h == nil || h.Service == nil {
		return nil, 0, errors.New("TASK_BUS_NOT_ADMITTED")
	}
	if len(p) == 4 {
		if a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin || r.Method != http.MethodGet {
			return nil, 0, missing
		}
		if e = emptyBody(r); e != nil {
			return nil, 0, e
		}
		if e = pageQuery(q); e != nil {
			return nil, 0, e
		}
		value, e := h.Service.Quarantines(r.Context(), a, q)
		return project(value, 200, e)
	}
	workspace, node := "", ""
	var tail []string
	if p[2] == "admin" {
		if a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin {
			return nil, 0, missing
		}
		v := q["workspace_id"]
		q.Del("workspace_id")
		if len(v) != 1 || !taskbus.ValidID(v[0]) {
			return nil, 0, bad
		}
		workspace, node, tail = v[0], p[4], p[6:]
	} else {
		if a.Me.Audience != "portal" {
			return nil, 0, missing
		}
		workspace, node, tail = p[3], p[5], p[7:]
	}
	if !taskbus.ValidID(workspace) || !taskbus.ValidID(node) {
		return nil, 0, missing
	}
	if r.Method == http.MethodGet {
		if e = emptyBody(r); e != nil {
			return nil, 0, e
		}
	}
	if len(tail) == 0 && r.Method == http.MethodGet {
		if e = pageQuery(q); e != nil {
			return nil, 0, e
		}
		v, e := h.Service.List(r.Context(), a, workspace, node, q)
		return project(v, 200, e)
	}
	if len(tail) == 0 || !taskbus.ValidID(tail[0]) {
		return nil, 0, missing
	}
	event := tail[0]
	if len(tail) == 1 && r.Method == http.MethodGet && len(q) == 0 {
		v, e := h.Service.Get(r.Context(), a, workspace, node, event)
		return project(v, 200, e)
	}
	if len(tail) == 2 && tail[1] == "dead-letters" && r.Method == http.MethodGet {
		if e = pageQuery(q); e != nil {
			return nil, 0, e
		}
		v, e := h.Service.DeadLetters(r.Context(), a, workspace, node, event, q)
		return project(v, 200, e)
	}
	if len(tail) == 2 && tail[1] == "retry" && r.Method == http.MethodPost && p[2] == "admin" && len(q) == 0 {
		keys := r.Header.Values("Idempotency-Key")
		if len(keys) != 1 || !taskbus.ValidID(keys[0]) {
			return nil, 0, bad
		}
		input, e := retryBody(r)
		if e != nil {
			return nil, 0, e
		}
		v, e := h.Service.Retry(r.Context(), a, workspace, node, event, input, keys[0], request)
		return project(v, 202, e)
	}
	return nil, 0, missing
}
