package imageregistry

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Explicit synthetic metadata exercises only persisted-state classification.
// It is not a valid manufacturer signature, image, registry or SQL acceptance.
func syntheticStoredBinding(t *testing.T) Pin {
	t.Helper()
	digest := "sha256:" + strings.Repeat("a", 64)
	config := "sha256:" + strings.Repeat("b", 64)
	archive := strings.Repeat("c", 64)
	image := cachedImage{
		Asset:     release.Asset{ID: "synthetic-state-only", Path: "images/synthetic.tar", Kind: "image_archive", Size: 1, SHA256: archive, Licenses: []string{"Apache-2.0"}, ImageID: digest, ImageConfigDigest: config, Architecture: "amd64"},
		ReleaseID: "synthetic-state-only", ReleaseManifestSHA256: archive, ReleaseArchiveSHA256: archive,
		Inventory:   bundle.ImageInventory{ManifestDigest: digest, ConfigDigest: config, OS: "linux", Architecture: "amd64", DiffIDs: []string{}, ExpandedBytes: 1},
		Archive:     bundle.ImportReceipt{SourceSHA256: archive, TransportSHA256: archive, TransportBytes: 1, ExpandedBytes: 1},
		Descriptors: []Descriptor{{Digest: digest, Size: 1, MediaType: "application/vnd.oci.image.manifest.v1+json", Kind: "manifest"}, {Digest: config, Size: 1, MediaType: "application/vnd.oci.image.config.v1+json", Kind: "config"}},
		Proof:       bundle.ImageProof{FormatVersion: 1, Manifest: []byte(`{"explicit":"synthetic-untrusted"}`), Signature: []byte(`{"explicit":"synthetic-untrusted"}`), AssetID: "synthetic-state-only"},
	}
	if err := json.Unmarshal([]byte(`{"layers":[]}`), &image.Inventory); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(image)
	if err != nil {
		t.Fatal(err)
	}
	hash := release.Digest(raw)
	bytes := int64(1)
	return Pin{AssetID: image.Asset.ID, ManifestDigest: &digest, ConfigDigest: &config, ArchiveSHA256: &archive, ArchiveBytes: &bytes, inventorySHA: &hash, inventory: raw}
}

func TestStoredRegistryIntentStateContracts(t *testing.T) {
	owner := "11111111-1111-4111-8111-111111111111"
	for _, state := range []string{"importing", "ready", "unknown", "deleting", "failed", "deleted", "unrecognized"} {
		for _, phase := range []string{"prepared", "remote", "completed", "unknown", "unrecognized"} {
			for _, bound := range []bool{false, true} {
				p := Pin{State: state}
				if bound {
					p = syntheticStoredBinding(t)
					p.State = state
				}
				accepted := state == "importing" && (!bound && phase == "prepared" || bound && phase == "remote") || state == "unknown" && bound && (phase == "remote" || phase == "unknown") || state == "ready" && bound && phase == "completed" || state == "deleting" && bound && phase == "completed" || state == "failed" && !bound && phase == "prepared" || state == "deleted" && (!bound && phase == "prepared" || bound && phase == "completed")
				availability, err := storedPinState(p, owner, phase)
				if (err == nil) != accepted {
					t.Fatalf("state=%s phase=%s bound=%v accepted=%v: %v", state, phase, bound, accepted, err)
				}
				if accepted && ((availability == storedPinReady) != (state == "ready") || (availability == storedPinDeleted) != (state == "deleted")) {
					t.Fatal("quarantined state became ready or a tombstone")
				}
			}
		}
	}
}

