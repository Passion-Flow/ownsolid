package http

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

func PreparationUpload(path, method string) bool {
	if method != http.MethodPost || !strings.HasPrefix(path, "/agent/v1/") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(path, "/agent/v1/"), "/")
	return len(parts) >= 5 && parts[0] == "nodes" && id(parts[1]) && parts[2] == "tasks" && id(parts[3]) && parts[4] == "model-preparation-uploads" && (len(parts) == 5 || len(parts) == 7 && id(parts[5]) && (parts[6] == "chunks" || parts[6] == "finish"))
}

func (h *Handler) preparationStop(r *http.Request, parts []string, credential string) (any, int, error) {
	if h.PreparationStop == nil {
		return nil, 0, errors.New("MODEL_PREPARATION_UNAVAILABLE")
	}
	media, parameters, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || media != "application/json" || len(parameters) != 0 || len(r.Header.Values("Content-Type")) != 1 {
		return nil, 0, errors.New("UNSUPPORTED_MEDIA_TYPE")
	}
	raw, e := io.ReadAll(io.LimitReader(r.Body, wire.MaximumStopSubmissionBytes+1))
	if e != nil {
		return nil, 0, errors.New("MODEL_PREPARATION_STOP_INVALID")
	}
	if int64(len(raw)) > wire.MaximumStopSubmissionBytes {
		return nil, 0, errors.New("PAYLOAD_TOO_LARGE")
	}
	input, e := wire.DecodeStopSubmission(raw)
	if e != nil {
		return nil, 0, e
	}
	out, e := h.PreparationStop(r.Context(), parts[1], credential, parts[3], input.LeaseToken, input.Receipt, input.Outcome)
	return out, 200, e
}

func (h *Handler) preparationUpload(r *http.Request, parts []string, credential string) (any, int, error) {
	bad := errors.New("MODEL_PREPARATION_UPLOAD_INVALID")
	media, parameters, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || len(parameters) != 0 || len(r.Header.Values("Content-Type")) != 1 {
		return nil, 0, errors.New("UNSUPPORTED_MEDIA_TYPE")
	}
	if len(parts) == 5 {
		if media != "application/json" {
			return nil, 0, errors.New("UNSUPPORTED_MEDIA_TYPE")
		}
		if h.BeginPreparationUpload == nil {
			return nil, 0, errors.New("MODEL_PREPARATION_UNAVAILABLE")
		}
		raw, e := io.ReadAll(io.LimitReader(r.Body, wire.MaximumUploadSubmissionBytes+1))
		if e != nil {
			return nil, 0, bad
		}
		if int64(len(raw)) > wire.MaximumUploadSubmissionBytes {
			return nil, 0, errors.New("PAYLOAD_TOO_LARGE")
		}
		input, e := wire.DecodeUploadSubmission(raw)
		if e != nil {
			return nil, 0, e
		}
		out, e := h.BeginPreparationUpload(r.Context(), parts[1], credential, parts[3], input.LeaseToken, input.Metadata)
		return out, 201, e
	}
	if parts[6] == "finish" {
		if media != "application/json" {
			return nil, 0, errors.New("UNSUPPORTED_MEDIA_TYPE")
		}
		if h.FinishPreparationUpload == nil {
			return nil, 0, errors.New("MODEL_PREPARATION_UNAVAILABLE")
		}
		var raw json.RawMessage
		if e = httpserver.DecodeJSON(r, &raw); e != nil {
			return nil, 0, e
		}
		var input struct {
			LeaseToken string `json:"lease_token"`
		}
		if wire.DecodeStrict(raw, &input) != nil || !security.ValidToken(input.LeaseToken) {
			return nil, 0, bad
		}
		out, e := h.FinishPreparationUpload(r.Context(), parts[1], credential, parts[3], input.LeaseToken, parts[5])
		return out, 200, e
	}
	if media != "application/octet-stream" {
		return nil, 0, errors.New("UNSUPPORTED_MEDIA_TYPE")
	}
	if h.PreparationChunk == nil {
		return nil, 0, errors.New("MODEL_PREPARATION_UNAVAILABLE")
	}
	header := func(name string) (string, bool) {
		v := r.Header.Values(name)
		return strings.Join(v, ""), len(v) == 1 && !strings.Contains(v[0], ",")
	}
	token, ok := header("X-Model-Preparation-Lease-Token")
	if !ok || !security.ValidToken(token) {
		return nil, 0, bad
	}
	asset, ok := header("X-Model-Preparation-Asset")
	if !ok {
		return nil, 0, bad
	}
	sha, ok := header("X-Model-Preparation-SHA256")
	if !ok {
		return nil, 0, bad
	}
	offset, ok := header("X-Model-Preparation-Offset")
	if !ok {
		return nil, 0, bad
	}
	start, e := strconv.ParseInt(offset, 10, 64)
	if e != nil || strconv.FormatInt(start, 10) != offset || len(r.TransferEncoding) != 0 {
		return nil, 0, bad
	}
	input := wire.ChunkRequest{Asset: asset, Offset: start, Length: r.ContentLength, SHA256: sha}
	if input.Validate() != nil {
		return nil, 0, bad
	}
	out, e := h.PreparationChunk(r.Context(), parts[1], credential, parts[3], token, parts[5], input, io.LimitReader(r.Body, input.Length+1))
	return out, 200, e
}

func PreparationStopRoute(path, method string) bool {
	if method != http.MethodPost || !strings.HasPrefix(path, "/agent/v1/") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(path, "/agent/v1/"), "/")
	return len(parts) == 5 && parts[0] == "nodes" && id(parts[1]) && parts[2] == "tasks" && id(parts[3]) && parts[4] == "model-preparation-stops"
}
