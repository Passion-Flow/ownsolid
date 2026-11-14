package factory

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func attestedImageFixture(t *testing.T, fault string) (Config, wire.Asset, string) {
	t.Helper()
	c, asset, path, _ := importFixture(t, "")
	files := tarFiles(t, mustFixtureRead(t, path))
	imageID := asset.ImageID
	subject := descriptor{MediaType: ociManifestType, Digest: imageID, Size: int64(len(files["blobs/sha256/"+strings.TrimPrefix(imageID, "sha256:")]))}
	statement := map[string]any{"_type": "https://in-toto.io/Statement/v1", "predicateType": "https://slsa.dev/provenance/v1", "subject": []any{map[string]any{"name": "pkg:docker/synthetic/image", "digest": map[string]string{"sha256": strings.TrimPrefix(imageID, "sha256:")}}}, "predicate": map[string]any{"buildDefinition": map[string]any{}}}
	switch fault {
	case "statement-subject":
		statement["subject"] = []any{map[string]any{"name": "synthetic", "digest": map[string]string{"sha256": strings.Repeat("a", 64)}}}
	case "statement-type":
		statement["_type"] = "arbitrary"
	case "statement-predicate":
		statement["predicate"] = nil
	case "predicate-type":
		statement["predicateType"] = "http://invalid.example/predicate"
	}
	statementRaw := Encode(statement)
	layer := descriptor{MediaType: inTotoType, Digest: "sha256:" + wire.Digest(statementRaw), Size: int64(len(statementRaw)), Annotations: map[string]string{attestationPredicateType: "https://slsa.dev/provenance/v1"}}
	empty := []byte("{}")
	config := descriptor{MediaType: ociEmptyType, Digest: "sha256:" + wire.Digest(empty), Size: 2, Data: base64.StdEncoding.EncodeToString(empty)}
	att := imageDocument{SchemaVersion: 2, ArtifactType: attestationManifestType, Subject: &subject, Config: config, Layers: []descriptor{layer}}
	switch fault {
	case "subject-digest":
		att.Subject.Digest = "sha256:" + strings.Repeat("a", 64)
	case "subject-size":
		att.Subject.Size++
	case "subject-url":
		att.Subject.URLs = []string{"https://invalid.example/never-fetch"}
	case "artifact-type":
		att.ArtifactType = "arbitrary"
	case "empty-config-type":
		att.Config.MediaType = "application/vnd.oci.image.config.v1+json"
	case "empty-config-inline":
		att.Config.Data = base64.StdEncoding.EncodeToString([]byte("bad"))
	case "config-url":
		att.Config.URLs = []string{"https://invalid.example/never-fetch"}
	case "layer-url":
		att.Layers[0].URLs = []string{"https://invalid.example/never-fetch"}
	case "layer-size":
		att.Layers[0].Size++
	case "layer-media":
		att.Layers[0].MediaType = "application/vnd.oci.image.layer.v1.tar+gzip"
	case "layer-annotation":
		att.Layers[0].Annotations[attestationPredicateType] = "https://invalid.example/other"
	}
	attRaw := Encode(att)
	d := descriptor{MediaType: ociManifestType, Digest: "sha256:" + wire.Digest(attRaw), Size: int64(len(attRaw)), Annotations: map[string]string{attestationReferenceType: "attestation-manifest", attestationReferenceDigest: imageID}}
	if json.Unmarshal([]byte(`{"architecture":"unknown","os":"unknown"}`), &d.Platform) != nil {
		t.Fatal("synthetic platform")
	}
	switch fault {
	case "descriptor-platform":
		d.Platform.Architecture = "amd64"
	case "descriptor-reference":
		d.Annotations[attestationReferenceDigest] = "sha256:" + strings.Repeat("b", 64)
	case "descriptor-type":
		d.Annotations[attestationReferenceType] = "arbitrary"
	case "descriptor-missing-type":
		delete(d.Annotations, attestationReferenceType)
	case "descriptor-data":
		d.Data = "e30="
	case "descriptor-url":
		d.URLs = []string{"https://invalid.example/never-fetch"}
	}
	var index imageDocument
	if imageJSON(files["index.json"], &index) != nil {
		t.Fatal("synthetic root index")
	}
	index.Manifests = append(index.Manifests, d)
	if fault == "visited-descriptor" {
		bad := index.Manifests[0]
		bad.Platform = d.Platform
		bad.Annotations = d.Annotations
		index.Manifests = append(index.Manifests, bad)
	}
	files["index.json"] = Encode(index)
	files["blobs/sha256/"+wire.Digest(attRaw)] = attRaw
	files["blobs/sha256/"+wire.Digest(empty)] = empty
	files["blobs/sha256/"+wire.Digest(statementRaw)] = statementRaw
	if fault == "missing-statement" {
		delete(files, "blobs/sha256/"+wire.Digest(statementRaw))
	}
	var output bytes.Buffer
	archive := tar.NewWriter(&output)
	names := []string{}
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		value := files[name]
		if e := archive.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(value)), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}); e != nil {
			t.Fatal(e)
		}
		if _, e := archive.Write(value); e != nil {
			t.Fatal(e)
		}
	}
	if e := archive.Close(); e != nil {
		t.Fatal(e)
	}
	path = filepath.Join(c.ArtifactRoot, "synthetic-attested-image.tar")
	put(t, path, output.Bytes())
	asset.Size = int64(output.Len())
	asset.SHA256 = wire.Digest(output.Bytes())
	asset.Path = "synthetic-attested-image.tar"
	return c, asset, path
}

func mustFixtureRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, e := readRegular(path, 1<<20, true)
	if e != nil {
		t.Fatal(e)
	}
	return raw
}

func TestAttestedImageClosureAndTaglessImport(t *testing.T) {
	for _, fault := range []string{"valid", "statement-subject", "statement-type", "statement-predicate", "predicate-type", "subject-digest", "subject-size", "subject-url", "artifact-type", "empty-config-type", "empty-config-inline", "config-url", "layer-url", "layer-size", "layer-media", "layer-annotation", "descriptor-platform", "descriptor-reference", "descriptor-type", "descriptor-missing-type", "descriptor-data", "descriptor-url", "visited-descriptor", "missing-statement"} {
		t.Run(fault, func(t *testing.T) {
			c, asset, path := attestedImageFixture(t, fault)
			source, e := OpenImageSource(context.Background(), c, path, asset, 1<<20)
			if fault != "valid" {
				if e == nil {
					source.Close()
					t.Fatal("invalid attestation accepted")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			defer source.Close()
			var output bytes.Buffer
			receipt, e := source.WriteTagless(context.Background(), &output)
			if e != nil || receipt.SourceSHA256 != asset.SHA256 {
				t.Fatal("tagless attested image", e)
			}
			files := tarFiles(t, output.Bytes())
			for _, raw := range files {
				if bytes.Contains(raw, []byte(attestationManifestType)) || bytes.Contains(raw, []byte("slsa.dev")) {
					t.Fatal("attestation entered runtime import transport")
				}
			}
			if source.Inventory().ManifestDigest != asset.ImageID || len(source.Inventory().Layers) != 1 {
				t.Fatal("runtime inventory changed")
			}
		})
	}
}
