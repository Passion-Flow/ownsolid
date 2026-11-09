//go:build catalog_docker_integration

package apps

import (
	"bytes"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
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
		return signOCI(t, ociTar(t, files, ""), second.image, first.image)
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
		imageAsset(verified.Archive.Images[0], verified.Archive.Bytes, verified.Archive.Digest), 2*layerSize, nil)
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
}

func TestImportVerifiedReleaseTransitions(t *testing.T) {
	socket, mount, store := emptyDockerStore(t)
	policy := bundle.Defaults()
	policy.MountRoot = mount
	for _, engine := range []string{"compose", "helm"} {
		t.Run(engine, func(t *testing.T) {
			root, key, private := transitionCatalog(t)
			first := writeTransitionRelease(t, root, private, catalogReleaseFixture{version: "1.0.0", engine: engine, variant: engine})
			second := writeTransitionRelease(t, root, private, catalogReleaseFixture{version: "1.1.0", engine: engine, variant: engine, predecessors: []ReleasePin{first}})
			importVersion := func(pin ReleasePin) InstallBundle {
				t.Helper()
				result, err := ImportInstallImage(t.Context(), filepath.Join(root.Name(), pin.Version), key, ImageImportOptions{
					DockerSocket: socket, Verification: policy, MaximumPayloadBytes: testTransitionBudget,
					MaximumExpandedBytes: testTransitionBudget, Timeout: 30 * time.Second, ExpectedPin: &pin,
				})
				if err != nil || result.Pin != pin {
					t.Fatalf("import of the selected release failed: %v", err)
				}
				return result
			}
			original := importVersion(first)
			request := TransitionRequest{AppID: "example-app", PlatformVersion: "1.2.0", Installed: first}
			upgrade, err := VerifyReleaseTransition(t.Context(), root, key, testTransitionBudget, request)
			if err != nil || upgrade.Pin != second {
				t.Fatalf("signed upgrade selection failed: %v", err)
			}
			importVersion(upgrade.Pin)
			before := listDockerImages(t, store)
			slices.Sort(before)
			request.Installed, request.RollbackTo = upgrade.Pin, &original.Pin
			for range 2 {
				rollback, err := VerifyReleaseTransition(t.Context(), root, key, testTransitionBudget, request)
				if err != nil || rollback.Pin != original.Pin {
					t.Fatalf("exact rollback selection failed: %v", err)
				}
				restored := importVersion(rollback.Pin)
				if restored.Manifest.Artifact != original.Manifest.Artifact || !slices.Equal(restored.Archive.Images, original.Archive.Images) {
					t.Fatal("rollback changed the original artifact or image identities")
				}
			}
			after := listDockerImages(t, store)
			slices.Sort(after)
			if !slices.Equal(after, before) {
				t.Fatalf("rollback added an image: before %v, after %v", before, after)
			}
		})
	}
}

func TestImportVerifiedRenderedCompose(t *testing.T) {
	socket, mount, store := emptyDockerStore(t)
	root, key, private := transitionCatalog(t)
	pin := writeTransitionRelease(t, root, private, catalogReleaseFixture{version: "1.0.0", engine: "compose"})
	staging := filepath.Join(root.Name(), pin.Version)
	release, err := root.OpenRoot(pin.Version)
	if err != nil {
		t.Fatal(err)
	}
	defer release.Close()
	verified, err := VerifyInstallBundle(t.Context(), release, key, testTransitionBudget)
	if err != nil {
		t.Fatal(err)
	}
	input, err := release.ReadFile("compose.yaml")
	if err != nil || digestBytes(input) != verified.Manifest.Artifact.SHA256 {
		t.Fatalf("read the verified Compose source: %v", err)
	}
	render := exec.CommandContext(t.Context(), "docker", "compose", "--project-name", "catalog-image-plan", "--profile", "*",
		"--project-directory", staging, "-f", "-", "config", "--format", "json", "--no-env-resolution")
	render.Stdin = bytes.NewReader(input)
	model, err := render.Output()
	if err != nil {
		t.Fatalf("render the signed Compose fixture: %v", err)
	}
	policy := bundle.Defaults()
	policy.MountRoot = mount
	rendered := RenderedCompose{ArtifactSHA256: digestBytes(input), JSON: model}
	plan, err := verified.VerifyComposeImagePlan(rendered)
	if err != nil {
		t.Fatal(err)
	}
	replaced := RenderedCompose{ArtifactSHA256: rendered.ArtifactSHA256, JSON: append(slices.Clone(model), '\n')}
	options := ImageImportOptions{
		DockerSocket: socket, Verification: policy, MaximumPayloadBytes: testTransitionBudget,
		MaximumExpandedBytes: testTransitionBudget, Timeout: 30 * time.Second, ExpectedPin: &pin,
		RenderedCompose: &replaced, ExpectedComposePlan: &plan,
	}
	if _, err := ImportInstallImage(t.Context(), staging, key, options); !errors.Is(err, ErrComposeImagePlan) || len(listDockerImages(t, store)) != 0 {
		t.Fatalf("replacement changed the local image store: %v", err)
	}
	options.RenderedCompose = &rendered
	result, err := ImportInstallImage(t.Context(), staging, key, options)
	if err != nil || result.Pin != pin {
		t.Fatalf("rendered signed image import failed: %v", err)
	}
	want := []ComposeServiceImage{{Service: "fixture", ManifestDigest: result.Archive.Images[0].ManifestDigest}}
	if err := plan.VerifyRendered(rendered); err != nil ||
		!slices.Equal(plan.Services, want) || !slices.Equal(listDockerImages(t, store), []string{want[0].ManifestDigest}) {
		t.Fatalf("engine does not contain the verified service image: %v", err)
	}
}
