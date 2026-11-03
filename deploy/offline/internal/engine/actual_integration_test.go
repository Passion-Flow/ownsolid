//go:build offlineintegration

package engine

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// This requires the exact retained small synthetic image and real local Docker.
// It never starts a container or establishes a native/NVIDIA/app-deployment claim.
func TestActualLocalEngineAndVerifiedTaglessImport(t *testing.T) {
	socket := os.Getenv("PROJECT_ONE_TEST_ENGINE_SOCKET_FILE")
	filename := os.Getenv("PROJECT_ONE_TEST_IMAGE_ARCHIVE")
	expected := os.Getenv("PROJECT_ONE_TEST_IMAGE_ARCHIVE_SHA256")
	manifest := os.Getenv("PROJECT_ONE_TEST_IMAGE_MANIFEST")
	configDigest := os.Getenv("PROJECT_ONE_TEST_IMAGE_CONFIG")
	mount := os.Getenv("PROJECT_ONE_TEST_EXTERNAL_ROOT")
	evidence := os.Getenv("PROJECT_ONE_TEST_ENGINE_EVIDENCE")
	info, e := os.Stat(filename)
	if e != nil || info.Size() > 32<<20 {
		t.Fatal("exact bounded actual fixture required", e)
	}
	settings := configuration.Engine{SocketFile: socket, Profile: "local-macos-amd64-emulation", DialSeconds: 2, RequestSeconds: 5, LoadSeconds: 30, ResponseBytes: 1048576, MaximumExpandedImageBytes: 64 << 20}
	client, e := New(settings)
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	observation, e := client.Preflight(ctx)
	if e != nil || observation.Status != "passed_local_engine_protocol_preflight_only" {
		t.Fatal(observation, e)
	}
	asset := wire.Asset{ID: "actual-synthetic-image", Path: "actual-image.tar", Kind: "image_archive", Size: info.Size(), SHA256: expected, ImageID: manifest, ImageConfigDigest: configDigest, Architecture: "amd64", Licenses: []string{"explicit-synthetic-fixture"}}
	verification := bundle.Defaults()
	verification.MountRoot = mount
	source, e := bundle.OpenImageSource(ctx, verification, filename, asset, settings.MaximumExpandedImageBytes)
	if e != nil {
		t.Fatal(e)
	}
	defer source.Close()
	inventory := source.Inventory()
	if inventory.ManifestDigest != manifest || inventory.ConfigDigest != configDigest || inventory.ExpandedBytes < 1 {
		t.Fatal(inventory)
	}
	tags := func() map[string]string {
		raw, status, e := client.request(ctx, http.MethodGet, "/v1.49/images/json", url.Values{"all": {"1"}}, nil, settings.RequestSeconds)
		if e != nil || status != 200 {
			t.Fatal("actual image inventory unavailable", e, status)
		}
		var images []struct {
			ID       string `json:"Id"`
			RepoTags []string
		}
		if wire.UniqueJSON(raw, &images) != nil {
			t.Fatal("actual inventory malformed")
		}
		out := map[string]string{}
		for _, image := range images {
			for _, tag := range image.RepoTags {
				out[tag] = image.ID
			}
		}
		return out
	}
	before := tags()
	if _, found, e := client.Inspect(ctx, manifest); e != nil || found {
		t.Fatal("test requires exact absent disposable fixture image", found, e)
	}
	cleanup := func() error {
		_, found, e := client.Inspect(context.Background(), manifest)
		if e != nil || !found {
			return e
		}
		raw, status, e := client.request(context.Background(), http.MethodGet, "/v1.49/containers/json", url.Values{"all": {"1"}}, nil, settings.RequestSeconds)
		if e != nil || status != 200 {
			return failure("OFFLINE_ENGINE_TEST_CONSUMER_INSPECTION_FAILED")
		}
		var containers []struct{ ImageID string }
		if wire.UniqueJSON(raw, &containers) != nil {
			return failure("OFFLINE_ENGINE_TEST_CONSUMER_INSPECTION_FAILED")
		}
		for _, container := range containers {
			if container.ImageID == manifest || container.ImageID == configDigest {
				return failure("OFFLINE_ENGINE_TEST_IMAGE_HAS_CONSUMER")
			}
		}
		image, _, e := client.Inspect(context.Background(), manifest)
		if e != nil || len(image.RepoTags) != 0 {
			return failure("OFFLINE_ENGINE_TEST_TAGGED_IMAGE_PRESERVED")
		}
		_, status, e = client.request(context.Background(), http.MethodDelete, "/v1.49/images/"+url.PathEscape(manifest), url.Values{"force": {"0"}, "noprune": {"1"}}, nil, settings.RequestSeconds)
		if e != nil || status != 200 {
			return failure("OFFLINE_ENGINE_TEST_EXACT_IMAGE_CLEANUP_FAILED")
		}
		return nil
	}
	cleaned := false
	t.Cleanup(func() {
		if !cleaned {
			if e := cleanup(); e != nil {
				t.Error(e)
			}
		}
	})
	reader, writer := io.Pipe()
	type produced struct {
		receipt bundle.ImportReceipt
		err     error
	}
	done := make(chan produced, 1)
	go func() {
		receipt, e := source.WriteTagless(ctx, writer)
		writer.CloseWithError(e)
		done <- produced{receipt, e}
	}()
	loadError := client.LoadTagless(ctx, reader)
	reader.CloseWithError(loadError)
	result := <-done
	if loadError != nil || result.err != nil {
		t.Fatal("actual verified load failed", loadError, result.err)
	}
	image, found, e := client.Inspect(ctx, manifest)
	if e != nil || !found || len(image.RepoTags) != 0 || !reflect.DeepEqual(image.RootFS.Layers, inventory.DiffIDs) {
		t.Fatal("actual immutable/config/rootfs readback mismatch", image, found, e)
	}
	after := tags()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("preexisting named references changed")
	}
	if e = cleanup(); e != nil {
		t.Fatal(e)
	}
	cleaned = true
	if !reflect.DeepEqual(before, tags()) {
		t.Fatal("cleanup changed named references")
	}
	beforeJSON, _ := json.Marshal(before)
	afterJSON, _ := json.Marshal(after)
	receipt := map[string]any{"status": "passed_actual_Unix_API_preflight_verified_tagless_image_import_and_exact_cleanup", "qualification": "Explicit synthetic small image/local protocol only, no application/container/native target qualification", "engine": observation, "inventory": inventory, "transport": result.receipt, "named_tags_before_sha256": wire.Digest(beforeJSON), "named_tags_after_sha256": wire.Digest(afterJSON), "imported_and_removed_exact_image": manifest, "container_created": false}
	file, e := os.OpenFile(evidence, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer file.Close()
	if e = json.NewEncoder(file).Encode(receipt); e != nil || file.Sync() != nil {
		t.Fatal("evidence failed", e)
	}
}
