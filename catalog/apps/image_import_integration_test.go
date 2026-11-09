//go:build catalog_docker_integration

package apps

import (
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
)

func emptyDockerStore(t *testing.T) (socket, mount string, store dockerImageStore) {
	t.Helper()
	socket = os.Getenv("OWNSOLID_CATALOG_DOCKER_TEST_SOCKET")
	mount = os.Getenv("OWNSOLID_CATALOG_DOCKER_TEST_MOUNT")
	if socket == "" || mount == "" {
		t.Skip("set the socket and staging mount for a disposable Docker engine")
	}
	t.Setenv("TMPDIR", mount)
	store, err := newDockerImageStore(socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.client.CloseIdleConnections)
	if err := store.requireContainerd(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(listDockerImages(t, store)) != 0 {
		t.Fatal("integration test requires an empty disposable Docker store")
	}
	return socket, mount, store
}

func listDockerImages(t *testing.T, store dockerImageStore) []string {
	t.Helper()
	status, raw, err := store.request(t.Context(), http.MethodGet, "/images/json?all=1", nil)
	var inventory []struct {
		ID   string   `json:"Id"`
		Tags []string `json:"RepoTags"`
	}
	if err != nil || status != http.StatusOK || json.Unmarshal(raw, &inventory) != nil || inventory == nil {
		t.Fatalf("read the disposable engine inventory: %v", err)
	}
	ids := make([]string, 0, len(inventory))
	for _, item := range inventory {
		if len(item.Tags) != 0 {
			t.Fatalf("import registered source tags: %v", item.Tags)
		}
		ids = append(ids, item.ID)
	}
	return ids
}

func TestImportInstallImageIntoEmptyDockerStore(t *testing.T) {
	socket, mount, store := emptyDockerStore(t)
	ctx := t.Context()
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
	if len(listDockerImages(t, store)) != 0 {
		t.Fatal("refused import changed the engine inventory")
	}

	options.MaximumExpandedBytes = 1 << 20
	for range 2 {
		verified, err := ImportInstallImage(ctx, fixture.root.Name(), fixture.key, options)
		if err != nil || len(verified.Archive.Images) != 1 || verified.Archive.Images[0].ManifestDigest != image.image {
			t.Fatalf("verified image import failed: %v", err)
		}
	}
	if ids := listDockerImages(t, store); len(ids) != 1 || ids[0] != image.image {
		t.Fatalf("readback did not contain exactly the signed manifest: %v", ids)
	}
}

func TestImportInstallImageSetIntoEmptyDockerStore(t *testing.T) {
	_, mount, store := emptyDockerStore(t)
	proxy := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "http", Host: "docker"})
	proxy.Transport = store.client.Transport
	var requests, loads atomic.Int32
	socket := dockerSocketFixture(t, func(response http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Method == http.MethodPost && request.URL.Path == "/v1.49/images/load" && loads.Add(1) == 2 {
			_, _ = io.Copy(io.Discard, request.Body)
			http.Error(response, "injected second load failure", http.StatusServiceUnavailable)
			return
		}
		proxy.ServeHTTP(response, request)
	})
	first := newImageFixture(t, "amd64", false)
	second := imageVariant(t, "amd64", false, "second", false)
	badSecond := imageVariant(t, "amd64", false, "bad diff", true)
	policy := bundle.Defaults()
	policy.MountRoot = mount
	newSet := func(first, second imageFixture) signedFixture {
		t.Helper()
		files := imageSetFiles(t, first, second)
		fixture := signOCI(t, ociTar(t, files, ""), second.image, first.image)
		if err := os.Chmod(fixture.root.Name(), 0700); err != nil {
			t.Fatal(err)
		}
		return fixture
	}
	importSet := func(fixture signedFixture, limit int64) (InstallBundle, error) {
		t.Helper()
		return ImportInstallImage(t.Context(), fixture.root.Name(), fixture.key, ImageImportOptions{
			DockerSocket: socket, Verification: policy, MaximumPayloadBytes: fixture.limit,
			MaximumExpandedBytes: limit, Timeout: 30 * time.Second,
		})
	}
	fixture := newSet(first, second)
	verified, err := VerifyInstallBundle(t.Context(), fixture.root, fixture.key, fixture.limit)
	if err != nil {
		t.Fatal(err)
	}
	layerSize := int64(len(first.files[first.layer]))
	source, err := openImportSource(t.Context(), policy, filepath.Join(fixture.root.Name(), ImageArchiveName),
		imageAsset(verified.Archive.Images[0], verified.Archive.Bytes, verified.Archive.Digest), 2*layerSize)
	if source != nil {
		source.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "RELEASE_OCI_PLATFORM_MANIFEST_IDENTITY_INVALID") {
		t.Fatalf("factory contract unexpectedly admitted a second runtime root: %v", err)
	}
	for _, tc := range []struct {
		fixture signedFixture
		limit   int64
	}{{newSet(first, badSecond), 2 * layerSize}, {fixture, 2*layerSize - 1}} {
		result, err := importSet(tc.fixture, tc.limit)
		if !errors.Is(err, ErrImageImport) || len(result.Archive.Images) != 0 || requests.Load() != 0 {
			t.Fatalf("incomplete preflight contacted Docker or returned a result: %v", err)
		}
	}
	result, err := importSet(fixture, 2*layerSize)
	if !errors.Is(err, ErrImageImport) || len(result.Archive.Images) != 0 || loads.Load() != 2 {
		t.Fatalf("second load failure returned success or the wrong attempts: %v", err)
	}
	if ids := listDockerImages(t, store); !slices.Equal(ids, []string{first.image}) {
		t.Fatalf("verified first image did not survive the failure: %v", ids)
	}
	want := []OCIImage{{ManifestDigest: first.image, ConfigDigest: first.config}, {ManifestDigest: second.image, ConfigDigest: second.config}}
	for range 2 {
		result, err := importSet(fixture, 2*layerSize)
		if err != nil || !slices.Equal(result.Archive.Images, want) || loads.Load() != 3 {
			t.Fatalf("retry did not load only the missing image: %v", err)
		}
	}
	ids := listDockerImages(t, store)
	slices.Sort(ids)
	expected := []string{first.image, second.image}
	slices.Sort(expected)
	if !slices.Equal(ids, expected) {
		t.Fatalf("readback did not contain the two exact signed manifests: %v", ids)
	}
	files, err := os.ReadDir(mount)
	if err != nil || slices.ContainsFunc(files, func(file os.DirEntry) bool {
		return strings.HasPrefix(file.Name(), ".catalog-image-")
	}) {
		t.Fatalf("private projection was left on the mount: %v", err)
	}
}
