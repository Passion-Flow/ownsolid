//go:build catalog_docker_integration

package apps

import (
	"encoding/json/v2"
	"errors"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
)

func TestImportInstallImageIntoEmptyDockerStore(t *testing.T) {
	socket := os.Getenv("OWNSOLID_CATALOG_DOCKER_TEST_SOCKET")
	mount := os.Getenv("OWNSOLID_CATALOG_DOCKER_TEST_MOUNT")
	if socket == "" || mount == "" {
		t.Skip("set the socket and staging mount for a disposable Docker engine")
	}
	t.Setenv("TMPDIR", mount)
	store, err := newDockerImageStore(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer store.client.CloseIdleConnections()
	ctx := t.Context()
	if err := store.requireContainerd(ctx); err != nil {
		t.Fatal(err)
	}
	listImages := func() []string {
		t.Helper()
		status, raw, err := store.request(ctx, http.MethodGet, "/images/json?all=1", nil)
		var inventory []struct {
			ID string `json:"Id"`
		}
		if err != nil || status != http.StatusOK || json.Unmarshal(raw, &inventory) != nil || inventory == nil {
			t.Fatalf("read the disposable engine inventory: %v", err)
		}
		ids := make([]string, 0, len(inventory))
		for _, item := range inventory {
			ids = append(ids, item.ID)
		}
		return ids
	}
	if len(listImages()) != 0 {
		t.Fatal("integration test requires an empty disposable Docker store")
	}

	image := newImageFixture(t, "amd64", false)
	fixture := signOCI(t, ociTar(t, image.files, ""), image.image)
	policy := bundle.Defaults()
	policy.MountRoot = mount
	options := ImageImportOptions{
		DockerSocket: socket, Verification: policy, MaximumPayloadBytes: fixture.limit,
		MaximumExpandedBytes: 1, Timeout: 30 * time.Second,
	}
	if _, err := ImportInstallImage(ctx, fixture.root.Name(), fixture.key, options); !errors.Is(err, ErrImageImport) {
		t.Fatalf("under-budget import returned %v", err)
	}
	if len(listImages()) != 0 {
		t.Fatal("refused import changed the engine inventory")
	}

	options.MaximumExpandedBytes = 1 << 20
	for range 2 {
		verified, err := ImportInstallImage(ctx, fixture.root.Name(), fixture.key, options)
		if err != nil || len(verified.Archive.Images) != 1 || verified.Archive.Images[0].ManifestDigest != image.image {
			t.Fatalf("verified image import failed: %v", err)
		}
	}
	if ids := listImages(); len(ids) != 1 || ids[0] != image.image {
		t.Fatalf("readback did not contain exactly the signed manifest: %v", ids)
	}
}
