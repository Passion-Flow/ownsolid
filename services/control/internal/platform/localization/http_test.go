package localization

import (
	"encoding/json"
	"github.com/google/uuid"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLocalizationActualImmutablePublicSeedBytesHashETagAndNoNetworkDependency(t *testing.T) {
	r, e := Default()
	if e != nil {
		t.Fatal(e)
	}
	request := httptest.NewRequest("GET", publicPrefix+"catalog", nil)
	response := httptest.NewRecorder()
	r.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatal("catalog unavailable")
	}
	var catalog struct {
		Data struct {
			SourceSHA256 string   `json:"source_sha256"`
			Locales      []Choice `json:"locales"`
		} `json:"data"`
	}
	if json.Unmarshal(response.Body.Bytes(), &catalog) != nil || catalog.Data.SourceSHA256 != r.source || len(catalog.Data.Locales) != 2 {
		t.Fatal("catalog contract")
	}
	for _, choice := range catalog.Data.Locales {
		request = httptest.NewRequest("GET", choice.PackageURL, nil)
		response = httptest.NewRecorder()
		r.ServeHTTP(response, request)
		if response.Code != 200 || hash(response.Body.Bytes()) != choice.PackageSHA256 || response.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatal("content integrity")
		}
		etag := response.Header().Get("ETag")
		request = httptest.NewRequest("GET", choice.PackageURL, nil)
		request.Header.Set("If-None-Match", etag)
		response = httptest.NewRecorder()
		r.ServeHTTP(response, request)
		if response.Code != 304 || response.Body.Len() != 0 {
			t.Fatal("conditional immutable read")
		}
	}
}
func TestLocalizationPublicReadRefusesArbitraryHashesCategoriesQueriesAndMethods(t *testing.T) {
	r, e := Default()
	if e != nil {
		t.Fatal(e)
	}
	digest := r.Catalog()[0].PackageSHA256
	for _, path := range []string{publicPrefix + "packages/../../private", publicPrefix + "packages/" + strings.Repeat("a", 64), publicPrefix + "fonts/" + digest, publicPrefix + "assets/" + digest, publicPrefix + "catalog?filename=private", publicPrefix + "packages/" + digest + "/extra"} {
		request := httptest.NewRequest("GET", path, nil)
		response := httptest.NewRecorder()
		r.ServeHTTP(response, request)
		if response.Code != 404 || strings.Contains(response.Body.String(), "/private") {
			t.Fatal("unsafe public read", response.Code)
		}
	}
	response := httptest.NewRecorder()
	r.ServeHTTP(response, httptest.NewRequest("POST", publicPrefix+"catalog", strings.NewReader("{}")))
	if response.Code != 404 {
		t.Fatal("unsupported method accepted")
	}
}

func TestLocalizationProblemUsesServerRequestIdentityInsteadOfCallerOverride(t *testing.T) {
	r, e := Default()
	if e != nil {
		t.Fatal(e)
	}
	request := httptest.NewRequest("GET", publicPrefix+"catalog?user_id=foreign", nil)
	request.Header.Set("X-Request-ID", "caller-controlled")
	response := httptest.NewRecorder()
	r.ServeHTTP(response, request)
	var problem struct {
		RequestID string `json:"request_id"`
	}
	if response.Code != 404 || json.Unmarshal(response.Body.Bytes(), &problem) != nil {
		t.Fatal("missing actual problem")
	}
	if _, e = uuid.Parse(problem.RequestID); e != nil || problem.RequestID != response.Header().Get("X-Request-ID") {
		t.Fatal("untrusted or malformed request identity")
	}
}
