package localization

import (
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"net/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"strconv"
	"strings"
)

const publicPrefix = "/api/v1/localization/"

func (r *Registry) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	r.ServeHTTPWithPolicy(w, request, config.DefaultHTTPPolicy())
}

// ServeHTTPWithPolicy shares the serving owner's retry advice without a writer wrapper.
func (r *Registry) ServeHTTPWithPolicy(w http.ResponseWriter, request *http.Request, policy config.HTTPPolicy) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	requestID := w.Header().Get("X-Request-ID")
	if _, err := uuid.Parse(requestID); err != nil {
		requestID = uuid.NewString()
		w.Header().Set("X-Request-ID", requestID)
	}
	fail := func(code string) { httpserver.ProblemWithPolicy(w, requestID, errors.New(code), policy) }
	if r == nil {
		fail("SERVICE_NOT_READY")
		return
	}
	if request.Method != http.MethodGet || request.URL.RawQuery != "" || request.URL.RawPath != "" {
		fail("RESOURCE_NOT_FOUND")
		return
	}
	if request.URL.Path == publicPrefix+"catalog" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"source_sha256": r.source, "locales": r.Catalog(), "limits": map[string]int{"package_bytes": r.policy.PackageBytes, "font_bytes": r.policy.FontBytes, "aggregate_bytes": r.policy.AggregateBytes, "maximum_messages": r.policy.MaximumMessages, "maximum_fonts": r.policy.MaximumFonts}}})
		return
	}
	if !strings.HasPrefix(request.URL.Path, publicPrefix) {
		fail("RESOURCE_NOT_FOUND")
		return
	}
	parts := strings.Split(strings.TrimPrefix(request.URL.Path, publicPrefix), "/")
	if len(parts) != 2 || !digestPattern.MatchString(parts[1]) {
		fail("RESOURCE_NOT_FOUND")
		return
	}
	value, ok := r.blobs[parts[1]]
	if !ok {
		fail("RESOURCE_NOT_FOUND")
		return
	}
	allowed := parts[0] == "packages" && value.mediaType == "application/json" || parts[0] == "fonts" && value.mediaType == "font/ttf" || parts[0] == "assets" && (value.mediaType == "image/png" || value.mediaType == "text/plain; charset=utf-8")
	if !allowed {
		fail("RESOURCE_NOT_FOUND")
		return
	}
	etag := "\"" + parts[1] + "\""
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age="+strconv.Itoa(r.policy.CacheSeconds)+", immutable")
	w.Header().Set("Content-Type", value.mediaType)
	if request.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(value.bytes)))
	_, _ = w.Write(value.bytes)
}
