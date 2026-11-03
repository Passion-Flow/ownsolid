//go:build offlineintegration

package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Real Unix Engine APIs, exact retained synthetic image and private synthetic bytes.
// No container process starts. Every disposable object has an explicit intent and cleanup.
func TestActualTypedPrivateVolumeProjectionAndNoReplay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	resources := wire.UtilityResources{MemoryBytes: 24 << 20, MilliCPU: 50, PIDs: 12}
	settings := configuration.Engine{SocketFile: os.Getenv("PROJECT_ONE_TEST_ENGINE_SOCKET_FILE"), Profile: "local-macos-amd64-emulation", DialSeconds: 2, RequestSeconds: 5, LoadSeconds: 30, ResponseBytes: 4 << 20, MaximumExpandedImageBytes: 64 << 20, Utility: &resources}
	c, e := New(settings)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	observation, e := c.Preflight(ctx)
	if e != nil {
		t.Fatal(e)
	}
	image := os.Getenv("PROJECT_ONE_TEST_IMAGE_MANIFEST")
	configDigest := os.Getenv("PROJECT_ONE_TEST_IMAGE_CONFIG")
	if _, found, e := c.Inspect(ctx, image); e != nil || found {
		t.Fatal("initially absent exact disposable image required", e)
	}
	var random [32]byte
	if _, e = rand.Read(random[:]); e != nil {
		t.Fatal(e)
	}
	owner := Ownership{ScopeID: hex.EncodeToString(random[:16]), OperationID: hex.EncodeToString(random[16:]), Role: "configuration"}
	output := os.Getenv("PROJECT_ONE_TEST_VOLUME_EVIDENCE")
	intent, e := os.OpenFile(output+".intent.json", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	if json.NewEncoder(intent).Encode(map[string]any{"status": "before_any_test_mutation", "owner": owner, "image": image, "qualification": LocalQualification}) != nil || intent.Sync() != nil || intent.Close() != nil {
		t.Fatal("durable intent failed")
	}
	imageLoaded := false
	volumeCreated := false
	utilityCreated := false
	cleaned := false
	cleanup := func() error {
		if utilityCreated {
			if e := c.RemoveUtility(context.Background(), owner, image, 10033, 10034); e != nil {
				return e
			}
			utilityCreated = false
		}
		if volumeCreated {
			if e := c.noForeignConsumers(context.Background(), owner, ""); e != nil {
				return e
			}
			if _, found, e := c.InspectVolume(context.Background(), owner); e != nil || !found {
				return failure("OFFLINE_ENGINE_TEST_VOLUME_CLEANUP_CONTEXT_FAILED")
			}
			_, status, e := c.request(context.Background(), http.MethodDelete, "/v1.49/volumes/"+url.PathEscape(owner.VolumeName()), url.Values{"force": {"0"}}, nil, settings.RequestSeconds)
			if e != nil || status != 204 {
				return failure("OFFLINE_ENGINE_TEST_VOLUME_CLEANUP_FAILED")
			}
			volumeCreated = false
		}
		if imageLoaded {
			value, found, e := c.Inspect(context.Background(), image)
			if e != nil || !found || len(value.RepoTags) > 0 {
				return failure("OFFLINE_ENGINE_TEST_IMAGE_CONTEXT_CHANGED")
			}
			raw, status, e := c.request(context.Background(), http.MethodGet, "/v1.49/containers/json", url.Values{"all": {"1"}}, nil, settings.RequestSeconds)
			if e != nil || status != 200 {
				return failure("OFFLINE_ENGINE_TEST_CONSUMER_CHECK_FAILED")
			}
			var consumers []struct{ ImageID string }
			if wire.UniqueJSON(raw, &consumers) != nil {
				return failure("OFFLINE_ENGINE_TEST_CONSUMER_CHECK_FAILED")
			}
			for _, consumer := range consumers {
				if consumer.ImageID == image || consumer.ImageID == configDigest {
					return failure("OFFLINE_ENGINE_TEST_IMAGE_CONSUMER_PRESERVED")
				}
			}
			_, status, e = c.request(context.Background(), http.MethodDelete, "/v1.49/images/"+url.PathEscape(image), url.Values{"force": {"0"}, "noprune": {"1"}}, nil, settings.RequestSeconds)
			if e != nil || status != 200 {
				return failure("OFFLINE_ENGINE_TEST_IMAGE_CLEANUP_FAILED")
			}
			imageLoaded = false
		}
		cleaned = true
		return nil
	}
	t.Cleanup(func() {
		if !cleaned {
			if e := cleanup(); e != nil {
				t.Error(e)
			}
		}
	})
	filename := os.Getenv("PROJECT_ONE_TEST_IMAGE_ARCHIVE")
	info, e := os.Stat(filename)
	if e != nil || info.Size() > 32<<20 {
		t.Fatal("bounded exact source required", e)
	}
	asset := wire.Asset{ID: "actual-synthetic-image", Path: "actual-image.tar", Kind: "image_archive", Size: info.Size(), SHA256: os.Getenv("PROJECT_ONE_TEST_IMAGE_ARCHIVE_SHA256"), ImageID: image, ImageConfigDigest: configDigest, Architecture: "amd64", Licenses: []string{"explicit-synthetic-fixture"}}
	verification := bundle.Defaults()
	verification.MountRoot = os.Getenv("PROJECT_ONE_TEST_EXTERNAL_ROOT")
	source, e := bundle.OpenImageSource(ctx, verification, filename, asset, settings.MaximumExpandedImageBytes)
	if e != nil {
		t.Fatal(e)
	}
	defer source.Close()
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() { _, e := source.WriteTagless(ctx, writer); writer.CloseWithError(e); done <- e }()
	e = c.LoadTagless(ctx, reader)
	reader.CloseWithError(e)
	produced := <-done
	// A failed load can still have produced the declared immutable image; cleanup inspects actual state.
	_, imageLoaded, _ = c.Inspect(context.Background(), image)
	if e != nil || produced != nil || !imageLoaded {
		t.Fatal("exact fixture load", e, produced)
	}
	volume, e := c.CreateVolume(ctx, owner)
	if e != nil {
		_, volumeCreated, _ = c.InspectVolume(context.Background(), owner)
		t.Fatal(e)
	}
	volumeCreated = true
	utility, e := c.CreateUtility(ctx, owner, image, 10033, 10034)
	if e != nil {
		_, utilityCreated, _ = c.InspectUtility(context.Background(), owner, image, 10033, 10034)
		t.Fatal(e)
	}
	utilityCreated = true
	files := []ConfigurationFile{{Name: "control.yaml", Contents: []byte("Explicit synthetic test configuration\n")}, {Name: "key-reference", Contents: []byte("Synthetic bytes, never a working key\n")}}
	if e = c.PutConfiguration(ctx, owner, image, 10033, 10034, files); e != nil {
		t.Fatal(e)
	}
	state, e := c.InspectConfiguration(ctx, owner, image, 10033, 10034, files)
	if e != nil || state.Status != "verified_private_volume_payload" || state.Files != 2 {
		t.Fatal(state, e)
	}
	if e = c.PutConfiguration(ctx, owner, image, 10033, 10034, files); e == nil || e.Error() != "OFFLINE_ENGINE_NONEMPTY_CONFIGURATION_PRESERVED" {
		t.Fatal("nonempty write not refused", e)
	}
	if _, e = c.CreateVolume(ctx, owner); e == nil {
		t.Fatal("existing volume adopted")
	}
	if _, e = c.CreateUtility(ctx, owner, image, 10033, 10034); e == nil {
		t.Fatal("existing utility adopted")
	}
	observed, found, e := c.InspectUtility(ctx, owner, image, 10033, 10034)
	if e != nil || !found || observed.ID != utility.ID || observed.State.PID != 0 {
		t.Fatal("utility ran or changed", e)
	}
	if e = cleanup(); e != nil {
		t.Fatal(e)
	}
	file, e := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer file.Close()
	receipt := map[string]any{"status": "passed_actual_typed_private_volume_projection_and_no_replay", "qualification": LocalQualification, "engine": observation, "owner": owner, "volume": volume, "never_started_utility": observed, "configuration": state, "container_started": false, "nonempty_write_refused": true, "existing_names_preserved": true, "exact_image_container_volume_cleaned": cleaned}
	if json.NewEncoder(file).Encode(receipt) != nil || file.Sync() != nil {
		t.Fatal("evidence write failed")
	}
}
