package http

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"reflect"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/plugins"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

type Handler struct{ Service *plugins.Service }

func Owns(path string) bool {
	p := strings.Split(strings.Trim(path, "/"), "/")
	return len(p) >= 4 && p[0] == "api" && p[1] == "v1" && (p[2] == "admin" && p[3] == "plugins" || len(p) >= 5 && p[2] == "workspaces" && p[4] == "plugins")
}
func decode(r *http.Request, value any, maximum int64) error {
	media, parameters, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || media != "application/json" || len(parameters) > 1 || len(parameters) == 1 && parameters["charset"] != "utf-8" {
		return errors.New("UNSUPPORTED_MEDIA_TYPE")
	}
	raw, e := io.ReadAll(io.LimitReader(r.Body, maximum+1))
	if e != nil {
		return errors.New("VALIDATION_FAILED")
	}
	if int64(len(raw)) > maximum {
		return errors.New("PAYLOAD_TOO_LARGE")
	}
	var generic any
	if wire.DecodeStrict(raw, &generic) != nil {
		return errors.New("VALIDATION_FAILED")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return errors.New("VALIDATION_FAILED")
	}
	typ := reflect.TypeOf(value).Elem()
	if typ.Kind() != reflect.Struct || len(fields) != typ.NumField() {
		return errors.New("VALIDATION_FAILED")
	}
	for i := 0; i < typ.NumField(); i++ {
		v, ok := fields[strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]]
		if !ok || string(v) == "null" {
			return errors.New("VALIDATION_FAILED")
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil {
		return errors.New("VALIDATION_FAILED")
	}
	return nil
}
func intent(r *http.Request) (string, error) {
	v := r.Header.Values("Idempotency-Key")
	if len(v) != 1 || !wire.ValidID(v[0]) {
		return "", errors.New("VALIDATION_FAILED")
	}
	return v[0], nil
}

func (h *Handler) Handle(r *http.Request, a identity.Auth, request string) (any, int, error) {
	missing := errors.New("RESOURCE_NOT_FOUND")
	bad := errors.New("VALIDATION_FAILED")
	p := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if !Owns(r.URL.Path) || r.URL.RawPath != "" || r.URL.Path != "/"+strings.Join(p, "/") {
		return nil, 0, missing
	}
	q, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil {
		return nil, 0, bad
	}
	ctx := r.Context()
	if p[2] == "workspaces" {
		if len(p) != 5 || a.Me.Audience != "portal" || !wire.ValidID(p[3]) || r.Method != http.MethodGet {
			return nil, 0, missing
		}
		v, e := h.Service.Grants(ctx, a, p[3], q)
		return v, 200, e
	}
	if a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin {
		return nil, 0, missing
	}
	tail := p[4:]
	if len(tail) == 0 && r.Method == http.MethodGet {
		v, e := h.Service.List(ctx, a, q)
		return v, 200, e
	}
	if len(tail) == 1 && tail[0] == "configuration" && r.Method == http.MethodGet {
		if len(q) != 0 {
			return nil, 0, bad
		}
		v, e := h.Service.Configuration(ctx, a)
		return v, 200, e
	}
	maximum := httpserver.MaxJSONBytes + int64(base64.StdEncoding.EncodedLen(h.Service.Identity.Config.Policy().Plugins.CapsuleBytes))
	if len(tail) == 1 && tail[0] == "imports" && r.Method == http.MethodPost {
		if len(q) != 0 {
			return nil, 0, bad
		}
		key, e := intent(r)
		if e != nil {
			return nil, 0, e
		}
		var input plugins.ImportRequest
		if e = decode(r, &input, maximum); e != nil {
			return nil, 0, e
		}
		v, e := h.Service.Import(ctx, a, input, key, request)
		return v, 201, e
	}
	if len(tail) >= 2 && tail[0] == "versions" && wire.ValidID(tail[1]) {
		id := tail[1]
		if len(q) != 0 {
			return nil, 0, bad
		}
		if len(tail) == 2 && r.Method == http.MethodGet {
			v, e := h.Service.Get(ctx, a, id)
			return v, 200, e
		}
		if len(tail) == 3 && tail[2] == "references" && r.Method == http.MethodGet {
			v, e := h.Service.References(ctx, a, id)
			return v, 200, e
		}
		if len(tail) != 3 || r.Method != http.MethodPost || (tail[2] != "installations" && tail[2] != "uninstallations") {
			return nil, 0, missing
		}
		key, e := intent(r)
		if e != nil {
			return nil, 0, e
		}
		if tail[2] == "installations" {
			var input plugins.RevisionReason
			if e = decode(r, &input, httpserver.MaxJSONBytes); e != nil {
				return nil, 0, e
			}
			v, e := h.Service.Install(ctx, a, id, input, key, request)
			return v, 200, e
		}
		var input plugins.UninstallRequest
		if e = decode(r, &input, httpserver.MaxJSONBytes); e != nil {
			return nil, 0, e
		}
		v, e := h.Service.Uninstall(ctx, a, id, input, key, request)
		return v, 200, e
	}
	if len(tail) >= 3 && tail[0] == "workspaces" && wire.ValidID(tail[1]) && tail[2] == "grants" {
		workspace := tail[1]
		if len(tail) == 3 && r.Method == http.MethodGet {
			v, e := h.Service.Grants(ctx, a, workspace, q)
			return v, 200, e
		}
		if len(q) != 0 {
			return nil, 0, bad
		}
		if len(tail) == 3 && r.Method == http.MethodPost {
			key, e := intent(r)
			if e != nil {
				return nil, 0, e
			}
			var input plugins.GrantRequest
			if e = decode(r, &input, httpserver.MaxJSONBytes); e != nil {
				return nil, 0, e
			}
			v, e := h.Service.Grant(ctx, a, workspace, input, key, request)
			return v, 201, e
		}
		if len(tail) == 4 && wire.ValidID(tail[3]) && r.Method == http.MethodPatch {
			key, e := intent(r)
			if e != nil {
				return nil, 0, e
			}
			var input plugins.GrantUpdate
			if e = decode(r, &input, httpserver.MaxJSONBytes); e != nil {
				return nil, 0, e
			}
			v, e := h.Service.UpdateGrant(ctx, a, workspace, tail[3], input, key, request)
			return v, 200, e
		}
	}
	return nil, 0, missing
}
