package http

import (
	"errors"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Passion-Flow/ownsolid/api/agent/modelartifact"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
)

type Handler struct{ Service *models.Service }

type datasetVersionLineage struct {
	DatasetID        string `json:"dataset_id"`
	DatasetVersionID string `json:"dataset_version_id"`
	ManifestSHA256   string `json:"manifest_sha256"`
}

type datasetLineage struct {
	Training datasetVersionLineage `json:"training"`
	Holdout  datasetVersionLineage `json:"holdout"`
}

type adminModelDetail struct {
	models.Version
	DatasetLineage *datasetLineage `json:"dataset_lineage,omitempty"`
}

func Owns(path string) bool {
	return path == "/api/v1/admin/models" || strings.HasPrefix(path, "/api/v1/admin/models/") || path == "/api/v1/admin/model-imports" || strings.HasPrefix(path, "/api/v1/admin/model-imports/") || strings.HasPrefix(path, "/api/v1/workspaces/") && (strings.HasSuffix(path, "/models") || strings.Contains(path, "/models/"))
}
func (h *Handler) Handle(r *http.Request, a identity.Auth, request string) (any, int, error) {
	bad := errors.New("VALIDATION_FAILED")
	missing := errors.New("RESOURCE_NOT_FOUND")
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	ctx := r.Context()
	if len(parts) >= 5 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "workspaces" && parts[4] == "models" {
		if a.Me.Audience != "portal" || r.Method != "GET" || r.URL.RawQuery != "" {
			return nil, 0, missing
		}
		if len(parts) == 5 {
			items, e := h.Service.List(ctx, a, parts[3])
			return map[string]any{"items": items}, 200, e
		}
		if len(parts) == 6 {
			value, e := h.Service.Get(ctx, a, parts[3], parts[5])
			return value, 200, e
		}
		return nil, 0, missing
	}
	if a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin {
		return nil, 0, errors.New("ACCESS_DENIED")
	}
	if len(parts) < 4 || parts[0] != "api" || parts[1] != "v1" || parts[2] != "admin" {
		return nil, 0, missing
	}
	if r.Method == "GET" && len(parts) == 5 && parts[3] == "models" && parts[4] == "catalog" && r.URL.RawQuery == "" {
		policy := h.Service.Identity.Config.Policy().Models
		return map[string]any{"catalog": wire.ApprovedModel(), "catalog_digest": wire.ModelCatalogDigest(), "chunk_bytes": models.ChunkBytes, "minimum_bundle_bytes": modelartifact.MinimumArchiveBytes(), "maximum_bundle_bytes": policy.MaximumBundleBytes, "maximum_attempts": policy.MaximumAttempts, "storage_available": h.Service.StorageAvailable()}, 200, nil
	}
	if r.Method == "GET" && len(parts) == 5 && parts[3] == "model-imports" && parts[4] == "resolve" {
		query, e := url.ParseQuery(r.URL.RawQuery)
		if e != nil || len(query) != 2 || len(query["workspace_id"]) != 1 || len(query["idempotency_key"]) != 1 {
			return nil, 0, bad
		}
		value, e := h.Service.ResolveImport(ctx, a, query.Get("workspace_id"), query.Get("idempotency_key"))
		return value, 200, e
	}
	if r.Method == "GET" && len(parts) == 4 {
		query := r.URL.Query()
		if len(query) != 1 || len(query["workspace_id"]) != 1 {
			return nil, 0, bad
		}
		workspace := query.Get("workspace_id")
		if parts[3] == "models" {
			items, e := h.Service.List(ctx, a, workspace)
			return map[string]any{"items": items}, 200, e
		}
		if parts[3] == "model-imports" {
			items, e := h.Service.Imports(ctx, a, workspace)
			return map[string]any{"items": items}, 200, e
		}
	}
	if r.URL.RawQuery != "" {
		return nil, 0, bad
	}
	if len(parts) == 6 && parts[3] == "models" && parts[5] == "retire" && r.Method == "POST" {
		var input struct {
			Reason string `json:"reason"`
		}
		if e := httpserver.DecodeJSON(r, &input); e != nil {
			return nil, 0, e
		}
		out, e := h.Service.Retire(ctx, a, parts[4], input.Reason, request)
		return out, 200, e
	}
	if len(parts) == 5 && parts[3] == "models" && r.Method == "GET" {
		value, e := h.Service.Get(ctx, a, "", parts[4])
		if e != nil {
			return nil, 0, e
		}
		detail := adminModelDetail{Version: value}
		if lineage := value.DatasetLineage; lineage != nil {
			detail.DatasetLineage = &datasetLineage{
				Training: datasetVersionLineage(lineage.Training),
				Holdout:  datasetVersionLineage(lineage.Holdout),
			}
		}
		return detail, 200, nil
	}
	if len(parts) == 4 && parts[3] == "model-imports" && r.Method == "POST" {
		var input models.ImportRequest
		if e := httpserver.DecodeJSON(r, &input); e != nil {
			return nil, 0, e
		}
		keys := r.Header.Values("Idempotency-Key")
		if len(keys) != 1 {
			return nil, 0, bad
		}
		value, e := h.Service.StartImport(ctx, a, input, keys[0], request)
		return value, 201, e
	}
	if len(parts) >= 5 && parts[3] == "model-imports" {
		id := parts[4]
		if len(parts) == 5 && r.Method == "GET" {
			value, e := h.Service.GetImport(ctx, a, id)
			return value, 200, e
		}
		if len(parts) == 6 && r.Method == "POST" {
			switch parts[5] {
			case "chunks":
				media, parameters, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
				if e != nil || media != "application/octet-stream" || len(parameters) != 0 {
					return nil, 0, errors.New("UNSUPPORTED_MEDIA_TYPE")
				}
				offsets, digests := r.Header.Values("X-Chunk-Offset"), r.Header.Values("X-Chunk-SHA256")
				if len(offsets) != 1 || len(digests) != 1 {
					return nil, 0, bad
				}
				offset, e := strconv.ParseInt(offsets[0], 10, 64)
				if e != nil || strconv.FormatInt(offset, 10) != offsets[0] || r.ContentLength < 1 || r.ContentLength > models.ChunkBytes {
					return nil, 0, bad
				}
				value, e := h.Service.UploadChunk(ctx, a, id, offset, r.ContentLength, digests[0], r.Body)
				return value, 200, e
			case "complete", "cancel":
				var input struct {
					Reason string `json:"reason"`
				}
				if e := httpserver.DecodeJSON(r, &input); e != nil {
					return nil, 0, e
				}
				if parts[5] == "complete" {
					value, e := h.Service.QueueImport(ctx, a, id, input.Reason, request)
					return value, 202, e
				}
				value, e := h.Service.CancelImport(ctx, a, id, input.Reason, request)
				return value, 200, e
			}
		}
	}
	return nil, 0, missing
}
