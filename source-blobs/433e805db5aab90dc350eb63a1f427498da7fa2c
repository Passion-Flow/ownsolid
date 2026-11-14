package http

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/artifactstores"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/objectstore"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

type Handler struct{ Service *artifactstores.Service }

func Owns(path string) bool {
	p := strings.Split(strings.Trim(path, "/"), "/")
	return len(p) >= 4 && p[0] == "api" && p[1] == "v1" && (p[2] == "admin" && p[3] == "artifact-objects" || len(p) >= 5 && p[2] == "workspaces" && p[4] == "artifact-objects")
}
func decode(r *http.Request, v any) error {
	media, params, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || media != "application/json" || len(params) > 1 || len(params) == 1 && params["charset"] != "utf-8" {
		return errors.New("UNSUPPORTED_MEDIA_TYPE")
	}
	if r.Header.Get("Content-Encoding") != "" {
		return errors.New("UNSUPPORTED_MEDIA_TYPE")
	}
	raw, e := io.ReadAll(io.LimitReader(r.Body, httpserver.MaxJSONBytes+1))
	if e != nil {
		return errors.New("VALIDATION_FAILED")
	}
	if len(raw) > httpserver.MaxJSONBytes {
		return errors.New("PAYLOAD_TOO_LARGE")
	}
	if wire.DecodeStrict(raw, v) != nil {
		return errors.New("VALIDATION_FAILED")
	}
	return nil
}
func single(q url.Values, key string, required bool) (string, error) {
	values, ok := q[key]
	q.Del(key)
	if !ok && !required {
		return "", nil
	}
	if len(values) != 1 || values[0] == "" {
		return "", errors.New("VALIDATION_FAILED")
	}
	return values[0], nil
}
func creationKey(r *http.Request) (string, error) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) != 1 || !wire.ValidID(values[0]) {
		return "", errors.New("VALIDATION_FAILED")
	}
	return values[0], nil
}
func projection(v any, status int, e error) (any, int, error) {
	if e != nil {
		return nil, 0, e
	}
	return v, status, nil
}
func emptyBody(r *http.Request) error {
	if r.Body == nil {
		return nil
	}
	var b [1]byte
	n, e := r.Body.Read(b[:])
	if n != 0 || e != io.EOF {
		return errors.New("VALIDATION_FAILED")
	}
	return nil
}
func page(q url.Values) (string, int, error) {
	after, e := single(q, "after", false)
	if e != nil {
		return "", 0, e
	}
	limit, e := single(q, "limit", true)
	if e != nil {
		return "", 0, e
	}
	n, e := strconv.Atoi(limit)
	if e != nil || strconv.Itoa(n) != limit || n < 1 || n > artifactstores.MaximumObjectPageSize || after != "" && !wire.ValidID(after) {
		return "", 0, errors.New("VALIDATION_FAILED")
	}
	return after, n, nil
}
func (h *Handler) Handle(r *http.Request, a identity.Auth, request string) (any, int, error) {
	missing := errors.New("RESOURCE_NOT_FOUND")
	bad := errors.New("VALIDATION_FAILED")
	if h == nil || h.Service == nil {
		return nil, 0, errors.New("OBJECT_NOT_ADMITTED")
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if !Owns(r.URL.Path) || r.URL.Path != "/"+strings.Join(parts, "/") || r.URL.EscapedPath() != r.URL.Path {
		return nil, 0, missing
	}
	query, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil {
		return nil, 0, bad
	}
	admin := parts[2] == "admin"
	workspace := ""
	var tail []string
	if admin {
		if a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin {
			return nil, 0, missing
		}
		workspace, e = single(query, "workspace_id", true)
		tail = parts[4:]
	} else {
		if a.Me.Audience != "portal" {
			return nil, 0, missing
		}
		workspace = parts[3]
		tail = parts[5:]
	}
	if e != nil || !wire.ValidID(workspace) {
		return nil, 0, bad
	}
	if r.Method == "GET" {
		if e = emptyBody(r); e != nil {
			return nil, 0, e
		}
	}
	if len(tail) == 0 {
		if r.Method == "GET" {
			after, limit, e := page(query)
			if e != nil || len(query) != 0 {
				return nil, 0, bad
			}
			v, e := h.Service.List(r.Context(), a, workspace, after, limit)
			return projection(v, 200, e)
		}
		if r.Method == "POST" && len(query) == 0 {
			key, e := creationKey(r)
			if e != nil {
				return nil, 0, e
			}
			var input struct {
				Name    string `json:"name"`
				Purpose string `json:"purpose"`
				Bytes   int64  `json:"expected_bytes"`
				SHA256  string `json:"sha256"`
				Reason  string `json:"reason"`
			}
			if e = decode(r, &input); e != nil {
				return nil, 0, e
			}
			v, e := h.Service.Create(r.Context(), a, artifactstores.CreateRequest{WorkspaceID: workspace, Name: input.Name, Purpose: input.Purpose, ExpectedBytes: input.Bytes, SHA256: input.SHA256, Reason: input.Reason}, key, request)
			return projection(v, 202, e)
		}
		return nil, 0, missing
	}
	if len(tail) == 1 && tail[0] == "configuration" && r.Method == "GET" && len(query) == 0 {
		v, e := h.Service.Capabilities(r.Context(), a, workspace)
		return projection(v, 200, e)
	}
	if tail[0] == "expire" && r.Method == "POST" && len(tail) == 1 && len(query) == 0 {
		var input struct {
			Limit  int    `json:"limit"`
			Reason string `json:"reason"`
		}
		if e = decode(r, &input); e != nil {
			return nil, 0, e
		}
		v, e := h.Service.Expire(r.Context(), a, workspace, input.Limit, input.Reason, request)
		return projection(v, 202, e)
	}
	if tail[0] == "staging" && admin {
		if len(tail) == 1 && r.Method == "GET" {
			after, limit, e := page(query)
			if e != nil || after != "" || len(query) != 0 {
				return nil, 0, bad
			}
			v, e := h.Service.StagingRecords(r.Context(), a, workspace, limit)
			return projection(v, 200, e)
		}
		if len(tail) == 1 && r.Method == "DELETE" && len(query) == 0 {
			var input struct {
				Record objectstore.StageRecord `json:"record"`
				Reason string                  `json:"reason"`
			}
			if e = decode(r, &input); e != nil {
				return nil, 0, e
			}
			e = h.Service.CleanupStaging(r.Context(), a, workspace, input.Record, input.Reason, request)
			return projection(nil, 204, e)
		}
		return nil, 0, missing
	}
	object := tail[0]
	tail = tail[1:]
	if !wire.ValidID(object) {
		return nil, 0, missing
	}
	grant := ""
	if len(tail) == 0 && r.Method == "GET" || len(tail) == 1 && (tail[0] == "content" || tail[0] == "model-import" || tail[0] == "dataset-import") || len(tail) == 3 && tail[0] == "ingestions" && tail[2] == "reconcile" {
		grant, e = single(query, "grant_id", false)
		if e != nil || grant != "" && !wire.ValidID(grant) {
			return nil, 0, bad
		}
	}
	if len(tail) == 1 && tail[0] == "grants" && r.Method == "GET" {
		after, limit, e := page(query)
		if e != nil || len(query) != 0 {
			return nil, 0, bad
		}
		v, e := h.Service.ListGrants(r.Context(), a, workspace, object, after, limit)
		return projection(v, 200, e)
	}
	if len(tail) == 1 && tail[0] == "ingestions" && r.Method == "GET" {
		after, limit, e := page(query)
		if e != nil || len(query) != 0 {
			return nil, 0, bad
		}
		v, e := h.Service.ListIngestions(r.Context(), a, workspace, object, after, limit)
		return projection(v, 200, e)
	}
	if len(query) != 0 {
		return nil, 0, bad
	}
	if len(tail) == 0 && r.Method == "GET" {
		v, e := h.Service.Get(r.Context(), a, workspace, object, grant)
		return projection(v, 200, e)
	}
	if len(tail) == 1 && tail[0] == "content" && r.Method == "GET" {
		v, e := h.Service.Download(r.Context(), a, workspace, object, grant)
		return projection(v, 200, e)
	}
	if len(tail) == 1 && tail[0] == "snapshot" && r.Method == "GET" && admin {
		v, e := h.Service.Snapshot(r.Context(), a, workspace, object)
		return projection(v, 200, e)
	}
	if len(tail) == 1 && tail[0] == "parts" && r.Method == "GET" {
		v, e := h.Service.Parts(r.Context(), a, workspace, object)
		return projection(v, 200, e)
	}
	if len(tail) == 2 && tail[0] == "parts" && r.Method == "PUT" {
		n, e := strconv.ParseInt(tail[1], 10, 32)
		hashes := r.Header.Values("X-Object-Part-SHA256")
		media, params, mediaError := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if e != nil || strconv.FormatInt(n, 10) != tail[1] || n < 1 || n > objectstore.MaximumMultipartParts || len(hashes) != 1 || !objectstore.ValidDigest(hashes[0]) || r.ContentLength < 1 || mediaError != nil || media != "application/octet-stream" || len(params) != 0 || r.Header.Get("Content-Encoding") != "" {
			return nil, 0, bad
		}
		v, e := h.Service.PutPart(r.Context(), a, workspace, object, objectstore.Part{Number: int32(n), Bytes: r.ContentLength, SHA256: hashes[0]}, r.Body, request)
		return projection(v, 200, e)
	}
	if len(tail) == 1 && tail[0] == "grants" && r.Method == "POST" {
		key, e := creationKey(r)
		if e != nil {
			return nil, 0, e
		}
		var input artifactstores.GrantRequest
		if e = decode(r, &input); e != nil {
			return nil, 0, e
		}
		v, e := h.Service.CreateGrant(r.Context(), a, workspace, object, input, key, request)
		return projection(v, 201, e)
	}
	if len(tail) == 1 && tail[0] == "model-import" && r.Method == "POST" {
		key, e := creationKey(r)
		if e != nil {
			return nil, 0, e
		}
		var input artifactstores.ModelImportRequest
		if e = decode(r, &input); e != nil {
			return nil, 0, e
		}
		v, e := h.Service.IngestModel(r.Context(), a, workspace, object, grant, input, key, request)
		return projection(v, 202, e)
	}
	if len(tail) == 1 && tail[0] == "dataset-import" && r.Method == "POST" {
		key, e := creationKey(r)
		if e != nil {
			return nil, 0, e
		}
		var input artifactstores.DatasetImportRequest
		if e = decode(r, &input); e != nil {
			return nil, 0, e
		}
		v, e := h.Service.IngestDataset(r.Context(), a, workspace, object, grant, input, key, request)
		return projection(v, 202, e)
	}
	if len(tail) == 2 && tail[0] == "ingestions" && r.Method == "GET" {
		v, e := h.Service.GetIngestion(r.Context(), a, workspace, object, tail[1])
		return projection(v, 200, e)
	}
	var input struct {
		Reason string `json:"reason"`
	}
	if len(tail) == 3 && tail[0] == "ingestions" && tail[2] == "cancel" && r.Method == "POST" {
		if e = decode(r, &input); e != nil {
			return nil, 0, e
		}
		v, e := h.Service.CancelIngestion(r.Context(), a, workspace, object, tail[1], input.Reason, request)
		return projection(v, 200, e)
	}
	if len(tail) == 3 && tail[0] == "ingestions" && tail[2] == "reconcile" && r.Method == "POST" {
		if e = decode(r, &input); e != nil {
			return nil, 0, e
		}
		v, e := h.Service.ReconcileIngestion(r.Context(), a, workspace, object, tail[1], grant, input.Reason, request)
		return projection(v, 202, e)
	}
	if len(tail) == 2 && tail[0] == "grants" && r.Method == "DELETE" {
		if e = decode(r, &input); e != nil {
			return nil, 0, e
		}
		e = h.Service.RevokeGrant(r.Context(), a, workspace, object, tail[1], input.Reason, request)
		return projection(nil, 204, e)
	}
	if len(tail) == 1 && r.Method == "POST" && (tail[0] == "complete" || tail[0] == "abort" || tail[0] == "delete" || tail[0] == "reconcile") {
		if e = decode(r, &input); e != nil {
			return nil, 0, e
		}
		var v artifactstores.Object
		switch tail[0] {
		case "complete":
			v, e = h.Service.Complete(r.Context(), a, workspace, object, input.Reason, request)
		case "abort":
			v, e = h.Service.Abort(r.Context(), a, workspace, object, input.Reason, request)
		case "delete":
			v, e = h.Service.Delete(r.Context(), a, workspace, object, input.Reason, request)
		case "reconcile":
			v, e = h.Service.Reconcile(r.Context(), a, workspace, object, input.Reason, request)
		}
		return projection(v, 202, e)
	}
	return nil, 0, missing
}
