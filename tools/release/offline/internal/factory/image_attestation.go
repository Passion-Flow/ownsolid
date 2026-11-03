package factory

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const (
	attestationReferenceType   = "vnd.docker.reference.type"
	attestationReferenceDigest = "vnd.docker.reference.digest"
	attestationManifestType    = "application/vnd.docker.attestation.manifest.v1+json"
	ociManifestType            = "application/vnd.oci.image.manifest.v1+json"
	ociEmptyType               = "application/vnd.oci.empty.v1+json"
	inTotoType                 = "application/vnd.in-toto+json"
	attestationPredicateType   = "in-toto.io/predicate-type"
)

// Attestations are bounded signed-bundle metadata, never runnable images or
// independent evidence of provenance authenticity, review or target support.
// This initial path accepts the current BuildKit OCI-artifact representation.
func validateAttestation(d descriptor, doc imageDocument, blobs map[string]imageBlob, asset wire.Asset) error {
	bad := func() error { return fail("RELEASE_OCI_ATTESTATION_INVALID") }
	if d.MediaType != ociManifestType || d.Data != "" || d.Annotations[attestationReferenceType] != "attestation-manifest" || d.Annotations[attestationReferenceDigest] != asset.ImageID || d.Platform == nil || d.Platform.OS != "unknown" || d.Platform.Architecture != "unknown" || d.Platform.Variant != "" || doc.ArtifactType != attestationManifestType || doc.Subject == nil || len(doc.Manifests) != 0 || len(doc.Layers) == 0 {
		return bad()
	}
	subject := *doc.Subject
	target, found := blobs[asset.ImageID]
	if !found || subject.MediaType != ociManifestType || subject.Digest != asset.ImageID || subject.Size != target.size || len(subject.URLs) != 0 || subject.Platform != nil || subject.Data != "" {
		return bad()
	}
	config, found := blobs[doc.Config.Digest]
	if !found || doc.Config.MediaType != ociEmptyType || doc.Config.Size != 2 || doc.Config.Size != config.size || doc.Config.Digest != "sha256:"+wire.Digest([]byte("{}")) || !bytes.Equal(config.data, []byte("{}")) || len(doc.Config.URLs) != 0 || doc.Config.Platform != nil {
		return bad()
	}
	if doc.Config.Data != "" {
		inline, e := base64.StdEncoding.Strict().DecodeString(doc.Config.Data)
		if e != nil || !bytes.Equal(inline, config.data) {
			return bad()
		}
	}
	for _, layer := range doc.Layers {
		blob, found := blobs[layer.Digest]
		if !found || blob.size != layer.Size || layer.Size < 1 || layer.MediaType != inTotoType || len(layer.URLs) != 0 || layer.Platform != nil || layer.Data != "" || len(blob.data) == 0 {
			return bad()
		}
		var statement struct {
			Type          string `json:"_type"`
			PredicateType string `json:"predicateType"`
			Subject       []struct {
				Name   string            `json:"name"`
				Digest map[string]string `json:"digest"`
			} `json:"subject"`
			Predicate json.RawMessage `json:"predicate"`
		}
		if imageJSON(blob.data, &statement) != nil || statement.Type != "https://in-toto.io/Statement/v1" && statement.Type != "https://in-toto.io/Statement/v0.1" || len(statement.Subject) != 1 || statement.Subject[0].Name == "" || len(statement.Subject[0].Digest) != 1 || statement.Subject[0].Digest["sha256"] != strings.TrimPrefix(asset.ImageID, "sha256:") || statement.PredicateType != layer.Annotations[attestationPredicateType] {
			return bad()
		}
		predicateURL, e := url.Parse(statement.PredicateType)
		if e != nil || predicateURL.Scheme != "https" || predicateURL.Host == "" || predicateURL.User != nil {
			return bad()
		}
		var predicate map[string]any
		if imageJSON(statement.Predicate, &predicate) != nil || predicate == nil {
			return bad()
		}
	}
	return nil
}