func TestStoredRegistryIdentityAndPartialBindingsRefuse(t *testing.T) {
	for _, owner := range []string{"", "not-a-uuid", "00000000-0000-0000-0000-000000000000", "11111111111141118111111111111111"} {
		p := Pin{State: "importing"}
		if _, err := storedPinState(p, owner, "prepared"); err == nil {
			t.Fatal("unproven intent owner admitted")
		}
	}
	for _, fault := range []string{"manifest", "config", "archive", "size", "inventory", "inventory-hash", "bad-digest", "bad-size"} {
		p := syntheticStoredBinding(t)
		p.State = "unknown"
		switch fault {
		case "manifest":
			p.ManifestDigest = nil
		case "config":
			p.ConfigDigest = nil
		case "archive":
			p.ArchiveSHA256 = nil
		case "size":
			p.ArchiveBytes = nil
		case "inventory":
			p.inventory = nil
		case "inventory-hash":
			p.inventorySHA = nil
		case "bad-digest":
			bad := "sha256:invalid"
			p.ManifestDigest = &bad
		case "bad-size":
			bad := int64(0)
			p.ArchiveBytes = &bad
		}
		if _, err := storedPinState(p, "11111111-1111-4111-8111-111111111111", "unknown"); err == nil {
			t.Fatal("partial immutable binding admitted", fault)
		}
	}
}

func TestStoredRegistryMetadataRemainsBoundAndBounded(t *testing.T) {
	p := syntheticStoredBinding(t)
	if err := storedBoundMetadata(p, int64(len(p.inventory))); err != nil {
		t.Fatal("canonical synthetic SQL binding rejected", err)
	}
	for _, fault := range []string{"size", "asset", "digest", "unknown-field", "duplicate", "malformed"} {
		bad := p
		limit := int64(len(p.inventory)) + 128
		switch fault {
		case "size":
			limit = int64(len(p.inventory)) - 1
		case "asset":
			bad.AssetID = "foreign-asset"
		case "digest":
			changed := strings.Repeat("d", 64)
			bad.inventorySHA = &changed
		case "unknown-field":
			bad.inventory = append([]byte(`{"untrusted":false,`), p.inventory[1:]...)
		case "duplicate":
			bad.inventory = append([]byte(`{"release_id":"duplicate",`), p.inventory[1:]...)
		case "malformed":
			bad.inventory = []byte("{")
		}
		if storedBoundMetadata(bad, limit) == nil {
			t.Fatal("unbound stored metadata admitted", fault)
		}
	}
}

func TestStoredRegistryDatabaseJSONNormalizationPreservesCanonicalBudget(t *testing.T) {
	p := syntheticStoredBinding(t)
	maximum := int64(len(p.inventory))
	var value any
	if err := json.Unmarshal(p.inventory, &value); err != nil {
		t.Fatal(err)
	}
	normalized, err := json.MarshalIndent(value, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(normalized)) <= maximum {
		t.Fatal("fixture must enlarge and reorder the stored JSON representation")
	}
	p.inventory = normalized
	if err := storedBoundMetadata(p, maximum); err != nil {
		t.Fatal("database normalization changed the canonical metadata budget", err)
	}
	if storedBoundMetadata(p, maximum-1) == nil {
		t.Fatal("normalized representation bypassed the canonical budget")
	}
}

func TestStoredRegistryRawJSONDecodingRemainsBounded(t *testing.T) {
	p := syntheticStoredBinding(t)
	maximum := int64(len(p.inventory))
	p.inventory = append(p.inventory, []byte(strings.Repeat(" ", int(maximum)))...)
	if err := storedBoundMetadata(p, maximum); err != nil {
		t.Fatal("exact fixed raw codec allowance rejected", err)
	}
	p.inventory = append(p.inventory, ' ')
	if storedBoundMetadata(p, maximum) == nil {
		t.Fatal("excess raw expansion admitted")
	}
	for _, limit := range []int64{0, -1, math.MaxInt64} {
		if storedBoundMetadata(p, limit) == nil {
			t.Fatal("invalid or overflowing decoding budget admitted", limit)
		}
	}
}

func TestStoredRegistryVerificationRejectsNilAndCancelledContext(t *testing.T) {
	s := &Service{}
	for _, verify := range []func(context.Context) error{s.VerifyStored, s.VerifyControlState} {
		if verify(nil) == nil {
			t.Fatal("nil verification context admitted")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if verify(ctx) == nil {
			t.Fatal("cancelled verification admitted")
		}
	}
}
