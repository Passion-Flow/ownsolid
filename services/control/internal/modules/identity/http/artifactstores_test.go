package http

import (
	"net/http/httptest"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"testing"
)

func TestArtifactTransferRoutesUseTypedBudget(t *testing.T) {
	c := config.Config{ObjectStorage: &config.ObjectStorageConfiguration{Enabled: true, TransferSeconds: 73}}
	want := 146 + 2*c.Policy().HTTP.RequestSeconds
	for _, item := range []struct {
		method, path string
		transfer     bool
	}{
		{"PUT", "/api/v1/workspaces/w/artifact-objects/o/parts/1", true},
		{"GET", "/api/v1/admin/artifact-objects/o/content", true},
		{"GET", "/api/v1/admin/artifact-objects/o/snapshot", true},
		{"POST", "/api/v1/admin/artifact-objects/o/complete", false},
		{"GET", "/api/v1/workspaces/w/artifact-objects", false},
		{"PUT", "/api/v1/workspaces/w/notes/o/parts/1", false},
	} {
		actual := artifactTransferBudget(c, httptest.NewRequest(item.method, item.path, nil))
		if item.transfer && actual != want || !item.transfer && actual != 0 {
			t.Fatalf("%s %s budget %d", item.method, item.path, actual)
		}
	}
	c.ObjectStorage.Enabled = false
	if artifactTransferBudget(c, httptest.NewRequest("GET", "/api/v1/admin/artifact-objects/o/content", nil)) != 0 {
		t.Fatal("disabled transfer admitted")
	}
}
