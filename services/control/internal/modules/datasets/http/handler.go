package http

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
)

type Handler struct{ Service *datasets.Service }

func Owns(path string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	return len(parts) >= 4 && parts[0] == "api" && parts[1] == "v1" && (parts[2] == "admin" && parts[3] == "datasets" || len(parts) >= 5 && parts[2] == "workspaces" && parts[4] == "datasets")
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
	return nil
}
func key(r *http.Request) (string, error) {
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 || !wire.ValidID(keys[0]) {
		return "", errors.New("VALIDATION_FAILED")
	}
	return keys[0], nil
}
func reason(r *http.Request) (string, error) {
	var input struct {
		Reason string `json:"reason"`
	}
	e := decode(r, &input, httpserver.MaxJSONBytes)
	return input.Reason, e
}
func (h *Handler) Handle(r *http.Request, a identity.Auth, requestID string) (any, int, error) {
	missing := errors.New("RESOURCE_NOT_FOUND")
	bad := errors.New("VALIDATION_FAILED")
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if r.URL.Path != "/"+strings.Join(parts, "/") || !Owns(r.URL.Path) {
		return nil, 0, missing
	}
	var workspace string
	var tail []string
	query := r.URL.Query()
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
	}
	collection := len(tail) == 0
	configuration := len(tail) == 1 && tail[0] == "configuration"
	if admin && r.Method == "GET" && (collection || configuration) {
		values := query["workspace_id"]
		if len(values) != 1 || !wire.ValidID(values[0]) {
			return nil, 0, bad
		}
		workspace = values[0]
		query.Del("workspace_id")
	}
	if !collection && !configuration {
		if !wire.ValidID(tail[0]) {
			return nil, 0, missing
		}
		if admin {
			var e error
			workspace, e = h.Service.ResolveDatasetWorkspace(r.Context(), a, tail[0])
			if e != nil {
				return nil, 0, e
			}
		}
	}
	if configuration && r.Method == "GET" {
		if len(query) != 0 {
			return nil, 0, bad
		}
		out, e := h.Service.Configuration(r.Context(), a, workspace)
		return out, 200, e
	}
	if collection {
		if r.Method == "GET" {
			out, e := h.Service.DatasetPage(r.Context(), a, workspace, query)
			return out, 200, e
		}
		if r.Method == "POST" {
			if len(query) != 0 {
				return nil, 0, bad
			}
			intent, e := key(r)
			if e != nil {
				return nil, 0, e
			}
			var input datasets.CreateRequest
			if admin {
				e = decode(r, &input, httpserver.MaxJSONBytes)
			} else {
				var body struct {
					Name           string `json:"name"`
					Origin         string `json:"origin"`
					License        string `json:"license"`
					RightsAsserted bool   `json:"rights_asserted"`
					Reason         string `json:"reason"`
				}
				e = decode(r, &body, httpserver.MaxJSONBytes)
				input = datasets.CreateRequest{WorkspaceID: workspace, Name: body.Name, Origin: body.Origin, License: body.License, RightsAsserted: body.RightsAsserted, Reason: body.Reason}
			}
			if e != nil {
				return nil, 0, e
			}
			out, e := h.Service.Create(r.Context(), a, input, intent, requestID)
			return out, 201, e
		}
		return nil, 0, missing
	}
	if configuration {
		return nil, 0, missing
	}
	dataset := tail[0]
	tail = tail[1:]
	// Only declared paginated collections accept query fields.
	page := r.Method == "GET" && (len(tail) == 1 && (tail[0] == "drafts" || tail[0] == "versions") || len(tail) == 3 && tail[0] == "drafts" && tail[2] == "rows")
	if !page && len(query) != 0 {
		return nil, 0, bad
	}
	if len(tail) == 0 && r.Method == "GET" {
		out, e := h.Service.Get(r.Context(), a, workspace, dataset)
		return out, 200, e
	}
	if len(tail) == 1 && tail[0] == "retire" && r.Method == "POST" {
		var body struct {
			ExpectedRevision int64  `json:"expected_revision"`
			Reason           string `json:"reason"`
		}
		if e := decode(r, &body, httpserver.MaxJSONBytes); e != nil {
			return nil, 0, e
		}
		out, e := h.Service.Retire(r.Context(), a, workspace, dataset, body.ExpectedRevision, body.Reason, requestID)
		return out, 200, e
	}
	if len(tail) > 0 && tail[0] == "mongodb" {
		return h.mongodb(r, a, workspace, dataset, tail[1:], requestID)
	}
	if len(tail) > 0 && tail[0] == "drafts" {
		return h.drafts(r, a, workspace, dataset, tail[1:], query, requestID)
	}
	if len(tail) > 0 && tail[0] == "jobs" {
		return h.jobs(r, a, workspace, dataset, tail[1:], requestID)
	}
	if len(tail) > 0 && tail[0] == "versions" {
		return h.versions(r, a, workspace, dataset, tail[1:], query, requestID)
	}
	return nil, 0, missing
}
func (h *Handler) drafts(r *http.Request, a identity.Auth, workspace, dataset string, tail []string, query url.Values, requestID string) (any, int, error) {
	missing := errors.New("RESOURCE_NOT_FOUND")
	ctx := r.Context()
	if len(tail) == 0 {
		if r.Method == "GET" {
			out, e := h.Service.DraftPage(ctx, a, workspace, dataset, query)
			return out, 200, e
		}
		if r.Method == "POST" {
			intent, e := key(r)
			if e != nil {
				return nil, 0, e
			}
			var input datasets.DraftRequest
			if e = decode(r, &input, httpserver.MaxJSONBytes); e != nil {
				return nil, 0, e
			}
			out, e := h.Service.CreateDraft(ctx, a, workspace, dataset, input, intent, requestID)
			return out, 201, e
		}
		return nil, 0, missing
	}
	draft := tail[0]
	if !wire.ValidID(draft) {
		return nil, 0, missing
	}
	if len(tail) == 1 && r.Method == "GET" {
		out, e := h.Service.GetDraft(ctx, a, workspace, dataset, draft)
		return out, 200, e
	}
	if len(tail) == 2 && tail[1] == "cancel" && r.Method == "POST" {
		why, e := reason(r)
		if e != nil {
			return nil, 0, e
		}
		out, e := h.Service.CancelDraft(ctx, a, workspace, dataset, draft, why, requestID)
		return out, 200, e
	}
	if len(tail) == 2 && tail[1] == "source" && r.Method == "POST" {
		media, parameters, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if e != nil || media != "application/x-ndjson" || len(parameters) != 0 {
			return nil, 0, errors.New("UNSUPPORTED_MEDIA_TYPE")
		}
		digests := r.Header.Values("X-Content-SHA256")
		if len(digests) != 1 || !wire.ValidDigest(digests[0]) || r.ContentLength < 1 || r.ContentLength > h.Service.Identity.Config.Policy().Datasets.Limits.MaximumBytes {
			return nil, 0, errors.New("VALIDATION_FAILED")
		}
		out, e := h.Service.Upload(ctx, a, workspace, dataset, draft, digests[0], r.ContentLength, r.Body, requestID)
		return out, 202, e
	}
	if len(tail) == 2 && tail[1] == "publications" && r.Method == "POST" {
		intent, e := key(r)
		if e != nil {
			return nil, 0, e
		}
		var body struct {
			ExpectedRevision      int64  `json:"expected_revision"`
			Seed                  string `json:"seed"`
			ValidationBasisPoints int    `json:"validation_basis_points"`
			Reason                string `json:"reason"`
			TestBasisPoints       *int   `json:"test_basis_points,omitempty"`
		}
		if e = decode(r, &body, httpserver.MaxJSONBytes); e != nil {
			return nil, 0, e
		}
		var options datasets.PublicationOptions
		if body.TestBasisPoints != nil {
			if *body.TestBasisPoints == 0 {
				return nil, 0, errors.New("VALIDATION_FAILED")
			}
			options.TestBasisPoints = *body.TestBasisPoints
		}
		input := datasets.PublicationRequest{
			ExpectedRevision: body.ExpectedRevision, Seed: body.Seed,
			ValidationBasisPoints: body.ValidationBasisPoints, Reason: body.Reason,
		}
		out, e := h.Service.PublishWithOptions(ctx, a, workspace, dataset, draft, input, options, intent, requestID)
		return out, 202, e
	}
	if len(tail) >= 2 && tail[1] == "rows" {
		maximum := h.Service.Identity.Config.Policy().Datasets.Limits.MaximumLineBytes + httpserver.MaxJSONBytes
		if len(tail) == 2 && r.Method == "GET" {
			out, e := h.Service.RowPage(ctx, a, workspace, dataset, draft, query)
			return out, 200, e
		}
		if len(tail) == 2 && r.Method == "POST" {
			var body struct {
				ExpectedRevision int64  `json:"expected_revision"`
				SourceID         string `json:"source_id"`
				Prompt           string `json:"prompt"`
				Completion       string `json:"completion"`
				Reason           string `json:"reason"`
			}
			if e := decode(r, &body, maximum); e != nil {
				return nil, 0, e
			}
			out, e := h.Service.AppendRow(ctx, a, workspace, dataset, draft, datasets.RowRequest{ExpectedRevision: body.ExpectedRevision, SourceID: body.SourceID, Prompt: body.Prompt, Completion: body.Completion, Reason: body.Reason}, requestID)
			return out, 201, e
		}
		if len(tail) == 3 && wire.ValidID(tail[2]) && r.Method == "PATCH" {
			var body datasets.RowRequest
			if e := decode(r, &body, maximum); e != nil {
				return nil, 0, e
			}
			out, e := h.Service.ReplaceRow(ctx, a, workspace, dataset, draft, tail[2], body, requestID)
			return out, 200, e
		}
		if len(tail) == 3 && wire.ValidID(tail[2]) && r.Method == "DELETE" {
			var body struct {
				ExpectedRevision int64  `json:"expected_revision"`
				RowRevision      int64  `json:"row_revision"`
				Reason           string `json:"reason"`
			}
			if e := decode(r, &body, httpserver.MaxJSONBytes); e != nil {
				return nil, 0, e
			}
			e := h.Service.DeleteRow(ctx, a, workspace, dataset, draft, tail[2], body.ExpectedRevision, body.RowRevision, body.Reason, requestID)
			return nil, 204, e
		}
	}
	return nil, 0, missing
}
func (h *Handler) jobs(r *http.Request, a identity.Auth, workspace, dataset string, tail []string, requestID string) (any, int, error) {
	if len(tail) == 0 || !wire.ValidID(tail[0]) {
		return nil, 0, errors.New("RESOURCE_NOT_FOUND")
	}
	id := tail[0]
	if len(tail) == 1 && r.Method == "GET" {
		out, e := h.Service.GetJob(r.Context(), a, workspace, dataset, id)
		return out, 200, e
	}
	if len(tail) == 2 && r.Method == "POST" {
		why, e := reason(r)
		if e != nil {
			return nil, 0, e
		}
		switch tail[1] {
		case "cancel":
			out, e := h.Service.CancelJob(r.Context(), a, workspace, dataset, id, why, requestID)
			return out, 200, e
		case "retry":
			intent, e := key(r)
			if e != nil {
				return nil, 0, e
			}
			out, e := h.Service.RetryJob(r.Context(), a, workspace, dataset, id, why, intent, requestID)
			return out, 202, e
		}
	}
	return nil, 0, errors.New("RESOURCE_NOT_FOUND")
}
func (h *Handler) versions(r *http.Request, a identity.Auth, workspace, dataset string, tail []string, query url.Values, requestID string) (any, int, error) {
	if len(tail) == 0 && r.Method == "GET" {
		out, e := h.Service.VersionPage(r.Context(), a, workspace, dataset, query)
		return out, 200, e
	}
	if len(tail) == 0 || !wire.ValidID(tail[0]) {
		return nil, 0, errors.New("RESOURCE_NOT_FOUND")
	}
	id := tail[0]
	if len(tail) == 1 && r.Method == "GET" {
		out, e := h.Service.GetVersion(r.Context(), a, workspace, dataset, id)
		return out, 200, e
	}
	if len(tail) == 2 && r.Method == "POST" {
		why, e := reason(r)
		if e != nil {
			return nil, 0, e
		}
		switch tail[1] {
		case "retire":
			out, e := h.Service.RetireVersion(r.Context(), a, workspace, dataset, id, why, requestID)
			return out, 200, e
		case "drafts":
			intent, e := key(r)
			if e != nil {
				return nil, 0, e
			}
			out, e := h.Service.CloneVersion(r.Context(), a, workspace, dataset, id, why, intent, requestID)
			return out, 202, e
		}
	}
	if len(tail) == 3 && tail[1] == "files" && r.Method == "GET" {
		asset := tail[2] + ".jsonl"
		if tail[2] == "manifest" {
			asset = "manifest.json"
		}
		file, metadata, e := h.Service.VersionFile(r.Context(), a, workspace, dataset, id, asset)
		if e != nil {
			return nil, 0, e
		}
		contentType := "application/x-ndjson"
		if tail[2] == "manifest" {
			contentType = "application/json"
		}
		return httpserver.Download{File: file, Size: metadata.SizeBytes, SHA256: metadata.SHA256, ContentType: contentType, FileName: "dataset-" + id + "-" + asset}, 200, nil
	}
	return nil, 0, errors.New("RESOURCE_NOT_FOUND")
}

// SourceUpload selects only the declared bounded upload route, never an arbitrary slow-request exemption.
func SourceUpload(path, method string) bool {
	if method != "POST" {
		return false
	}
	p := strings.Split(strings.Trim(path, "/"), "/")
	if path != "/"+strings.Join(p, "/") {
		return false
	}
	return len(p) == 8 && p[0] == "api" && p[1] == "v1" && p[2] == "admin" && p[3] == "datasets" && wire.ValidID(p[4]) && p[5] == "drafts" && wire.ValidID(p[6]) && p[7] == "source" || len(p) == 9 && p[0] == "api" && p[1] == "v1" && p[2] == "workspaces" && wire.ValidID(p[3]) && p[4] == "datasets" && wire.ValidID(p[5]) && p[6] == "drafts" && wire.ValidID(p[7]) && p[8] == "source"
}
