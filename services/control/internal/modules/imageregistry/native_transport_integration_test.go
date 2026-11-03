//go:build registry_integration

package imageregistry

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/registryfixture"
)

// These are injected only by the bounded native quality producer. No ambient
// deployment settings or caller credentials are adopted by the Registry child.
func nativeTransportInputs(t *testing.T) (string, string, int, int) {
	t.Helper()
	binary, sha := os.Getenv("PROJECT_ONE_REGISTRY_NATIVE_BINARY"), os.Getenv("PROJECT_ONE_REGISTRY_NATIVE_SHA256")
	seconds, e := strconv.Atoi(os.Getenv("PROJECT_ONE_REGISTRY_NATIVE_OPERATION_SECONDS"))
	if e != nil || seconds < 1 || seconds > 300 {
		t.Fatal("REGISTRY_NATIVE_TYPED_DEADLINE_REQUIRED")
	}
	memory, e := strconv.Atoi(os.Getenv("PROJECT_ONE_REGISTRY_NATIVE_MEMORY_MIB"))
	if e != nil || memory < 128 || memory > 2048 {
		t.Fatal("REGISTRY_NATIVE_TYPED_MEMORY_REQUIRED")
	}
	if !filepath.IsAbs(binary) || len(sha) != 64 {
		t.Fatal("REGISTRY_NATIVE_BOUND_ARTIFACT_REQUIRED")
	}
	return binary, sha, seconds, memory
}

type nativeOCIReference struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}
type nativeTransportImage struct {
	config, layer         Descriptor
	configFile, layerFile string
	diffID                string
}

func nativeTransportBytes(t *testing.T, root string) nativeTransportImage {
	t.Helper()
	// Real test-only OCI bytes with one harmless text file, never an approved
	// M06 image, signed release proof, model-serving artifact or executable claim.
	var layer bytes.Buffer
	archive := tar.NewWriter(&layer)
	content := []byte("Explicit isolated Registry protocol fixture.\n")
	if e := archive.WriteHeader(&tar.Header{Name: "fixture.txt", Mode: 0644, Size: int64(len(content)), ModTime: time.Unix(0, 0)}); e != nil {
		t.Fatal(e)
	}
	if _, e := archive.Write(content); e != nil {
		t.Fatal(e)
	}
	if e := archive.Close(); e != nil {
		t.Fatal(e)
	}
	diff := digestBytes(layer.Bytes())
	var compressed bytes.Buffer
	zip := gzip.NewWriter(&compressed)
	if _, e := zip.Write(layer.Bytes()); e != nil {
		t.Fatal(e)
	}
	if e := zip.Close(); e != nil {
		t.Fatal(e)
	}
	config, e := json.Marshal(map[string]any{"architecture": "amd64", "os": "linux", "rootfs": map[string]any{"type": "layers", "diff_ids": []string{diff}}, "config": map[string]any{"Labels": map[string]string{"org.opencontainers.image.title": "Explicit isolated protocol fixture"}}})
	if e != nil {
		t.Fatal(e)
	}
	image := nativeTransportImage{config: Descriptor{Digest: digestBytes(config), Size: int64(len(config)), MediaType: "application/vnd.oci.image.config.v1+json", Kind: "config"}, layer: Descriptor{Digest: digestBytes(compressed.Bytes()), Size: int64(compressed.Len()), MediaType: "application/vnd.oci.image.layer.v1.tar+gzip", Kind: "layer"}, configFile: filepath.Join(root, "fixture-config.json"), layerFile: filepath.Join(root, "fixture-layer.gz"), diffID: diff}
	for name, data := range map[string][]byte{image.configFile: config, image.layerFile: compressed.Bytes()} {
		f, e := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			t.Fatal(e)
		}
		_, e = f.Write(data)
		if e == nil {
			e = f.Sync()
		}
		closeErr := f.Close()
		if e != nil || closeErr != nil {
			t.Fatal("actual fixture bytes unavailable", e, closeErr)
		}
	}
	return image
}
func (i nativeTransportImage) manifest(t *testing.T, variant string) (Descriptor, []byte) {
	t.Helper()
	ref := func(d Descriptor) nativeOCIReference { return nativeOCIReference{d.MediaType, d.Digest, d.Size} }
	raw, e := json.Marshal(struct {
		SchemaVersion int                  `json:"schemaVersion"`
		MediaType     string               `json:"mediaType"`
		Config        nativeOCIReference   `json:"config"`
		Layers        []nativeOCIReference `json:"layers"`
		Annotations   map[string]string    `json:"annotations"`
	}{2, ociManifest, ref(i.config), []nativeOCIReference{ref(i.layer)}, map[string]string{"org.opencontainers.image.description": variant}})
	if e != nil {
		t.Fatal(e)
	}
	return Descriptor{Digest: digestBytes(raw), Size: int64(len(raw)), MediaType: ociManifest, Kind: "manifest"}, raw
}
func nativeTransportConfiguration(n *registryfixture.Native) Config {
	c := Defaults()
	c.Endpoint = n.Endpoint
	c.AllowedCIDRs = []string{"127.0.0.1/32"}
	c.CAFile = n.CAFile
	c.CredentialFile = n.CredentialFile
	c.StoreRoot = filepath.Join(n.Root, "unused-cas")
	c.PreparedReleaseRoot = filepath.Join(n.Root, "unused-release")
	c.Verification.MountRoot = n.Root
	c.Verification.TrustFile = filepath.Join(n.Root, "unused-trust.json")
	// Unused synthetic release metadata. This transport-only test never calls
	// release/image approval and cannot create an accepted product pin.
	c.ReleaseArchiveSHA256 = strings.Repeat("a", 64)
	c.ReleaseManifestSHA256 = strings.Repeat("b", 64)
	return c
}

