package http

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	strict "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
)

type Handler struct{ Service *licensing.Service }

const adminPath = "/api/v1/admin/license"
const portalPath = "/api/v1/license/status"

// JSON string escaping can expand the bounded artifact/reason before decoding.
const MaximumImportRequestBytes = 6*wire.MaximumArtifactBytes + 6*wire.MaximumReasonRunes + 1024

func Owns(path string) bool {
	return path == portalPath || path == adminPath || strings.HasPrefix(path, adminPath+"/")
}

func (h *Handler) Handle(r *http.Request, a identity.Auth, request string) (any, int, error) {
	if r.URL.RawPath != "" || r.URL.RawQuery != "" {
		return nil, 0, errors.New("VALIDATION_FAILED")
	}
	if h == nil || h.Service == nil {
		return nil, 0, errors.New("LICENSE_CONFIGURATION_REQUIRED")
	}
	switch r.Method + " " + r.URL.Path {
	case "GET " + portalPath:
		v, e := h.Service.StatusPortal(r.Context(), a)
		return v, http.StatusOK, e
	case "GET " + adminPath:
		v, e := h.Service.StatusAdmin(r.Context(), a)
		return v, http.StatusOK, e
	case "GET " + adminPath + "/diagnostics":
		v, e := h.Service.Diagnostics(r.Context(), a, request)
		return v, http.StatusOK, e
	case "POST " + adminPath:
		var input licensing.ImportInput
		if e := decodeImport(r, &input); e != nil {
			return nil, 0, e
		}
		v, e := h.Service.Import(r.Context(), a, input, request)
		return v, http.StatusOK, e
	default:
		return nil, 0, errors.New("RESOURCE_NOT_FOUND")
	}
}

// License artifacts have their own larger fixed protocol envelope. Do not
// enlarge the shared small-business JSON limit to admit a signed file.
func decodeImport(r *http.Request, input *licensing.ImportInput) error {
	media, parameters, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || media != "application/json" || len(r.Header.Values("Content-Type")) != 1 || len(parameters) > 1 || len(parameters) == 1 && parameters["charset"] != "utf-8" {
		return errors.New("UNSUPPORTED_MEDIA_TYPE")
	}
	raw, e := io.ReadAll(io.LimitReader(r.Body, MaximumImportRequestBytes+1))
	if e != nil {
		return errors.New("VALIDATION_FAILED")
	}
	if len(raw) > MaximumImportRequestBytes {
		return errors.New("PAYLOAD_TOO_LARGE")
	}
	var fields map[string]json.RawMessage
	if strict.DecodeStrict(raw, &fields) != nil || len(fields) != 3 {
		return errors.New("VALIDATION_FAILED")
	}
	for _, name := range []string{"artifact", "reason", "expected_current_license_id"} {
		if _, ok := fields[name]; !ok {
			return errors.New("VALIDATION_FAILED")
		}
	}
	if strict.DecodeStrict(raw, input) != nil {
		return errors.New("VALIDATION_FAILED")
	}
	return nil
}
