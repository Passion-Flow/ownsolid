package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workflows"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"reflect"
	"strings"
)

type Handler struct{ Service *workflows.Service }

func Owns(path string) bool {
	p := strings.Split(strings.Trim(path, "/"), "/")
	return len(p) >= 4 && p[0] == "api" && p[1] == "v1" && (p[2] == "admin" && p[3] == "workflows" || len(p) >= 5 && p[2] == "workspaces" && p[4] == "workflows")
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
	administration := p[2] == "admin"
	var workspace string
	var tail []string
	if administration {
		if a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin {
			return nil, 0, missing
		}
		tail = p[4:]
	} else {
		if a.Me.Audience != "portal" || !wire.ValidID(p[3]) {
			return nil, 0, missing
		}
		workspace = p[3]
		tail = p[5:]
	}
	collection := len(tail) == 0
	configuration := len(tail) == 1 && tail[0] == "configuration"
	if administration && r.Method == http.MethodGet && (collection || configuration) {
		v := q["workspace_id"]
		if len(v) != 1 || !wire.ValidID(v[0]) {
			return nil, 0, bad
		}
		workspace = v[0]
		q.Del("workspace_id")
	}
	if !collection && !configuration {
		if !wire.ValidID(tail[0]) {
			return nil, 0, missing
		}
		if administration {
			workspace, e = h.Service.ResolveWorkspace(r.Context(), a, tail[0])
			if e != nil {
				return nil, 0, e
			}
		}
	}
	ctx := r.Context()
	policy := h.Service.Identity.Config.Policy().Workflows
	maximum := httpserver.MaxJSONBytes + int64(policy.GraphBytes)*6
	if configuration {
		if r.Method != http.MethodGet {
			return nil, 0, missing
		}
		if len(q) != 0 {
			return nil, 0, bad
		}
		v, e := h.Service.Configuration(ctx, a, workspace)
		return v, 200, e
	}
	if collection {
		if r.Method == http.MethodGet {
			v, e := h.Service.List(ctx, a, workspace, q)
			return v, 200, e
		}
		if r.Method != http.MethodPost {
			return nil, 0, missing
		}
		if len(q) != 0 {
			return nil, 0, bad
		}
		key, e := intent(r)
		if e != nil {
			return nil, 0, e
		}
		var input workflows.CreateRequest
		if administration {
			e = decode(r, &input, maximum)
		} else {
			var body struct {
				Name   string `json:"name"`
				Reason string `json:"reason"`
			}
			e = decode(r, &body, maximum)
			input = workflows.CreateRequest{WorkspaceID: workspace, Name: body.Name, Reason: body.Reason}
		}
		if e != nil {
			return nil, 0, e
		}
		v, e := h.Service.Create(ctx, a, input, key, request)
		return v, 201, e
	}
	workflow := tail[0]
	tail = tail[1:]
	paged := r.Method == http.MethodGet && (len(tail) == 1 && (tail[0] == "versions" || tail[0] == "conversations" || tail[0] == "runs") || len(tail) == 3 && tail[0] == "conversations" && tail[2] == "runs")
	if !paged && len(q) != 0 {
		return nil, 0, bad
	}
	if len(tail) == 0 {
		if r.Method == http.MethodGet {
			v, e := h.Service.Get(ctx, a, workspace, workflow)
			return v, 200, e
		}
		if r.Method == http.MethodPatch {
			var input workflows.UpdateRequest
			if e := decode(r, &input, maximum); e != nil {
				return nil, 0, e
			}
			v, e := h.Service.Update(ctx, a, workspace, workflow, input, request)
			return v, 200, e
		}
		return nil, 0, missing
	}
	if len(tail) == 1 {
		if r.Method == http.MethodGet {
			switch tail[0] {
			case "versions":
				v, e := h.Service.Versions(ctx, a, workspace, workflow, q)
				return v, 200, e
			case "conversations":
				v, e := h.Service.Conversations(ctx, a, workspace, workflow, q)
				return v, 200, e
			case "runs":
				v, e := h.Service.RunPage(ctx, a, workspace, workflow, nil, q)
				return v, 200, e
			}
		}
		if r.Method != http.MethodPost {
			return nil, 0, missing
		}
		switch tail[0] {
		case "validations":
			var input workflows.RevisionReason
			if e := decode(r, &input, maximum); e != nil {
				return nil, 0, e
			}
			v, e := h.Service.Validate(ctx, a, workspace, workflow, input)
			return v, 200, e
		case "publications":
			key, e := intent(r)
			if e != nil {
				return nil, 0, e
			}
			var input workflows.RevisionReason
			if e = decode(r, &input, maximum); e != nil {
				return nil, 0, e
			}
			v, e := h.Service.Publish(ctx, a, workspace, workflow, input, key, request)
			return v, 201, e
		case "debug-runs":
			key, e := intent(r)
			if e != nil {
				return nil, 0, e
			}
			var input workflows.DebugRequest
			if e = decode(r, &input, maximum); e != nil {
				return nil, 0, e
			}
			v, e := h.Service.Debug(ctx, a, workspace, workflow, input, key, request)
			return v, 202, e
		case "conversations":
			key, e := intent(r)
			if e != nil {
				return nil, 0, e
			}
			var input workflows.ConversationRequest
			if e = decode(r, &input, maximum); e != nil {
				return nil, 0, e
			}
			v, e := h.Service.CreateConversation(ctx, a, workspace, workflow, input, key, request)
			return v, 201, e
		}
		return nil, 0, missing
	}
	if len(tail) > 3 || !wire.ValidID(tail[1]) {
		return nil, 0, missing
	}
	if tail[0] == "conversations" {
		conversation := tail[1]
		if len(tail) == 2 {
			if r.Method == http.MethodGet {
				v, e := h.Service.Conversation(ctx, a, workspace, workflow, conversation)
				return v, 200, e
			}
			if r.Method == http.MethodDelete {
				var input workflows.RevisionReason
				if e := decode(r, &input, maximum); e != nil {
					return nil, 0, e
				}
				e := h.Service.DeleteConversation(ctx, a, workspace, workflow, conversation, input, request)
				return map[string]bool{"deleted": e == nil}, 200, e
			}
		}
		if len(tail) == 3 && tail[2] == "runs" && r.Method == http.MethodGet {
			v, e := h.Service.RunPage(ctx, a, workspace, workflow, &conversation, q)
			return v, 200, e
		}
		if len(tail) == 3 && tail[2] == "runs" && r.Method == http.MethodPost {
			key, e := intent(r)
			if e != nil {
				return nil, 0, e
			}
			var input workflows.RunRequest
			if e = decode(r, &input, maximum); e != nil {
				return nil, 0, e
			}
			v, e := h.Service.RunConversation(ctx, a, workspace, workflow, conversation, input, key, request)
			return v, 202, e
		}
	}
	if tail[0] == "runs" {
		if len(tail) == 2 && r.Method == http.MethodGet {
			v, e := h.Service.GetRun(ctx, a, workspace, workflow, tail[1])
			return v, 200, e
		}
		if len(tail) == 3 && tail[2] == "cancellations" && r.Method == http.MethodPost {
			var input struct {
				Reason string `json:"reason"`
			}
			if e := decode(r, &input, maximum); e != nil {
				return nil, 0, e
			}
			v, e := h.Service.CancelRun(ctx, a, workspace, workflow, tail[1], input.Reason, request)
			return v, 200, e
		}
	}
	return nil, 0, missing
}