func TestActualNativeRegistryTransport(t *testing.T) {
	binary, sha, seconds, memory := nativeTransportInputs(t)
	for _, name := range []string{"actual_tls_auth_blob_manifest_roundtrip", "actual_mutable_tag_preserves_digest_reads", "actual_restart_and_gc"} {
		t.Run(name, func(t *testing.T) {
			n := registryfixture.New(t, binary, sha, seconds, memory)
			remote, e := newDistribution(nativeTransportConfiguration(n))
			if e != nil {
				t.Fatal(e)
			}
			defer remote.Close()
			ctx, cancel := context.WithTimeout(t.Context(), time.Duration(seconds)*time.Second)
			defer cancel()
			image := nativeTransportBytes(t, n.Root)
			repo := repository(uuid.NewString(), uuid.NewString(), uuid.NewString())
			first, raw := image.manifest(t, "first isolated protocol fixture")
			t.Logf("actual utility OCI only: config=%s layer=%s diff_id=%s manifest=%s", image.config.Digest, image.layer.Digest, image.diffID, first.Digest)
			if name == "actual_tls_auth_blob_manifest_roundtrip" {
				request, e := http.NewRequestWithContext(ctx, http.MethodGet, n.Endpoint+"/v2/", nil)
				if e != nil {
					t.Fatal(e)
				}
				response, e := remote.http.Do(request)
				if e != nil {
					t.Fatal(e)
				}
				response.Body.Close()
				if response.StatusCode != http.StatusUnauthorized {
					t.Fatal("actual native Registry accepted no credential")
				}
			}
			for _, entry := range []struct {
				descriptor Descriptor
				file       string
			}{{image.config, image.configFile}, {image.layer, image.layerFile}} {
				if e = remote.PutBlob(ctx, repo, entry.descriptor, entry.file); e != nil {
					t.Fatal("actual native blob upload failed", e)
				}
				if e = remote.Verify(ctx, repo, entry.descriptor); e != nil {
					t.Fatal("actual independent blob read failed", e)
				}
			}
			if e = remote.PutManifest(ctx, repo, first, raw); e != nil {
				t.Fatal("actual native manifest upload failed", e)
			}
			if e = remote.VerifyTag(ctx, repo, first); e != nil {
				t.Fatal(e)
			}
			if name == "actual_tls_auth_blob_manifest_roundtrip" {
				wrong := *remote
				wrong.credential.Password = "explicit-incorrect-fixture-password"
				if wrong.Verify(ctx, repo, first) == nil {
					t.Fatal("actual Registry accepted wrong credential")
				}
				if e = remote.Verify(ctx, repository(uuid.NewString(), uuid.NewString(), uuid.NewString()), first); e == nil || e.Error() != "REGISTRY_CONTENT_MISSING" {
					t.Fatal("another namespace exposed data", e)
				}
			}
			if name == "actual_mutable_tag_preserves_digest_reads" {
				second, raw := image.manifest(t, "second distinct isolated protocol fixture")
				if second.Digest == first.Digest {
					t.Fatal("fixture manifests unexpectedly identical")
				}
				if e = remote.PutManifest(ctx, repo, second, raw); e != nil {
					t.Fatal(e)
				}
				if e = remote.Verify(ctx, repo, first); e != nil {
					t.Fatal("mutable tag changed old digest bytes", e)
				}
				if remote.VerifyTag(ctx, repo, first) == nil {
					t.Fatal("moved tag matched old digest")
				}
				if e = remote.VerifyTag(ctx, repo, second); e != nil {
					t.Fatal(e)
				}
			}
			if name == "actual_restart_and_gc" {
				n.GC()
				for _, d := range []Descriptor{first, image.config, image.layer} {
					if e = remote.Verify(ctx, repo, d); e != nil {
						t.Fatal("referenced bytes lost during actual restart/GC", e)
					}
				}
				if e = remote.Remove(ctx, repo, first.Digest); e != nil {
					t.Fatal("actual owned removal uncertain", e)
				}
				n.GC()
				for _, d := range []Descriptor{first, image.config, image.layer} {
					if e = remote.Verify(ctx, repo, d); e == nil || e.Error() != "REGISTRY_CONTENT_MISSING" {
						t.Fatal("actual deleted/unreferenced content remained", e)
					}
				}
			}
		})
	}
}
