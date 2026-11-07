package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Passion-Flow/ownsolid/pkg/capabilities"
)

func TestHandleServesTheDeclaration(t *testing.T) {
	v, status, e := Handle(httptest.NewRequest(http.MethodGet, path, nil))
	if e != nil || status != http.StatusOK {
		t.Fatal(status, e)
	}
	body, ok := v.(Response)
	if !ok || len(body.Registries) != len(capabilities.Registries()) {
		t.Fatal(v)
	}
}

func TestHandleRefuses(t *testing.T) {
	for name, tc := range map[string]struct {
		method, target, code string
	}{
		"write":    {http.MethodPost, path, "RESOURCE_NOT_FOUND"},
		"delete":   {http.MethodDelete, path, "RESOURCE_NOT_FOUND"},
		"other":    {http.MethodGet, path + "/accelerators", "RESOURCE_NOT_FOUND"},
		"encoded":  {http.MethodGet, "/api/v1/capabilit%69es", "RESOURCE_NOT_FOUND"},
		"filtered": {http.MethodGet, path + "?state=ready", "VALIDATION_FAILED"},
	} {
		_, _, e := Handle(httptest.NewRequest(tc.method, tc.target, nil))
		if e == nil || e.Error() != tc.code {
			t.Errorf("%s: %v", name, e)
		}
	}
}

func TestOwnsOnlyItsPath(t *testing.T) {
	if !Owns(path) || Owns(path+"/x") || Owns("/api/v1/admin/capabilities") {
		t.Fatal("wrong paths")
	}
}
