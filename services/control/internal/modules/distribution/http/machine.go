package http

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	wire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
	"strings"
)

type Handler struct{ Service *distribution.Service }

func (h *Handler) Machine(r *http.Request, request string) (any, int, error) {
	bad := errors.New("VALIDATION_FAILED")
	unauthorized := errors.New("GATEWAY_UNAUTHORIZED")
	if r.Method != http.MethodPost || r.URL.RawQuery != "" || r.URL.RawPath != "" {
		return nil, 0, bad
	}
	path := strings.Split(strings.TrimPrefix(r.URL.Path, "/gateway/v1/instances/"), "/")
	if !strings.HasPrefix(r.URL.Path, "/gateway/v1/instances/") || len(path) != 2 || !wire.ValidID(path[0]) {
		return nil, 0, bad
	}
	for _, header := range []string{"Cookie", "Origin", "Content-Encoding", "Proxy-Authorization", "X-API-Key", "API-Key", "X-Workspace-ID", "X-Tenant-ID", "X-User-ID"} {
		if len(r.Header.Values(header)) != 0 {
			return nil, 0, bad
		}
	}
	values := r.Header.Values("Authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
		return nil, 0, unauthorized
	}
	credential := strings.TrimPrefix(values[0], "Bearer ")
	if credential == "" || len(credential) > 256 || strings.ContainsAny(credential, " \t\r\n,") {
		return nil, 0, unauthorized
	}
	values = r.Header.Values("Content-Type")
	if len(values) != 1 {
		return nil, 0, errors.New("UNSUPPORTED_MEDIA_TYPE")
	}
	kind, parameters, e := mime.ParseMediaType(values[0])
	if e != nil || kind != "application/json" {
		return nil, 0, errors.New("UNSUPPORTED_MEDIA_TYPE")
	}
	for name, value := range parameters {
		if name != "charset" || !strings.EqualFold(value, "utf-8") {
			return nil, 0, errors.New("UNSUPPORTED_MEDIA_TYPE")
		}
	}
	maximum := int64(wire.MaxMachineRequestBytes)
	if path[1] == "outcomes" {
		maximum = wire.MaxOutcomeBytes
	}
	if r.ContentLength > maximum {
		return nil, 0, errors.New("PAYLOAD_TOO_LARGE")
	}
	if r.ContentLength <= 0 || len(r.TransferEncoding) != 0 {
		return nil, 0, bad
	}
	body, e := io.ReadAll(io.LimitReader(r.Body, maximum+1))
	if e != nil {
		return nil, 0, bad
	}
	if len(body) > int(maximum) {
		return nil, 0, errors.New("PAYLOAD_TOO_LARGE")
	}
	if int64(len(body)) != r.ContentLength {
		return nil, 0, bad
	}
	switch path[1] {
	case "models":
		input, e := wire.DecodeModelsRequest(body)
		if e != nil {
			return nil, 0, bad
		}
		out, e := h.Service.AuthorizeModels(r.Context(), path[0], credential, input.APIKey)
		return out, http.StatusOK, e
	case "admissions":
		input, e := wire.DecodeAdmissionRequest(body)
		if e != nil {
			return nil, 0, bad
		}
		out, e := h.Service.Admit(r.Context(), path[0], credential, input, request)
		return out, http.StatusOK, e
	case "outcomes":
		input, e := wire.DecodeOutcome(body)
		if e != nil {
			return nil, 0, bad
		}
		out, e := h.Service.Report(r.Context(), path[0], credential, input, request)
		return out, http.StatusOK, e
	default:
		return nil, 0, errors.New("RESOURCE_NOT_FOUND")
	}
}
