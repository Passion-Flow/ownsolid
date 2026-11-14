package factory

import (
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func TestCurrentReferrerMustBeLocalNonRuntimeMetadata(t *testing.T) {
	blobs := map[string]imageBlob{}
	makeBlob := func(mediaType string, raw []byte) descriptor {
		d := descriptor{MediaType: mediaType, Digest: "sha256:" + wire.Digest(raw), Size: int64(len(raw))}
		blobs[d.Digest] = imageBlob{d.Size, raw}
		return d
	}
	layer := makeBlob(inTotoType, []byte(`{"_type":"https://in-toto.io/Statement/v1"}`))
	runtime := makeBlob(ociManifestType, []byte(`{"schemaVersion":2}`))
	config := func(arch string) descriptor {
		return makeBlob("application/vnd.oci.image.config.v1+json", Encode(map[string]any{
			"architecture": arch, "os": "unknown", "rootfs": map[string]any{"type": "layers", "diff_ids": []string{layer.Digest}},
		}))
	}
	asset := wire.Asset{ImageID: runtime.Digest}
	desc := descriptor{MediaType: ociManifestType, Annotations: map[string]string{containerdSubject: runtime.Digest}}
	for _, test := range []struct {
		name string
		edit func(*imageDocument)
		ok   bool
	}{
		{"old BuildKit config", func(d *imageDocument) { d.Config = config("unknown") }, true},
		{"OCI artifact config", func(d *imageDocument) {
			d.ArtifactType, d.Subject, d.Config = attestationManifestType, &runtime, makeBlob(ociEmptyType, []byte("{}"))
		}, true},
		{"runnable config", func(d *imageDocument) { d.Config = config("amd64") }, false},
		{"external layer", func(d *imageDocument) { d.Layers[0].URLs = []string{"https://registry.example.test/blob"} }, false},
		{"missing layer", func(d *imageDocument) { d.Layers[0].Digest = "sha256:" + wire.Digest([]byte("missing")) }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			doc := imageDocument{Config: config("unknown"), Layers: []descriptor{layer}}
			test.edit(&doc)
			if err := checkCurrentReferrer(desc, doc, blobs, asset); (err == nil) != test.ok {
				t.Fatalf("valid=%t, got %v", test.ok, err)
			}
		})
	}
	desc.Annotations[containerdSubject] = layer.Digest
	if err := checkCurrentReferrer(desc, imageDocument{Config: config("unknown"), Layers: []descriptor{layer}}, blobs, asset); err == nil {
		t.Fatal("referrer of another image was accepted")
	}
}
