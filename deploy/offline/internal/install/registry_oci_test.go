package install

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
)

func TestRegistryUploadLocationIsConstrained(t *testing.T) {
	const base = "https://10.77.108.1:5443"
	const prefix = "/v2/ownsolid/release/control-image/blobs/uploads/"
	const state = "upload-1?_state=a%3D"
	c := registryClient{base: base}
	digest := "sha256:" + wire.Digest([]byte("blob"))
	for _, tc := range []struct {
		name, location string
		accepted       bool
	}{
		{"same endpoint", base + prefix + state, true},
		{"foreign address", "https://10.77.108.2:5443" + prefix + state, false},
		{"cleartext", "http://10.77.108.1:5443" + prefix + state, false},
		{"other repo", base + strings.Replace(prefix, "control-image", "postgres-image", 1) + state, false},
		{"encoded separator", base + prefix + "other%2F" + state, false},
		{"extra query", base + prefix + state + "&digest=other", false},
		{"duplicate state", base + prefix + state + "&_state=other", false},
		{"fragment", base + prefix + state + "#fragment", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			location, err := url.Parse(tc.location)
			if err != nil {
				t.Fatal(err)
			}
			path, err := c.uploadLocation(prefix, digest, location)
			if !tc.accepted {
				if err == nil {
					t.Fatalf("accepted upload to %q", path)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			resolved, err := url.Parse(path)
			if err != nil || resolved.Path != prefix+"upload-1" || resolved.Query().Get("_state") != "a=" || resolved.Query().Get("digest") != digest {
				t.Fatalf("invalid upload URL %q", path)
			}
		})
	}
}

func TestRegistryReadbackVerifiesManifestAndBlobBytes(t *testing.T) {
	const content = "signed"
	const mediaType = "application/vnd.oci.image.manifest.v1+json"
	for _, tc := range []struct {
		name, body, media string
		manifest, valid   bool
	}{
		{"manifest", content, mediaType, true, true},
		{"changed manifest type", content, "application/json", true, false},
		{"blob", content, "application/octet-stream", false, true},
		{"changed blob", "broken", "application/octet-stream", false, false},
		{"oversized blob", content + "extra", "application/octet-stream", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := bundle.RuntimeBlob{
				Digest: "sha256:" + wire.Digest([]byte(content)), MediaType: mediaType,
				Size: int64(len(content)), Manifest: tc.manifest,
			}
			repo := "ownsolid/release/control-image"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != registryPath(repo, b) {
					t.Error("readback requested a different immutable reference")
				}
				w.Header().Set("Content-Type", tc.media)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			client := registryClient{base: server.URL, client: server.Client()}
			if err := client.verify(t.Context(), repo, b); (err == nil) != tc.valid {
				t.Fatalf("unexpected readback outcome %v", err)
			}
		})
	}
}
