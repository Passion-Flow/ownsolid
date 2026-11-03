package http

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"reflect"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/knowledge"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

type Handler struct{ Service *knowledge.Service }

func Owns(path string) bool {
	p := strings.Split(strings.Trim(path, "/"), "/")
	return len(p) >= 4 && p[0] == "api" && p[1] == "v1" && (p[2] == "admin" && p[3] == "knowledge-bases" || len(p) >= 5 && p[2] == "workspaces" && p[4] == "knowledge-bases")
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
	if wire.DecodeStrict(raw, value) != nil {
		return errors.New("VALIDATION_FAILED")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return errors.New("VALIDATION_FAILED")
	}
	typ := reflect.TypeOf(value).Elem()
	// Every initial request field is required. Zero-valued booleans/scores cannot
	// silently stand in for a missing explicit mutation or retrieval parameter.
	if typ.Kind() != reflect.Struct || len(fields) != typ.NumField() {
		return errors.New("VALIDATION_FAILED")
	}
	for i := 0; i < typ.NumField(); i++ {
		if _, ok := fields[strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]]; !ok {
			return errors.New("VALIDATION_FAILED")
		}
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
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if !Owns(r.URL.Path) || r.URL.RawPath != "" || r.URL.Path != "/"+strings.Join(parts, "/") {
		return nil, 0, missing
	}
	query, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil {
		return nil, 0, bad
	}
	admin := parts[2] == "admin"
	var workspace string
	var tail []string
	if admin {
		if a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin {
			return nil, 0, missing
		}
		tail = parts[4:]
	} else {
		if a.Me.Audience != "portal" || !wire.ValidID(parts[3]) {
			return nil, 0, missing
		}
		workspace = parts[3]
		tail = parts[5:]
	}
	collection := len(tail) == 0
	configuration := len(tail) == 1 && tail[0] == "configuration"
	if admin && r.Method == http.MethodGet && (collection || configuration) {
		v := query["workspace_id"]
		if len(v) != 1 || !wire.ValidID(v[0]) {
			return nil, 0, bad
		}
		workspace = v[0]
		query.Del("workspace_id")
	}
	if !collection && !configuration {
		if !wire.ValidID(tail[0]) {
			return nil, 0, missing
		}
		if admin {
			workspace, e = h.Service.ResolveBaseWorkspace(r.Context(), a, tail[0])
			if e != nil {
				return nil, 0, e
			}
		}
	}
	ctx := r.Context()
	if configuration && r.Method == http.MethodGet {
		if len(query) != 0 {
			return nil, 0, bad
		}
		v, e := h.Service.Configuration(ctx, a, workspace)
		return v, 200, e
	}
	if configuration {
		return nil, 0, missing
	}
	if collection {
		if r.Method == http.MethodGet {
			v, e := h.Service.BasePage(ctx, a, workspace, query)
			return v, 200, e
		}
		if r.Method == http.MethodPost {
			if len(query) != 0 {
				return nil, 0, bad
			}
			key, e := intent(r)
			if e != nil {
				return nil, 0, e
			}
			var input knowledge.CreateBase
			if admin {
				e = decode(r, &input, httpserver.MaxJSONBytes)
			} else {
				var body struct {
					Name   string `json:"name"`
					Reason string `json:"reason"`
				}
				e = decode(r, &body, httpserver.MaxJSONBytes)
				input = knowledge.CreateBase{WorkspaceID: workspace, Name: body.Name, Reason: body.Reason}
			}
			if e != nil {
				return nil, 0, e
			}
			v, e := h.Service.CreateBase(ctx, a, input, key, request)
			return v, 201, e
		}
		return nil, 0, missing
	}
	base := tail[0]
	tail = tail[1:]
	page := r.Method == http.MethodGet && len(tail) == 1 && tail[0] == "documents"
	sourceRead := r.Method == http.MethodGet && len(tail) == 3 && tail[0] == "documents" && tail[2] == "source"
	if !page && !sourceRead && len(query) != 0 {
		return nil, 0, bad
	}
	if len(tail) == 0 {
		if r.Method == http.MethodGet {
			v, e := h.Service.GetBase(ctx, a, workspace, base)
			return v, 200, e
		}
		if r.Method == http.MethodPatch {
			var input knowledge.UpdateBase
			if e := decode(r, &input, httpserver.MaxJSONBytes); e != nil {
				return nil, 0, e
			}
			v, e := h.Service.Update(ctx, a, workspace, base, input, request)
			return v, 200, e
		}
	}
	if len(tail) == 1 {
		switch tail[0] {
		case "documents":
			if r.Method == http.MethodGet {
				v, e := h.Service.DocumentPage(ctx, a, workspace, base, query)
				return v, 200, e
			}
			if r.Method == http.MethodPost {
				key, e := intent(r)
				if e != nil {
					return nil, 0, e
				}
				var input knowledge.UploadDocument
				if e = decode(r, &input, int64(h.Service.Identity.Config.Policy().Knowledge.SourceBytes)*6+httpserver.MaxJSONBytes); e != nil {
					return nil, 0, e
				}
				v, e := h.Service.Upload(ctx, a, workspace, base, input, key, request)
				return v, 202, e
			}
		case "searches":
			if r.Method == http.MethodPost {
				var input knowledge.SearchRequest
				if e := decode(r, &input, httpserver.MaxJSONBytes+int64(h.Service.Identity.Config.Policy().Knowledge.QueryBytes)*6); e != nil {
					return nil, 0, e
				}
				v, e := h.Service.Search(ctx, a, workspace, base, input, request)
				return v, 200, e
			}
		case "answers":
			if r.Method == http.MethodPost {
				var input knowledge.AnswerRequest
				if e := decode(r, &input, httpserver.MaxJSONBytes+int64(h.Service.Identity.Config.Policy().Knowledge.QueryBytes)*6); e != nil {
					return nil, 0, e
				}
				v, e := h.Service.Answer(ctx, a, workspace, base, input, request)
				return v, 200, e
			}
		case "rebuilds":
			if r.Method == http.MethodPost {
				key, e := intent(r)
				if e != nil {
					return nil, 0, e
				}
				var input knowledge.RevisionReason
				if e = decode(r, &input, httpserver.MaxJSONBytes); e != nil {
					return nil, 0, e
				}
				v, e := h.Service.Rebuild(ctx, a, workspace, base, input, key, request)
				return v, 202, e
			}
		}
	}
	if len(tail) < 2 || len(tail) > 3 || tail[0] != "documents" || !wire.ValidID(tail[1]) {
		return nil, 0, missing
	}
	document := tail[1]
	if len(tail) == 2 {
		if r.Method == http.MethodGet {
			v, e := h.Service.GetDocument(ctx, a, workspace, base, document)
			return v, 200, e
		}
		if r.Method == http.MethodDelete {
			var input knowledge.RevisionReason
			if e := decode(r, &input, httpserver.MaxJSONBytes); e != nil {
				return nil, 0, e
			}
			v, e := h.Service.Delete(ctx, a, workspace, base, document, input, request)
			return v, 200, e
		}
	}
	if len(tail) == 3 && tail[2] == "source" {
		if r.Method == http.MethodGet {
			values := query["source_id"]
			if len(query) != 1 || len(values) != 1 || !wire.ValidID(values[0]) {
				return nil, 0, bad
			}
			v, e := h.Service.GetSource(ctx, a, workspace, base, document, values[0])
			return v, 200, e
		}
		if r.Method == http.MethodPut {
			key, e := intent(r)
			if e != nil {
				return nil, 0, e
			}
			var input knowledge.ReplaceSource
			if e = decode(r, &input, int64(h.Service.Identity.Config.Policy().Knowledge.SourceBytes)*6+httpserver.MaxJSONBytes); e != nil {
				return nil, 0, e
			}
			v, e := h.Service.Replace(ctx, a, workspace, base, document, input, key, request)
			return v, 202, e
		}
	}
	if len(tail) == 3 && tail[2] == "retries" && r.Method == http.MethodPost {
		key, e := intent(r)
		if e != nil {
			return nil, 0, e
		}
		var input knowledge.RevisionReason
		if e = decode(r, &input, httpserver.MaxJSONBytes); e != nil {
			return nil, 0, e
		}
		v, e := h.Service.Retry(ctx, a, workspace, base, document, input, key, request)
		return v, 202, e
	}
	return nil, 0, missing
}
